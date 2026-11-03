package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

func TestIntegrationSearchWorksWhileLocked(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("dev/github", h.Canary("password"))
	g.Insert("email/work", h.Canary("password"))
	g.Lock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})

	status, body := search(t, srv, "git")

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", status, body)
	}
	if !strings.Contains(body, `data-entry="dev/github"`) || !strings.Contains(body, `dev/<mark>git</mark>hub`) {
		t.Errorf("search result is missing while locked:\n%s", body)
	}
	if strings.Contains(body, `data-entry="email/work"`) {
		t.Errorf("non-matching entry is listed:\n%s", body)
	}
}
