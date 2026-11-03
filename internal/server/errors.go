package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/backpaper0/passui/internal/store"
)

// storeFailure はストア バックエンドのエラーを、応答のステータスとブラウザに出す
// メッセージに分類する。分類できないエラー（そのほかの失敗）は op と attrs と一緒に
// error でログに出し、「操作に失敗しました」とエラー ID を返す。エラー文（子プロセスの
// stderr などを含みうる）はブラウザに出さない。attrs にシークレットを入れないこと。
func (s *handler) storeFailure(r *http.Request, err error, op string, attrs ...any) (int, string) {
	switch {
	case errors.Is(err, store.ErrLocked):
		return http.StatusLocked, "ロック中です。ゲストのターミナルで解錠してください"
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound, "エントリが見つかりません"
	case errors.Is(err, store.ErrExists):
		return http.StatusConflict, "同じ名前のエントリが既にあります"
	case errors.Is(err, store.ErrIsDirectory):
		return http.StatusConflict, "同じ名前のフォルダがあります"
	case errors.Is(err, store.ErrConflict):
		return http.StatusConflict, "エントリがほかで変更されています"
	}
	id := logFailure(r.Context(), s.logger, op, append(attrs, "err", err)...)
	return http.StatusInternalServerError, failureMessage(id)
}

// newErrorID はブラウザとログを結び付けるエラー ID（16 桁の 16 進数）を作る。
func newErrorID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand.Read は失敗しない
	return hex.EncodeToString(b)
}

// failureMessage は失敗したときにブラウザに返す定型メッセージ。
func failureMessage(errorID string) string {
	return fmt.Sprintf("操作に失敗しました（エラー ID: %s）", errorID)
}

// logFailure はエラー ID を作り、msg と attrs を error でログに出して、エラー ID を返す。
// attrs にシークレット（エントリの中身、トークン、フォームの値）を入れてはいけない。
func logFailure(ctx context.Context, logger *slog.Logger, msg string, attrs ...any) string {
	id := newErrorID()
	logger.ErrorContext(ctx, msg, append([]any{"error_id", id}, attrs...)...)
	return id
}

// writeFailure は「操作に失敗しました」とエラー ID だけを 500 で返す。
// ハンドラで想定外の失敗が起きたときに使う。詳細はログにだけ出す。
func writeFailure(w http.ResponseWriter, r *http.Request, logger *slog.Logger, msg string, attrs ...any) {
	id := logFailure(r.Context(), logger, msg, attrs...)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, "<p>%s</p>\n", template.HTMLEscapeString(failureMessage(id)))
}
