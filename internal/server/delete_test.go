package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

// postDelete は削除の確認画面の「削除する」を htmx から送ったときのリクエストを通す。
// form は送るフォームの値（確認を経たなら confirm=yes が入る）。
func postDelete(srv *passuitest.Server, name string, form url.Values) *http.Response {
	req := srv.NewRequest(http.MethodPost, "/delete/"+name, form.Encode())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	return srv.Do(req)
}

func TestDeleteAsksForConfirmationBeforeDeleting(t *testing.T) {
	fake := passuitest.NewFakeStore("email/work")
	srv := passuitest.New(t).Serve(passuitest.Options{Store: fake})

	// 詳細の見出しの「削除」は確認画面を開くだけ。
	res := srv.Get("/delete/email/work")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	body := readBody(t, res)
	for _, want := range []string{`hx-post="/delete/email/work"`, `name="confirm" value="yes"`, "email/work"} {
		if !strings.Contains(body, want) {
			t.Errorf("confirmation does not contain %s:\n%s", want, body)
		}
	}
	if _, ok := fake.Content("email/work"); !ok {
		t.Fatal("entry was deleted by opening the confirmation")
	}

	// 確認を経ない POST では消さない。
	res = postDelete(srv, "email/work", url.Values{})

	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status without confirmation = %d, want 400", res.StatusCode)
	}
	if _, ok := fake.Content("email/work"); !ok {
		t.Fatal("entry was deleted without confirmation")
	}
}

func TestDeleteAfterConfirmationRemovesEntryAndReloadsTree(t *testing.T) {
	h := passuitest.New(t)
	fake := passuitest.NewFakeStore("email/work", "email/personal")
	srv := h.Serve(passuitest.Options{Store: fake})

	res := postDelete(srv, "email/work", url.Values{"confirm": {"yes"}})

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, readBody(t, res))
	}
	if got := res.Header.Get("HX-Trigger"); got != "tree-changed" {
		t.Errorf("HX-Trigger = %q, want tree-changed", got)
	}
	if body := readBody(t, res); !strings.Contains(body, "削除しました") {
		t.Errorf("body does not tell the entry was deleted:\n%s", body)
	}
	if _, ok := fake.Content("email/work"); ok {
		t.Error("entry still exists")
	}
	if tree := readBody(t, srv.Get("/tree")); strings.Contains(tree, `data-entry="email/work"`) {
		t.Errorf("tree still contains the deleted entry:\n%s", tree)
	}
	if !strings.Contains(h.Logs(), "msg=operation op=delete entry=email/work") {
		t.Errorf("operation log is missing:\n%s", h.Logs())
	}
}

func TestDetailAndLockedNoticeHaveDeleteButton(t *testing.T) {
	fake := passuitest.NewFakeStore("email/work")
	srv := passuitest.New(t).Serve(passuitest.Options{Store: fake})
	want := `hx-get="/delete/email/work"`

	if body := readBody(t, srv.Get("/entry/email/work")); !strings.Contains(body, want) {
		t.Errorf("detail does not contain %s:\n%s", want, body)
	}

	fake.SetLocked(true)
	if body := readBody(t, srv.Get("/entry/email/work")); !strings.Contains(body, want) {
		t.Errorf("locked notice does not contain %s:\n%s", want, body)
	}
}

func TestDeleteMissingEntryReportsNotFoundAndReloadsTree(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{Store: passuitest.NewFakeStore("bank")})

	res := postDelete(srv, "email/work", url.Values{"confirm": {"yes"}})

	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.StatusCode)
	}
	if got := res.Header.Get("HX-Trigger"); got != "tree-changed" {
		t.Errorf("HX-Trigger = %q, want tree-changed", got)
	}
	if body := readBody(t, res); !strings.Contains(body, "見つかりません") {
		t.Errorf("body does not contain 見つかりません:\n%s", body)
	}
}
