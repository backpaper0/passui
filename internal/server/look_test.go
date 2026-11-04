package server_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

// トップ ページとログインの案内ページは、同じ CSS と favicon を読み込む（#56）。
func TestPagesLoadStylesheetAndFavicon(t *testing.T) {
	h := passuitest.New(t)
	srv := h.Serve(passuitest.Options{})
	anon := h.Serve(passuitest.Options{Anonymous: true})

	pages := map[string]string{
		"top":            readBody(t, srv.Get("/")),
		"login required": readBody(t, anon.Get("/")),
	}
	for name, body := range pages {
		for _, want := range []string{`href="/static/passui.css"`, `href="/static/favicon.svg"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s page does not contain %s:\n%s", name, want, body)
			}
		}
	}
}

// favicon はログインなしで配信される（案内ページでも出す）。
func TestFaviconIsServedWithoutLogin(t *testing.T) {
	anon := passuitest.New(t).Serve(passuitest.Options{Anonymous: true})

	res := anon.Get("/static/favicon.svg")
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Content-Type"), "image/svg+xml") || !strings.Contains(body, "<svg") {
		t.Errorf("favicon: status = %d, Content-Type = %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
}

// 表示とコピーはアイコンだけのボタンなので、どれにも aria-label が要る。
func TestIconButtonsHaveLabels(t *testing.T) {
	h := passuitest.New(t)
	fake := passuitest.NewFakeStore()
	fake.Put("email/work", "secret\nuser: alice\nurl: https://example.com/login\n")
	srv := h.Serve(passuitest.Options{Store: fake})

	_, body := openEntry(t, srv, "email/work")

	buttons := regexp.MustCompile(`<button[^>]*class="icon-button[^>]*>`).FindAllString(body, -1)
	if len(buttons) != 4 { // 表示、パスワードのコピー、user と url のコピー
		t.Fatalf("icon buttons = %d, want 4:\n%s", len(buttons), body)
	}
	for _, want := range []string{`aria-label="表示"`, `aria-label="パスワードをコピー"`, `aria-label="user をコピー"`, `aria-label="url をコピー"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %s:\n%s", want, body)
		}
	}
	for _, b := range buttons {
		if !strings.Contains(b, `aria-label="`) {
			t.Errorf("icon button without aria-label: %s", b)
		}
	}
}
