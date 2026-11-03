package server_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

// postLock は htmx でロック ボタンを押したときと同じリクエストを送る。
func postLock(t *testing.T, srv *passuitest.Server) (*http.Response, string) {
	t.Helper()
	req := srv.NewRequest(http.MethodPost, "/lock", "")
	req.Header.Set("Origin", "http://"+srv.Host)
	req.Header.Set("HX-Request", "true")
	res := srv.Do(req)
	return res, readBody(t, res)
}

func TestTopHasLockButtonWithScopeNote(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{})

	body := readBody(t, srv.Get("/"))

	if !strings.Contains(body, `hx-post="/lock"`) {
		t.Errorf("top lacks a lock button:\n%s", body)
	}
	if !strings.Contains(body, "ターミナルの pass もロックされます") {
		t.Errorf("lock button lacks the scope note:\n%s", body)
	}
}

func TestLockLocksStoreClearsRightPaneAndKeepsLogin(t *testing.T) {
	h := passuitest.New(t)
	password := h.Canary("password")
	fake := passuitest.NewFakeStore()
	fake.Put("bank", password)
	srv := h.Serve(passuitest.Options{Store: fake})

	res, body := postLock(t, srv)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, body)
	}
	// 右ペインは詳細ではなく、案内だけに差し替わる。
	if !strings.Contains(body, "ロックしました") || strings.Contains(body, "<html") {
		t.Errorf("right pane is not replaced with a notice:\n%s", body)
	}
	// ヘッダーのバッジを読み直させる。
	if got := res.Header.Get("HX-Trigger"); got != "lock-changed" {
		t.Errorf("HX-Trigger = %q, want lock-changed", got)
	}
	if locked, _ := fake.Locked(t.Context()); !locked {
		t.Error("store is not locked")
	}
	// ログインは続き、エントリを開くとロック中の案内になる。
	res, body = openEntry(t, srv, "bank")
	if res.StatusCode != http.StatusLocked || strings.Contains(body, password) {
		t.Errorf("open after lock: status = %d, want 423:\n%s", res.StatusCode, body)
	}
	if !strings.Contains(h.Logs(), "op=lock") {
		t.Errorf("no operation log:\n%s", h.Logs())
	}
}

func TestLockWithoutHtmxRedirectsToTop(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{})

	req := srv.NewRequest(http.MethodPost, "/lock", "")
	req.Header.Set("Origin", "http://"+srv.Host)
	res := srv.Do(req)

	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/" {
		t.Errorf("status = %d Location = %q, want 303 /", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestLockFailureShowsOnlyErrorIDAndClearsRightPane(t *testing.T) {
	h := passuitest.New(t)
	fake := passuitest.NewFakeStore()
	srv := h.Serve(passuitest.Options{Store: fake})
	fake.SetErr(errors.New("gpg-connect-agent: boom"))

	res, body := postLock(t, srv)

	if res.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500:\n%s", res.StatusCode, body)
	}
	if !strings.Contains(body, "エラー ID") || strings.Contains(body, "boom") {
		t.Errorf("failure is not reduced to an error ID:\n%s", body)
	}
	// 失敗してもバッジは読み直させる（実際の状態を出すため）。
	if got := res.Header.Get("HX-Trigger"); got != "lock-changed" {
		t.Errorf("HX-Trigger = %q, want lock-changed", got)
	}
}
