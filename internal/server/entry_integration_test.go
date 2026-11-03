package server_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

func TestIntegrationOpenEntryWhileUnlocked(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	password := h.Canary("password")
	user := h.Canary("user")
	g.Insert("email/work", password+"\nuser: "+user+"\nurl: https://example.com/")
	g.Unlock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})

	res, body := openEntry(t, srv, "email/work")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, body)
	}
	if !strings.Contains(body, `type="password" value="`+password+`"`) {
		t.Errorf("password is not masked by default:\n%s", body)
	}
	for _, want := range []string{"<dt>user</dt><dd>" + user + "</dd>", "<dt>url</dt><dd>https://example.com/</dd>"} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %s:\n%s", want, body)
		}
	}
	if !strings.Contains(h.Logs(), "op=show entry=email/work") {
		t.Errorf("no operation log:\n%s", h.Logs())
	}
}

func TestIntegrationOpenEntryWhileLocked(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	password := h.Canary("password")
	user := h.Canary("user")
	g.Insert("dev/github token", password+"\nuser: "+user)
	g.Lock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})

	res, body := openEntry(t, srv, "dev/github%20token")

	if res.StatusCode != http.StatusLocked {
		t.Errorf("status = %d, want 423:\n%s", res.StatusCode, body)
	}
	if !strings.Contains(body, "pass show &#39;dev/github token&#39; &gt;/dev/null") {
		t.Errorf("no unlock one-liner with the entry name:\n%s", body)
	}
	if strings.Contains(body, password) || strings.Contains(body, user) {
		t.Errorf("locked response contains the entry:\n%s", body)
	}
	if strings.Contains(h.Logs(), "op=show") {
		t.Errorf("locked open is logged as a view:\n%s", h.Logs())
	}
}

func TestIntegrationBadgeFollowsLockState(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("a", h.Canary("password"))
	srv := h.Serve(passuitest.Options{Store: g.Backend()})

	g.Lock()
	if body := readBody(t, srv.Get("/")); !strings.Contains(body, ">ロック中<") {
		t.Errorf("badge is not locked:\n%s", body)
	}
	g.Unlock()
	if body := readBody(t, srv.Get("/lock-status")); !strings.Contains(body, ">解錠中<") {
		t.Errorf("badge is not unlocked:\n%s", body)
	}
	g.Lock()
	if body := readBody(t, srv.Get("/lock-status")); !strings.Contains(body, ">ロック中<") {
		t.Errorf("badge is not locked again:\n%s", body)
	}
}

func TestIntegrationUnlockInTerminalThenRetryShowsDetail(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	password := h.Canary("password")
	g.Insert("bank", password)
	srv := h.Serve(passuitest.Options{Store: g.Backend()})

	if res, _ := openEntry(t, srv, "bank"); res.StatusCode != http.StatusLocked {
		t.Fatalf("status = %d, want 423", res.StatusCode)
	}
	g.Unlock() // ターミナルで解錠した
	res, body := openEntry(t, srv, "bank")

	if res.StatusCode != http.StatusOK || !strings.Contains(body, password) {
		t.Errorf("retry after unlock does not show the detail: %d\n%s", res.StatusCode, body)
	}
}

func TestIntegrationOpenMissingEntry(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Unlock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})

	res, body := openEntry(t, srv, "no/such/entry")

	if res.StatusCode != http.StatusNotFound || !strings.Contains(body, "見つかりません") {
		t.Errorf("status = %d, want 404 with not found:\n%s", res.StatusCode, body)
	}
}

func TestIntegrationPassFailureShowsOnlyErrorID(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Unlock()
	// 壊れた暗号文。復号は失敗するが、ロック中ではない。
	if err := os.WriteFile(filepath.Join(g.Dir, "broken.gpg"), []byte("not an OpenPGP message"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := h.Serve(passuitest.Options{Store: g.Backend()})

	res, body := openEntry(t, srv, "broken")

	if res.StatusCode != http.StatusInternalServerError || !strings.Contains(body, "操作に失敗しました（エラー ID: ") {
		t.Errorf("status = %d, want 500 with error ID:\n%s", res.StatusCode, body)
	}
	if strings.Contains(body, "gpg") {
		t.Errorf("stderr leaked to browser:\n%s", body)
	}
	if !strings.Contains(h.Logs(), `msg="pass failed"`) {
		t.Errorf("stderr is not logged:\n%s", h.Logs())
	}
}
