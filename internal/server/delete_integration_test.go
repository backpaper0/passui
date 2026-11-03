package server_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

func TestIntegrationDeleteAfterConfirmationRemovesEntryFromStoreAndTree(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("email/work", h.Canary("password"))
	g.Insert("email/personal", h.Canary("password"))
	g.Unlock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})

	if body := readBody(t, srv.Get("/delete/email/work")); !strings.Contains(body, `name="confirm" value="yes"`) {
		t.Fatalf("confirmation is not shown:\n%s", body)
	}
	res := postDelete(srv, "email/work", url.Values{"confirm": {"yes"}})

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, readBody(t, res))
	}
	if _, err := os.Stat(filepath.Join(g.Dir, "email", "work.gpg")); !os.IsNotExist(err) {
		t.Errorf("entry file still exists: %v", err)
	}
	tree := readBody(t, srv.Get("/tree"))
	if strings.Contains(tree, `data-entry="email/work"`) {
		t.Errorf("tree still contains the deleted entry:\n%s", tree)
	}
	if !strings.Contains(tree, `data-entry="email/personal"`) {
		t.Errorf("tree lost another entry:\n%s", tree)
	}
	if !strings.Contains(h.Logs(), "msg=operation op=delete entry=email/work") {
		t.Errorf("operation log is missing:\n%s", h.Logs())
	}
}

func TestIntegrationDeleteWorksWhileLocked(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("bank", h.Canary("password"))
	g.Lock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})

	// ロック中の案内からも削除を選べる。
	if body := readBody(t, srv.Get("/entry/bank")); !strings.Contains(body, `hx-get="/delete/bank"`) {
		t.Fatalf("locked notice has no delete button:\n%s", body)
	}
	res := postDelete(srv, "bank", url.Values{"confirm": {"yes"}})

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, readBody(t, res))
	}
	if _, err := os.Stat(filepath.Join(g.Dir, "bank.gpg")); !os.IsNotExist(err) {
		t.Errorf("entry file still exists: %v", err)
	}
}

func TestIntegrationDeleteEntryRemovedElsewhereReportsNotFound(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("email/work", h.Canary("password"))
	g.Insert("bank", h.Canary("password"))
	srv := h.Serve(passuitest.Options{Store: g.Backend()})
	if body := readBody(t, srv.Get("/delete/email/work")); !strings.Contains(body, "email/work") {
		t.Fatalf("confirmation is not shown:\n%s", body)
	}
	// 確認を開いている間に、ターミナルの pass などで消された。
	if err := os.Remove(filepath.Join(g.Dir, "email", "work.gpg")); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, g.Dir)

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
	if after := snapshot(t, g.Dir); len(after) != len(before) {
		t.Errorf("store changed:\nbefore %v\nafter  %v", before, after)
	}
}
