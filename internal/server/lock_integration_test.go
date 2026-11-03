package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

func TestIntegrationLockButtonLocksAgentAndKeepsLogin(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	password := h.Canary("password")
	g.Insert("bank", password)
	g.Unlock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})
	if res, body := openEntry(t, srv, "bank"); res.StatusCode != http.StatusOK {
		t.Fatalf("before lock: status = %d, want 200:\n%s", res.StatusCode, body)
	}

	res, body := postLock(t, srv)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("lock: status = %d, want 200:\n%s", res.StatusCode, body)
	}

	// ログインは続いていて、エントリを開くとロック中の案内が返る。
	res, body = openEntry(t, srv, "bank")
	if res.StatusCode != http.StatusLocked {
		t.Errorf("after lock: status = %d, want 423:\n%s", res.StatusCode, body)
	}
	if strings.Contains(body, password) {
		t.Errorf("locked response contains the entry:\n%s", body)
	}
	if body := readBody(t, srv.Get("/lock-status")); !strings.Contains(body, ">ロック中<") {
		t.Errorf("badge is not locked:\n%s", body)
	}
}

func TestIntegrationLockWhileAlreadyLockedSucceeds(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	srv := h.Serve(passuitest.Options{Store: g.Backend()})

	if res, body := postLock(t, srv); res.StatusCode != http.StatusOK {
		t.Errorf("lock: status = %d, want 200:\n%s", res.StatusCode, body)
	}
}
