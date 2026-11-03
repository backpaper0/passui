// Package server は passui の HTTP シームである。設定とロガー（以降のチケットでは
// ストア バックエンドも）から http.Handler を組み立てる。
package server

import (
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// Config は Handler の設定である。
type Config struct {
	// Port は待ち受けるポート。許可する Host ヘッダの組み立てに使う。
	Port int
}

// New は passui の http.Handler を返す。すべてのルートに共通のミドルウェアが掛かる。
func New(cfg Config, logger *slog.Logger) http.Handler {
	s := &handler{cfg: cfg, logger: logger}
	mux := http.NewServeMux()
	s.routes(mux)
	return Middleware(cfg, logger, mux)
}

type handler struct {
	cfg    Config
	logger *slog.Logger
}

// routes にルートを登録する。以降のチケットはここに足す。
func (s *handler) routes(mux *http.ServeMux) {
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /{$}", s.top)
}

func (s *handler) top(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "index.html", nil)
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
