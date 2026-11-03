package server

import (
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/backpaper0/passui/internal/secret"
)

// sessionCookieName はセッション Cookie の名前。
const sessionCookieName = "passui_session"

// sessions はメモリ上だけにあるセッションの集合。アイドル タイムアウトはなく、
// passui の再起動かログアウトで消える。
type sessions struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

func newSessions() *sessions {
	return &sessions{ids: map[string]struct{}{}}
}

func (s *sessions) create() secret.Value {
	id := secret.New()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids[id.Reveal()] = struct{}{}
	return id
}

func (s *sessions) valid(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.ids[id]
	return ok
}

func (s *sessions) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.ids, id)
}

// sessionCookie はセッション Cookie を作る。発行（login）と削除（logout、maxAge は -1）で
// 属性を揃える。SameSite=Strict なので、他サイトからのリクエストには付かない。
func sessionCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

// isHTMX は htmx が送ったリクエストかを返す。
func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// loginHost は Cookie を発行してよい Host。localhost や 127.0.0.1 の Cookie は、
// ホストのほかの開発サーバーにも送られてしまうので、passui.localhost に限る。
func loginHost(port int) string {
	return "passui.localhost:" + strconv.Itoa(port)
}

// login は GET /login?token=… を受ける。トークンが一致すればセッション Cookie を発行し、
// 303 で / に移す（トークンをアドレスバーに残さない）。
func (s *handler) login(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Host, loginHost(s.cfg.Port)) {
		s.logger.WarnContext(r.Context(), "login rejected", "reason", "host", "host", r.Host)
		s.render(w, r, http.StatusUnauthorized, "login_required.html", loginRequiredData{WrongHost: true, LoginHost: loginHost(s.cfg.Port)})
		return
	}
	if !s.cfg.Token.Equal(r.URL.Query().Get("token")) {
		s.logger.WarnContext(r.Context(), "login rejected", "reason", "token")
		s.render(w, r, http.StatusUnauthorized, "login_required.html", loginRequiredData{})
		return
	}
	id := s.sessions.create()
	http.SetCookie(w, sessionCookie(id.Reveal(), 0))
	s.logger.InfoContext(r.Context(), "logged in")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// logout はセッションを破棄し、Cookie を消して / に移す。解錠の状態には触れない。
func (s *handler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.delete(c.Value)
	}
	http.SetCookie(w, sessionCookie("", -1))
	s.logger.InfoContext(r.Context(), "logged out")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// requireSession は、ログインなしで通すルート（/login と /static/）以外で
// セッションを確かめる。なければ 401 と案内ページを返す。htmx のリクエストには
// HX-Refresh を付け、ページ全体を読み直させて案内ページに移す。
func (s *handler) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(sessionCookieName); err == nil && s.sessions.valid(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		if isHTMX(r) {
			w.Header().Set("HX-Refresh", "true")
		}
		s.render(w, r, http.StatusUnauthorized, "login_required.html", loginRequiredData{})
	})
}

type loginRequiredData struct {
	// WrongHost は passui.localhost 以外の Host でログインしようとしたことを表す。
	WrongHost bool
	LoginHost string
}

// isSafeMethod は状態を変えないメソッドかどうかを返す。
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

// checkOrigin は状態を変えるメソッドで、他オリジンからのリクエストを 403 で拒否する。
// Origin があれば http://<Host> と一致するかを見る（Host は checkHost で検証済み）。
// Origin がなければ Sec-Fetch-Site を見て、same-origin と none だけを通す。
//
// どちらもなければ通す。今のブラウザは、他オリジンのページから送らせた状態変更の
// リクエストに Origin か Sec-Fetch-Site の少なくとも一方を必ず付けるので、どちらもない
// リクエストはブラウザ以外（curl やスクリプト）か古いブラウザからのものである。前者は
// CSRF の経路にならず、後者に対しても、この後のセッションの確認（Cookie は
// SameSite=Strict で他サイトからは付かない）が守る。拒否すると、ヘッダを付けない
// クライアントからの正当な操作まで使えなくなる。
func checkOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) || sameOrigin(r) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "他のオリジンからのリクエストは受け付けません", http.StatusForbidden)
	})
}

func sameOrigin(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		return strings.EqualFold(origin, "http://"+r.Host)
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	}
	return false
}
