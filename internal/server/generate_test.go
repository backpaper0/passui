package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

func TestNewEntryFormOffersGeneration(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{})

	body := readBody(t, srv.Get("/new"))

	for _, want := range []string{
		`name="mode" value="manual" checked`,
		`name="mode" value="generate"`,
		`name="length"`,
		`name="no-symbols"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("form does not contain %s:\n%s", want, body)
		}
	}
}

func TestGenerateEntryLogsOperationAndShowsEntry(t *testing.T) {
	h := passuitest.New(t)
	fake := passuitest.NewFakeStore()
	srv := h.Serve(passuitest.Options{Store: fake})

	res := postGenerate(srv, "email/work", "30", true)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	content, ok := fake.Content("email/work")
	if !ok {
		t.Fatal("entry was not created")
	}
	password := strings.TrimSuffix(content, "\n")
	h.AddCanary(password)
	if len(password) != 30 {
		t.Errorf("password length = %d, want 30", len(password))
	}
	if got := res.Header.Get("HX-Trigger"); got != "tree-changed" {
		t.Errorf("HX-Trigger = %q, want tree-changed", got)
	}
	if body := readBody(t, res); !strings.Contains(body, `data-entry="email/work"`) {
		t.Errorf("response is not the detail of the entry:\n%s", body)
	}
	if logs := h.Logs(); !strings.Contains(logs, "op=generate entry=email/work") {
		t.Errorf("logs do not contain the operation:\n%s", logs)
	}
}

func TestGenerateEntryRejectsBadLengthAndKeepsInput(t *testing.T) {
	for _, length := range []string{"0", "-1", "abc", "1025"} {
		fake := passuitest.NewFakeStore()
		srv := passuitest.New(t).Serve(passuitest.Options{Store: fake})

		res := postGenerate(srv, "email/work", length, true)

		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("length %q: status = %d, want 400", length, res.StatusCode)
		}
		body := readBody(t, res)
		for _, want := range []string{"長さは", `value="email/work"`, `name="mode" value="generate" checked`, `name="no-symbols" checked`} {
			if !strings.Contains(body, want) {
				t.Errorf("length %q: form does not contain %s:\n%s", length, want, body)
			}
		}
		if _, ok := fake.Content("email/work"); ok {
			t.Errorf("length %q: entry was created", length)
		}
	}
}

func TestEntryDetailHasRegenerateButton(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{Store: passuitest.NewFakeStore("dev/my token")})

	_, body := openEntry(t, srv, "dev/my%20token")

	for _, want := range []string{
		`hx-post="/regenerate/dev/my%20token"`,
		"パスワードを再生成",
		"hx-confirm=",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail does not contain %s:\n%s", want, body)
		}
	}
}

func TestRegenerateKeepsRestAndShowsEntry(t *testing.T) {
	h := passuitest.New(t)
	fake := passuitest.NewFakeStore()
	fake.Put("email/work", h.Canary("old")+"\nuser: alice\n")
	srv := h.Serve(passuitest.Options{Store: fake})

	res := postRegenerate(srv, "email/work")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	content, _ := fake.Content("email/work")
	first, rest, _ := strings.Cut(content, "\n")
	h.AddCanary(first)
	if rest != "user: alice\n" {
		t.Errorf("rest = %q", rest)
	}
	if got := res.Header.Get("HX-Trigger"); got != "tree-changed" {
		t.Errorf("HX-Trigger = %q, want tree-changed", got)
	}
	body := readBody(t, res)
	if !strings.Contains(body, "パスワードを再生成しました") {
		t.Errorf("response has no notice:\n%s", body)
	}
	if logs := h.Logs(); !strings.Contains(logs, "op=regenerate entry=email/work") {
		t.Errorf("logs do not contain the operation:\n%s", logs)
	}
}

func TestRegenerateWhileLockedRetriesByOpeningEntry(t *testing.T) {
	fake := passuitest.NewFakeStore("email/work")
	fake.SetLocked(true)
	srv := passuitest.New(t).Serve(passuitest.Options{Store: fake})

	res := postRegenerate(srv, "email/work")

	if res.StatusCode != http.StatusLocked {
		t.Errorf("status = %d, want 423", res.StatusCode)
	}
	// 再試行やフォーカスの確認で勝手に再生成しないよう、閲覧の URL を開き直す。
	body := readBody(t, res)
	if !strings.Contains(body, `hx-get="/entry/email/work"`) || strings.Contains(body, `hx-get="/regenerate/`) {
		t.Errorf("retry does not open the entry:\n%s", body)
	}
	if got, _ := fake.Content("email/work"); got != "ciphertext of email/work" {
		t.Errorf("entry changed: %q", got)
	}
}

// postGenerate は新規作成フォームで「生成する」を選んで送ったときのリクエストを通す。
// length が空なら pass の既定の長さになる。
func postGenerate(srv *passuitest.Server, path, length string, noSymbols bool) *http.Response {
	form := url.Values{"path": {path}, "mode": {"generate"}, "length": {length}}
	if noSymbols {
		form.Set("no-symbols", "on")
	}
	req := srv.NewRequest(http.MethodPost, "/new", form.Encode())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	return srv.Do(req)
}

// postRegenerate は詳細の「パスワードを再生成」を押したときのリクエストを通す。
func postRegenerate(srv *passuitest.Server, name string) *http.Response {
	req := srv.NewRequest(http.MethodPost, "/regenerate/"+name, "")
	req.Header.Set("HX-Request", "true")
	return srv.Do(req)
}
