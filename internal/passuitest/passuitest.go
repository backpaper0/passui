// Package passuitest は HTTP テストと統合テストのためのヘルパである。
//
// すべての HTTP テストと統合テストは、このパッケージ経由で Handler を立ち上げる。
// slog の出力を debug レベルでバッファに集め、t.Cleanup で、登録したカナリアが
// どれもログに出ていないことを確かめる。
package passuitest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/backpaper0/passui/internal/backup"
	"github.com/backpaper0/passui/internal/secret"
	"github.com/backpaper0/passui/internal/server"
	"github.com/backpaper0/passui/internal/store"
)

// DefaultPort は Options.Port を省いたときのポート。
const DefaultPort = 3560

// Harness はログのバッファとカナリアを持つ。テストごとに New で作る。
type Harness struct {
	t      testing.TB
	buf    *syncBuffer
	Logger *slog.Logger

	mu       sync.Mutex
	canaries []string
}

// New は Harness を作り、t.Cleanup でカナリアの検査を登録する。
// Logger は debug レベルで、出力はすべてバッファに入る。ストア バックエンドなど、
// Handler の外のコンポーネントにもこの Logger を渡すこと。
func New(t testing.TB) *Harness {
	t.Helper()
	buf := &syncBuffer{}
	h := &Harness{
		t:      t,
		buf:    buf,
		Logger: slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	t.Cleanup(h.checkCanaries)
	return h
}

// Canary は一意のカナリア文字列を作って登録し、返す。label は失敗時の表示用。
// 登録したカナリアがログに 1 つでも現れると、テストの終わりに失敗する。
func (h *Harness) Canary(label string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		h.t.Fatal(err)
	}
	c := "CANARY" + label + hex.EncodeToString(b)
	h.AddCanary(c)
	return c
}

// AddCanary は既にある文字列をカナリアとして登録する。
func (h *Harness) AddCanary(s string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.canaries = append(h.canaries, s)
}

// LogWriter は Logger と同じバッファに書く io.Writer を返す。自前でロガーを作る
// コンポーネント（cli.Run の stderr など）の出力もカナリア検査の対象にするのに使う。
func (h *Harness) LogWriter() io.Writer {
	return h.buf
}

// Logs はここまでに集めたログを返す。
func (h *Harness) Logs() string {
	return h.buf.String()
}

func (h *Harness) checkCanaries() {
	logs := h.Logs()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.canaries {
		if strings.Contains(logs, c) {
			h.t.Errorf("canary %q leaked into logs:\n%s", c, logs)
		}
	}
}

// Options は Serve に渡す Handler の設定。
type Options struct {
	// Port は Handler の設定のポート。0 なら DefaultPort。
	Port int
	// Token はログイン トークン。空ならカナリアを作って使う。
	// ここに渡した値もカナリアとして登録する。
	Token string
	// Anonymous が true なら、ログインしないまま返す。
	// false（既定）なら Serve の中でログインし、以降の Do にセッション Cookie を付ける。
	Anonymous bool
	// Store はストア バックエンド。nil なら空の FakeStore を使う。
	// 統合テストでは NewGPGStore(...).Backend() を渡す。
	Store store.Backend
	// Backup はバックアップを作る関数（server.Config.Backup）。nil なら、何も書かずに
	// FakeBackupPath を返す偽物を使う。
	Backup server.BackupFunc
}

// FakeBackupPath は Options.Backup を省いたときの偽物が返すパス。
const FakeBackupPath = "/passuitest/backup/passui-backup-20000101-000000.tar.gz"

// Serve は server.New で Handler を組み立てる。既定ではログインまで済ませる。
func (h *Harness) Serve(opts Options) *Server {
	if opts.Port == 0 {
		opts.Port = DefaultPort
	}
	if opts.Token == "" {
		opts.Token = h.Canary("token")
	} else {
		h.AddCanary(opts.Token)
	}
	if opts.Store == nil {
		opts.Store = NewFakeStore()
	}
	if opts.Backup == nil {
		opts.Backup = func(context.Context) (backup.Summary, error) {
			return backup.Summary{Path: FakeBackupPath}, nil
		}
	}
	cfg := server.Config{
		Port:      opts.Port,
		Token:     secret.Of(opts.Token),
		Store:     opts.Store,
		BackupDir: "/passuitest/backup",
		Backup:    opts.Backup,
	}
	s := h.newServer(opts.Port, server.New(cfg, h.Logger))
	s.Token = opts.Token
	if !opts.Anonymous {
		s.SessionCookie = s.Login()
	}
	return s
}

// Wrap は next に共通のミドルウェアだけを掛ける。ミドルウェア自体のテスト
// （panic の recover など、本物のルートでは起こしにくい経路）に使う。
func (h *Harness) Wrap(next http.Handler) *Server {
	cfg := server.Config{Port: DefaultPort}
	return h.newServer(DefaultPort, server.Middleware(cfg, h.Logger, next))
}

func (h *Harness) newServer(port int, handler http.Handler) *Server {
	return &Server{Harness: h, Handler: handler, Host: "passui.localhost:" + strconv.Itoa(port)}
}

// Server は Harness と組み立てた Handler の組。
type Server struct {
	*Harness
	Handler http.Handler
	// Host は NewRequest が既定で付ける Host ヘッダ。
	Host string
	// Token は Handler のログイン トークン（Wrap では空）。
	Token string
	// SessionCookie は Serve でログインしたときのセッション Cookie（Anonymous なら nil）。
	// Do は、リクエストにセッション Cookie がなければこれを付ける。
	SessionCookie *http.Cookie
}

// SessionCookieName は passui のセッション Cookie の名前。
const SessionCookieName = "passui_session"

// Login は Token で新しくログインし、発行されたセッション Cookie を返す。
// Cookie の値はカナリアとして登録する。SessionCookie は変えない。
func (s *Server) Login() *http.Cookie {
	s.t.Helper()
	res := s.Do(s.NewRequest(http.MethodGet, "/login?token="+url.QueryEscape(s.Token), ""))
	for _, c := range res.Cookies() {
		if c.Name == SessionCookieName && c.Value != "" {
			s.AddCanary(c.Value)
			return c
		}
	}
	s.t.Fatalf("login failed: status %d", res.StatusCode)
	return nil
}

// NewRequest は Host ヘッダを Server.Host にしたリクエストを作る。
func (s *Server) NewRequest(method, target string, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	r.Host = s.Host
	return r
}

// Do はリクエストを Handler に通し、レスポンスを返す。
func (s *Server) Do(r *http.Request) *http.Response {
	if s.SessionCookie != nil {
		if _, err := r.Cookie(SessionCookieName); err != nil {
			r.AddCookie(s.SessionCookie)
		}
	}
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, r)
	return rec.Result()
}

// Get は GET リクエストを Handler に通す。
func (s *Server) Get(target string) *http.Response {
	return s.Do(s.NewRequest(http.MethodGet, target, ""))
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
