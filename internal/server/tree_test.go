package server_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

func TestTopPageShowsStoreAsTree(t *testing.T) {
	fake := passuitest.NewFakeStore("email/work", "email/personal", "bank", "dev/github/token")
	srv := passuitest.New(t).Serve(passuitest.Options{Store: fake})

	body := readBody(t, srv.Get("/"))

	for _, want := range []string{
		`id="left-pane"`,
		`id="right-pane"`,
		`data-folder="email"`,
		`data-entry="email/work"`,
		`data-entry="email/personal"`,
		`data-entry="bank"`,
		`data-folder="dev/github"`,
		`data-entry="dev/github/token"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %s:\n%s", want, body)
		}
	}
	// フォルダが先、エントリが後。それぞれ名前順。
	assertOrder(t, body, `data-folder="dev"`, `data-folder="email"`, `data-entry="bank"`)
	assertOrder(t, body, `data-entry="email/personal"`, `data-entry="email/work"`)
}

func TestTreeFragmentReflectsCurrentStore(t *testing.T) {
	fake := passuitest.NewFakeStore("old")
	srv := passuitest.New(t).Serve(passuitest.Options{Store: fake})

	fake.Put("new", "ciphertext")
	fake.Remove("old")
	req := srv.NewRequest(http.MethodGet, "/tree", "")
	req.Header.Set("HX-Request", "true")
	res := srv.Do(req)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	body := readBody(t, res)
	if !strings.Contains(body, `data-entry="new"`) || strings.Contains(body, `data-entry="old"`) {
		t.Errorf("tree is not reloaded:\n%s", body)
	}
	if strings.Contains(body, "<html") {
		t.Errorf("tree fragment contains a whole page:\n%s", body)
	}
}

func TestTreeReloadsWhenTabBecomesVisible(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{Store: passuitest.NewFakeStore("a")})

	body := readBody(t, srv.Get("/"))

	if !strings.Contains(body, `hx-get="/tree"`) || !strings.Contains(body, "visibilitychange") {
		t.Errorf("tree does not reload on visibilitychange:\n%s", body)
	}
}

func TestEmptyStoreShowsMessage(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{Store: passuitest.NewFakeStore()})

	body := readBody(t, srv.Get("/"))

	if !strings.Contains(body, "エントリがありません") {
		t.Errorf("empty store has no message:\n%s", body)
	}
}

func TestTreeFailureShowsOnlyErrorID(t *testing.T) {
	h := passuitest.New(t)
	// エラー文はログには出てよい（調査に使う）が、ブラウザには出さない。
	detail := "permission denied: /home/someone/.password-store"
	fake := passuitest.NewFakeStore("a")
	fake.SetErr(errors.New(detail))
	srv := h.Serve(passuitest.Options{Store: fake})

	for _, target := range []string{"/", "/tree"} {
		body := readBody(t, srv.Get(target))
		if !strings.Contains(body, "操作に失敗しました（エラー ID: ") {
			t.Errorf("GET %s: body has no error ID:\n%s", target, body)
		}
		if strings.Contains(body, detail) {
			t.Errorf("GET %s: error detail leaked to browser:\n%s", target, body)
		}
	}
}

func assertOrder(t *testing.T, body string, parts ...string) {
	t.Helper()
	last := -1
	for _, p := range parts {
		i := strings.Index(body, p)
		if i < 0 {
			t.Errorf("body does not contain %s", p)
			return
		}
		if i < last {
			t.Errorf("%s appears out of order in:\n%s", p, body)
			return
		}
		last = i
	}
}
