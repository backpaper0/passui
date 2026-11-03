package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

// postCreate は新規作成フォーム（手で入力する）を htmx から送ったときのリクエストを通す。
func postCreate(srv *passuitest.Server, path, content string) *http.Response {
	form := url.Values{"path": {path}, "content": {content}}
	req := srv.NewRequest(http.MethodPost, "/new", form.Encode())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	return srv.Do(req)
}

func TestLeftPaneHasNewEntryButton(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{})

	body := readBody(t, srv.Get("/"))

	for _, want := range []string{"＋ 新規エントリ", `hx-get="/new"`, `hx-include="#selected-entry"`} {
		if !strings.Contains(body, want) {
			t.Errorf("top page does not contain %s:\n%s", want, body)
		}
	}
}

func TestNewEntryFormDefaultsToFolderOfSelectedEntry(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{Store: passuitest.NewFakeStore("email/work", "dev/github/token", "bank")})

	for _, tc := range []struct{ selected, folder string }{
		{"email/work", "email/"},
		{"dev/github/token", "dev/github/"},
		{"bank", ""},
		{"", ""},
	} {
		body := readBody(t, srv.Get("/new?selected="+url.QueryEscape(tc.selected)))
		want := `name="path" value="` + tc.folder + `"`
		if !strings.Contains(body, want) {
			t.Errorf("selected %q: form does not contain %s:\n%s", tc.selected, want, body)
		}
	}
}

func TestCreateEntryReloadsTreeAndShowsCreatedEntry(t *testing.T) {
	h := passuitest.New(t)
	fake := passuitest.NewFakeStore("email/personal")
	srv := h.Serve(passuitest.Options{Store: fake})

	res := postCreate(srv, "email/work", h.Canary("password")+"\r\nuser: alice")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("HX-Trigger"); got != "tree-changed" {
		t.Errorf("HX-Trigger = %q, want tree-changed", got)
	}
	body := readBody(t, res)
	if !strings.Contains(body, `data-entry="email/work"`) {
		t.Errorf("created entry is not shown:\n%s", body)
	}
	// 作ったエントリは詳細（#29）で出す。1 行目は伏せ字の初期状態。
	if !strings.Contains(body, `type="password" value="CANARYpassword`) {
		t.Errorf("password is not masked after create:\n%s", body)
	}
	// ツリーはこのイベントで読み直す。
	if top := readBody(t, srv.Get("/")); !strings.Contains(top, "tree-changed from:body") {
		t.Errorf("tree does not reload on tree-changed:\n%s", top)
	}
	if tree := readBody(t, srv.Get("/tree")); !strings.Contains(tree, `data-entry="email/work"`) {
		t.Errorf("tree does not contain the created entry:\n%s", tree)
	}
	// textarea の CRLF は LF にし、末尾に改行を足す。
	if got, _ := fake.Content("email/work"); !strings.HasSuffix(got, "\nuser: alice\n") || strings.Contains(got, "\r") {
		t.Errorf("stored content = %q", got)
	}
}

func TestCreateEntryLogsOperationButNotFormValues(t *testing.T) {
	h := passuitest.New(t)
	srv := h.Serve(passuitest.Options{Store: passuitest.NewFakeStore()})
	content := h.Canary("password") + "\n" + h.Canary("username")

	res := postCreate(srv, "email/work", content)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	logs := h.Logs()
	if !strings.Contains(logs, "level=INFO msg=operation op=create entry=email/work") {
		t.Errorf("operation is not logged:\n%s", logs)
	}
	// 本文のカナリアがログに出ていないことは Harness が確かめる。
}

func TestCreateEntryFailureKeepsInputWithoutLoggingIt(t *testing.T) {
	h := passuitest.New(t)
	fake := passuitest.NewFakeStore("email/work")
	srv := h.Serve(passuitest.Options{Store: fake})
	content := h.Canary("password")

	res := postCreate(srv, "email/work", content)

	if res.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", res.StatusCode)
	}
	if res.Header.Get("HX-Trigger") != "" {
		t.Errorf("tree is reloaded after a failed create")
	}
	body := readBody(t, res)
	for _, want := range []string{"同じ名前のエントリが既にあります", `value="email/work"`, content} {
		if !strings.Contains(body, want) {
			t.Errorf("form does not contain %q:\n%s", want, body)
		}
	}
	if got, _ := fake.Content("email/work"); got != "ciphertext of email/work" {
		t.Errorf("existing entry changed: %q", got)
	}
}

func TestCreateEntryRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		name, path, content, message string
	}{
		{"empty content", "a", "", "本文を入力してください"},
		{"empty path", "", "x", "パスが正しくありません"},
		{"parent directory", "../outside", "x", "パスが正しくありません"},
		{"absolute path", "/abs", "x", "パスが正しくありません"},
		{"double slash", "a//b", "x", "パスが正しくありません"},
		{"hidden element", ".git/x", "x", "パスが正しくありません"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := passuitest.NewFakeStore()
			srv := passuitest.New(t).Serve(passuitest.Options{Store: fake})

			res := postCreate(srv, tc.path, tc.content)

			if res.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", res.StatusCode)
			}
			if body := readBody(t, res); !strings.Contains(body, tc.message) {
				t.Errorf("body does not contain %q:\n%s", tc.message, body)
			}
			if entries, _ := fake.Entries(t.Context()); len(entries) != 0 {
				t.Errorf("store changed: %v", entries)
			}
		})
	}
}
