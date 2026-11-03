package server_test

import (
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

func TestIntegrationTreeShowsEntriesInsertedWithPass(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("email/work", h.Canary("password")+"\nuser: alice")
	g.Insert("bank", h.Canary("password"))
	srv := h.Serve(passuitest.Options{Store: g.Backend()})

	body := readBody(t, srv.Get("/"))

	for _, want := range []string{`data-folder="email"`, `data-entry="email/work"`, `data-entry="bank"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, ".gpg") {
		t.Errorf("tree shows file extensions or .gpg-id:\n%s", body)
	}
}

func TestIntegrationTreeShowsWhileLocked(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("dev/github", h.Canary("password"))
	g.Lock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})

	body := readBody(t, srv.Get("/tree"))

	if !strings.Contains(body, `data-entry="dev/github"`) {
		t.Errorf("tree is not shown while locked:\n%s", body)
	}
}
