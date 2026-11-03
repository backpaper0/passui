package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Middleware は共通のミドルウェアを next に掛ける。New はこれでルート全体を包む。
func Middleware(cfg Config, logger *slog.Logger, next http.Handler) http.Handler {
	// 外側から順に掛かる。no-store はどの応答にも付くよう最も外側に置き、
	// recover はアクセスログが 500 を記録できるようアクセスログの内側に置く。
	// Origin の検証は Host を検証した後に、Host から自分のオリジンを組み立てて行う。
	h := next
	h = checkOrigin(h)
	h = checkHost(cfg, logger, h)
	h = recoverPanic(logger, h)
	h = accessLog(logger, h)
	h = noStore(h)
	return h
}

// accessLog はメソッド、クエリを落としたパス、ステータス、所要時間を info で出す。
// クエリ文字列にはログイン トークンや検索語が入るので、決して出さない。
func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		logger.InfoContext(r.Context(), "http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration", time.Since(start),
		)
	})
}

// recoverPanic はハンドラの panic を捕まえ、スタックを error でログに出し、
// 定型メッセージとエラー ID を返す。
func recoverPanic(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v)
			}
			attrs := []any{"panic", fmt.Sprint(v), "stack", string(debug.Stack())}
			if sw.wroteHeader {
				// 既にヘッダを送ったので、応答は差し替えられない。ログにだけ出す。
				logFailure(r.Context(), logger, "panic recovered", attrs...)
				return
			}
			writeFailure(sw, r, logger, "panic recovered", attrs...)
		}()
		next.ServeHTTP(sw, r)
	})
}

// statusWriter は書いたステータスを覚えておく。
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap は http.ResponseController が元の ResponseWriter に届くようにする。
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// AllowedHosts は Handler が受け付ける Host ヘッダの一覧を返す（DNS rebinding 対策）。
func AllowedHosts(port int) []string {
	p := strconv.Itoa(port)
	return []string{"passui.localhost:" + p, "localhost:" + p, "127.0.0.1:" + p}
}

// checkHost は許可していない Host ヘッダのリクエストを 421 で拒否する。
func checkHost(cfg Config, logger *slog.Logger, next http.Handler) http.Handler {
	allowed := AllowedHosts(cfg.Port)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(allowed, strings.ToLower(r.Host)) {
			logger.WarnContext(r.Context(), "rejected host", "host", r.Host)
			http.Error(w, "許可されていない Host です", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// noStore はすべてのレスポンスに Cache-Control: no-store を付ける。
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
