package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

const sessionCookieName = "passui_session"

func sessionCookie(t *testing.T, res *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	return nil
}

func TestLoginWithCorrectTokenIssuesSessionCookieAndRedirectsToTop(t *testing.T) {
	h := passuitest.New(t)
	token := h.Canary("token")
	srv := h.Serve(passuitest.Options{Token: token, Anonymous: true})

	res := srv.Get("/login?token=" + url.QueryEscape(token))

	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", res.StatusCode)
	}
	if got := res.Header.Get("Location"); got != "/" {
		t.Errorf("Location = %q, want /", got)
	}
	c := sessionCookie(t, res)
	if c == nil {
		t.Fatalf("no %s cookie in %v", sessionCookieName, res.Header["Set-Cookie"])
	}
	h.AddCanary(c.Value)
	if len(c.Value) < 32 || strings.Contains(c.Value, token) {
		t.Errorf("cookie value is not a fresh random session ID")
	}
	if !c.HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", c.SameSite)
	}
	if c.MaxAge != 0 || !c.Expires.IsZero() {
		t.Errorf("cookie is persistent: Max-Age=%d Expires=%v", c.MaxAge, c.Expires)
	}
	// Domain を付けない host-only の Cookie にして、passui.localhost にだけ送られるようにする。
	if c.Domain != "" {
		t.Errorf("Domain = %q, want host-only", c.Domain)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want /", c.Path)
	}

	// 発行された Cookie でトップが開ける。
	req := srv.NewRequest(http.MethodGet, "/", "")
	req.AddCookie(c)
	if res := srv.Do(req); res.StatusCode != http.StatusOK {
		t.Errorf("top with session: status = %d, want 200", res.StatusCode)
	}
}

func TestLoginWithWrongTokenFails(t *testing.T) {
	h := passuitest.New(t)
	token := h.Canary("token")
	srv := h.Serve(passuitest.Options{Token: token, Anonymous: true})

	for _, target := range []string{
		"/login?token=" + url.QueryEscape(token+"x"),
		"/login?token=" + url.QueryEscape(token[:len(token)-1]),
		"/login?token=",
		"/login",
	} {
		res := srv.Get(target)
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", res.StatusCode)
		}
		if c := sessionCookie(t, res); c != nil {
			t.Errorf("session cookie was issued for a wrong token")
		}
	}
}

func TestLoginOnlyOnPassuiLocalhost(t *testing.T) {
	// localhost の Cookie はホストのほかの開発サーバーにも送られるので、発行しない。
	h := passuitest.New(t)
	token := h.Canary("token")
	srv := h.Serve(passuitest.Options{Token: token, Anonymous: true})

	for _, host := range []string{"localhost:3560", "127.0.0.1:3560"} {
		req := srv.NewRequest(http.MethodGet, "/login?token="+url.QueryEscape(token), "")
		req.Host = host
		res := srv.Do(req)
		if c := sessionCookie(t, res); c != nil {
			t.Errorf("Host %s: session cookie was issued", host)
		}
		if res.StatusCode == http.StatusSeeOther && res.Header.Get("Location") == "/" {
			t.Errorf("Host %s: redirected to top as if logged in", host)
		}
	}
}

func TestUnauthenticatedRequestGets401AndGuidance(t *testing.T) {
	h := passuitest.New(t)
	srv := h.Serve(passuitest.Options{Anonymous: true})

	for _, target := range []string{"/", "/no-such-page"} {
		res := srv.Get(target)
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s: status = %d, want 401", target, res.StatusCode)
		}
		if body := readBody(t, res); !strings.Contains(body, "passui login") {
			t.Errorf("GET %s: body lacks guidance: %s", target, body)
		}
	}

	// 偽の Cookie では通らない。
	req := srv.NewRequest(http.MethodGet, "/", "")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: h.Canary("forged")})
	if res := srv.Do(req); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("forged cookie: status = %d, want 401", res.StatusCode)
	}
}

func TestUnauthenticatedHtmxRequestGets401AndRefresh(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{Anonymous: true})

	req := srv.NewRequest(http.MethodGet, "/", "")
	req.Header.Set("HX-Request", "true")
	res := srv.Do(req)

	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", res.StatusCode)
	}
	// htmx はこのヘッダでページ全体を読み直し、案内ページに移る。
	if got := res.Header.Get("HX-Refresh"); got != "true" {
		t.Errorf("HX-Refresh = %q, want true", got)
	}
}

func TestStaticFilesNeedNoLogin(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{Anonymous: true})

	if res := srv.Get("/static/htmx.min.js"); res.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", res.StatusCode)
	}
}

func TestTopHasLogoutButton(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{})

	body := readBody(t, srv.Get("/"))

	if !strings.Contains(body, `action="/logout"`) || !strings.Contains(body, "ログアウト") {
		t.Errorf("top lacks a logout button: %s", body)
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{})
	cookie := srv.SessionCookie

	req := srv.NewRequest(http.MethodPost, "/logout", "")
	req.Header.Set("Origin", "http://"+srv.Host)
	res := srv.Do(req)

	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/" {
		t.Errorf("status = %d Location = %q, want 303 /", res.StatusCode, res.Header.Get("Location"))
	}
	if c := sessionCookie(t, res); c == nil || c.MaxAge >= 0 {
		t.Errorf("logout does not expire the cookie: %v", res.Header["Set-Cookie"])
	}

	// 同じ Cookie はもう使えない。
	req = srv.NewRequest(http.MethodGet, "/", "")
	req.AddCookie(cookie)
	if res := srv.Do(req); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("after logout: status = %d, want 401", res.StatusCode)
	}
}

func TestLogoutDoesNotAffectOtherSessions(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{})
	other := srv.Login()

	req := srv.NewRequest(http.MethodPost, "/logout", "")
	req.Header.Set("Origin", "http://"+srv.Host)
	srv.Do(req)

	req = srv.NewRequest(http.MethodGet, "/", "")
	req.AddCookie(other)
	if res := srv.Do(req); res.StatusCode != http.StatusOK {
		t.Errorf("other session: status = %d, want 200", res.StatusCode)
	}
}

func TestStateChangingRequestFromOtherOriginIsRejected(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{})

	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"foreign Origin", map[string]string{"Origin": "http://evil.example"}},
		{"other port", map[string]string{"Origin": "http://passui.localhost:9999"}},
		{"https scheme", map[string]string{"Origin": "https://" + srv.Host}},
		{"null Origin", map[string]string{"Origin": "null"}},
		{"foreign Origin wins over Sec-Fetch-Site", map[string]string{"Origin": "http://evil.example", "Sec-Fetch-Site": "same-origin"}},
		{"cross-site without Origin", map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{"same-site without Origin", map[string]string{"Sec-Fetch-Site": "same-site"}},
	} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			req := srv.NewRequest(method, "/logout", "")
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			if res := srv.Do(req); res.StatusCode != http.StatusForbidden {
				t.Errorf("%s %s: status = %d, want 403", tc.name, method, res.StatusCode)
			}
		}
	}

	// 拒否されたのでセッションは生きている。
	if res := srv.Get("/"); res.StatusCode != http.StatusOK {
		t.Errorf("session was invalidated by a rejected request: status = %d", res.StatusCode)
	}
}

func TestStateChangingRequestFromSameOriginIsAccepted(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"same Origin", map[string]string{"Origin": "http://passui.localhost:3560"}},
		{"same-origin without Origin", map[string]string{"Sec-Fetch-Site": "same-origin"}},
		{"user-initiated without Origin", map[string]string{"Sec-Fetch-Site": "none"}},
	} {
		srv := passuitest.New(t).Serve(passuitest.Options{})
		req := srv.NewRequest(http.MethodPost, "/logout", "")
		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}
		if res := srv.Do(req); res.StatusCode != http.StatusSeeOther {
			t.Errorf("%s: status = %d, want 303", tc.name, res.StatusCode)
		}
	}
}

func TestSafeMethodsSkipOriginCheck(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{})

	req := srv.NewRequest(http.MethodGet, "/", "")
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if res := srv.Do(req); res.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", res.StatusCode)
	}
}

func TestTokenAndSessionCookieNeverAppearInLogs(t *testing.T) {
	// Harness は Token と Login で得た Cookie をカナリアとして登録する。
	// 成功、失敗、ログアウトのどの経路でもログに出ないことを t.Cleanup で確かめる。
	h := passuitest.New(t)
	token := h.Canary("token")
	srv := h.Serve(passuitest.Options{Token: token})

	srv.Get("/login?token=" + url.QueryEscape(token))
	srv.Get("/login?token=" + url.QueryEscape(token) + "x")
	srv.Get("/")
	req := srv.NewRequest(http.MethodPost, "/logout", "")
	req.Header.Set("Origin", "http://evil.example")
	srv.Do(req)
	req = srv.NewRequest(http.MethodPost, "/logout", "")
	req.Header.Set("Origin", "http://"+srv.Host)
	srv.Do(req)
	srv.Get("/")

	if !strings.Contains(h.Logs(), "path=/login") {
		t.Errorf("logs lack the login access:\n%s", h.Logs())
	}
}
