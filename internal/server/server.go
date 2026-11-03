// Package server は passui の HTTP シームである。設定（ストア バックエンドを含む）と
// ロガーから http.Handler を組み立てる。
package server

import (
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/backpaper0/passui/internal/secret"
	"github.com/backpaper0/passui/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

var templates = template.Must(template.New("").Funcs(template.FuncMap{
	"entryURL":      entryURL,
	"editURL":       editURL,
	"regenerateURL": regenerateURL,
	"deleteURL":     deleteURL,
}).ParseFS(templateFS, "templates/*.html"))

// Config は Handler の設定である。
type Config struct {
	// Port は待ち受けるポート。許可する Host ヘッダの組み立てに使う。
	Port int
	// Token はログイン トークン。serve が起動ごとに作る。空ならログインできない。
	Token secret.Value
	// Store はストア バックエンド。
	Store store.Backend
}

// New は passui の http.Handler を返す。すべてのルートに共通のミドルウェアが掛かり、
// /login と /static/ 以外のルートにはログインが要る。
func New(cfg Config, logger *slog.Logger) http.Handler {
	s := &handler{cfg: cfg, logger: logger, sessions: newSessions()}
	mux := http.NewServeMux()
	s.routes(mux)
	return Middleware(cfg, logger, s.requireSession(mux))
}

type handler struct {
	cfg      Config
	logger   *slog.Logger
	sessions *sessions
}

// routes にルートを登録する。
func (s *handler) routes(mux *http.ServeMux) {
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /{$}", s.top)
	mux.HandleFunc("GET /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /tree", s.tree)
	mux.HandleFunc("GET /entry/{name...}", s.entry)
	mux.HandleFunc("GET /lock-status", s.lockStatus)
	mux.HandleFunc("POST /lock", s.lock)
	mux.HandleFunc("GET /new", s.newEntryForm)
	mux.HandleFunc("POST /new", s.createEntry)
	mux.HandleFunc("GET /edit/{name...}", s.editForm)
	mux.HandleFunc("POST /edit/{name...}", s.saveEdit)
	mux.HandleFunc("POST /regenerate/{name...}", s.regenerateEntry)
	mux.HandleFunc("GET /delete/{name...}", s.confirmDelete)
	mux.HandleFunc("POST /delete/{name...}", s.deleteEntry)
}

type indexData struct {
	Tree  treeData
	Badge badgeData
}

func (s *handler) top(w http.ResponseWriter, r *http.Request) {
	// ツリーの取得に失敗しても、画面の枠は出す（左ペインにエラー ID が出る）。
	tree, _ := s.loadTree(r)
	s.render(w, r, http.StatusOK, "index.html", indexData{Tree: tree, Badge: s.loadBadge(r)})
}

// render はテンプレートを status で描画する。途中で失敗したら応答は差し替えられないので、
// ログにだけ出す。
func (s *handler) render(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := templates.ExecuteTemplate(w, name, data); err != nil {
		s.logger.ErrorContext(r.Context(), "render template", "template", name, "err", err)
	}
}
