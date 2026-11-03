package server_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

// search は左ペインの検索欄と同じリクエスト（htmx の GET /tree?q=...）を送る。
func search(t *testing.T, srv *passuitest.Server, q string) (int, string) {
	t.Helper()
	req := srv.NewRequest(http.MethodGet, "/tree?q="+url.QueryEscape(q), "")
	req.Header.Set("HX-Request", "true")
	res := srv.Do(req)
	return res.StatusCode, readBody(t, res)
}

func TestSearchListsMatchingPathsFlatWithHighlight(t *testing.T) {
	fake := passuitest.NewFakeStore("email/work", "email/personal", "bank", "dev/github/token")
	srv := passuitest.New(t).Serve(passuitest.Options{Store: fake})

	status, body := search(t, srv, "o")

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	for _, want := range []string{
		`data-entry="email/work"`,
		`data-entry="email/personal"`,
		`data-entry="dev/github/token"`,
		// パス全体を出し、一致した部分（すべての出現）を強調する。
		`email/w<mark>o</mark>rk`,
		`email/pers<mark>o</mark>nal`,
		`dev/github/t<mark>o</mark>ken`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, `data-entry="bank"`) {
		t.Errorf("non-matching entry is listed:\n%s", body)
	}
	// 平らに並べる（フォルダを出さない）。並びはパスの名前順。
	if strings.Contains(body, "data-folder") {
		t.Errorf("search results contain folders:\n%s", body)
	}
	assertOrder(t, body, `data-entry="dev/github/token"`, `data-entry="email/personal"`, `data-entry="email/work"`)
}

func TestSearchIgnoresCaseAndKeepsOriginalText(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{Store: passuitest.NewFakeStore("GitHub/Token", "gitlab")})

	_, body := search(t, srv, "GIT")

	for _, want := range []string{`<mark>Git</mark>Hub/Token`, `<mark>git</mark>lab`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %s:\n%s", want, body)
		}
	}
}

func TestSearchWithoutMatchShowsMessage(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{Store: passuitest.NewFakeStore("bank")})

	status, body := search(t, srv, "zzz")

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !strings.Contains(body, "一致するエントリがありません") || strings.Contains(body, "data-entry") {
		t.Errorf("no-match message is not shown:\n%s", body)
	}
}

func TestSearchEscapesEntryNames(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{Store: passuitest.NewFakeStore(`a<b>/x"y`)})

	_, body := search(t, srv, "<b")

	if strings.Contains(body, "<b>") {
		t.Errorf("entry name is not escaped:\n%s", body)
	}
	if !strings.Contains(body, `a<mark>&lt;b</mark>&gt;/x&#34;y`) {
		t.Errorf("escaped highlight is missing:\n%s", body)
	}
}

func TestSearchQueryDoesNotAppearInLogs(t *testing.T) {
	h := passuitest.New(t)
	// カナリアの検索語。ヘルパが t.Cleanup でログに出ていないことを確かめる。
	q := h.Canary("query")
	fake := passuitest.NewFakeStore("web/" + q + "/login")
	srv := h.Serve(passuitest.Options{Store: fake})

	// 一致する場合、一致しない場合、ストアが失敗する場合のどれでもログに出さない。
	if _, body := search(t, srv, q); !strings.Contains(body, "<mark>"+q+"</mark>") {
		t.Fatalf("search does not match:\n%s", body)
	}
	if _, body := search(t, srv, q+"-nomatch"); !strings.Contains(body, "一致するエントリがありません") {
		t.Errorf("no-match message is not shown:\n%s", body)
	}
	fake.SetErr(errors.New("disk failure"))
	if status, body := search(t, srv, q); status != http.StatusInternalServerError || !strings.Contains(body, "エラー ID") {
		t.Errorf("failure: status = %d, body:\n%s", status, body)
	}

	// アクセスログ自体は出ている（検査が空振りしていない）。
	if logs := h.Logs(); !strings.Contains(logs, "path=/tree") {
		t.Errorf("access log for /tree is missing:\n%s", logs)
	}
}

func TestEmptySearchReturnsTree(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{Store: passuitest.NewFakeStore("email/work", "bank")})

	for _, q := range []string{"", "   "} {
		status, body := search(t, srv, q)
		if status != http.StatusOK {
			t.Fatalf("q=%q: status = %d, want 200", q, status)
		}
		if !strings.Contains(body, `data-folder="email"`) || strings.Contains(body, "<mark>") {
			t.Errorf("q=%q: tree is not returned:\n%s", q, body)
		}
	}
}

func TestTopPageHasSearchBoxDrivingTree(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{Store: passuitest.NewFakeStore("a")})

	body := readBody(t, srv.Get("/"))

	for _, want := range []string{
		`id="search"`,
		`type="search"`,
		`name="q"`,
		// 検索欄はツリーを差し替える。
		`hx-target="#tree"`,
		// ツリーの読み直し（タブに戻ったときなど）でも検索語を保つ。
		`hx-include="#search"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %s:\n%s", want, body)
		}
	}
}
