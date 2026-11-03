package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

// コピー ボタン（data-copy-secret）は、押すと 45 秒後にクリップボードのクリアを試みる。
// 値そのものはボタンの属性に持たせず、同じ行（.password-row / .field）の要素から読む。
func TestDetailHasCopyButtonsForPasswordAndEachValue(t *testing.T) {
	h := passuitest.New(t)
	password := h.Canary("password")
	fake := passuitest.NewFakeStore()
	fake.Put("email/work", password+"\nuser: alice\nurl: https://example.com/login\nfree text line\n")
	srv := h.Serve(passuitest.Options{Store: fake})

	res, body := openEntry(t, srv, "email/work")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, body)
	}
	rows := map[string]string{
		"password": between(body, `<div class="password-row">`, `</div>`),
		"user":     between(body, `<dt>user</dt>`, `</div>`),
		"url":      between(body, `<dt>url</dt>`, `</div>`),
	}
	for name, row := range rows {
		if !strings.Contains(row, `data-copy-secret`) {
			t.Errorf("%s row has no copy button: %q", name, row)
		}
	}
	// key: value でない行にはコピー ボタンを付けない。
	if row := between(body, `free text line`, `</div>`); strings.Contains(row, "data-copy-secret") {
		t.Errorf("plain line has a copy button: %q", row)
	}
	// 平文がボタンの属性に複製されていない（値は DOM の 1 か所だけ）。
	if n := strings.Count(body, password); n != 1 {
		t.Errorf("password appears %d times, want 1:\n%s", n, body)
	}
}

// コピーとクリアの処理（clipboard.js）をトップ ページが読み込み、配信される。
func TestTopPageLoadsClipboardScript(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{})

	if body := readBody(t, srv.Get("/")); !strings.Contains(body, `src="/static/clipboard.js"`) {
		t.Errorf("top page does not load clipboard.js:\n%s", body)
	}
	res := srv.Get("/static/clipboard.js")
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "data-copy-secret") {
		t.Errorf("clipboard.js is not served: status = %d", res.StatusCode)
	}
}

// between は s の中で start の後から end の手前までを返す（なければ空）。
func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	rest := s[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		return ""
	}
	return rest[:j]
}
