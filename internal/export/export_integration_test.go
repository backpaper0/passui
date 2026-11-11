package export_test

import (
	"testing"

	"github.com/backpaper0/passui/internal/export"
	"github.com/backpaper0/passui/internal/passuitest"
)

func TestIntegrationWriteWhileLockedSucceedsAndKeepsLocked(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("bank", h.Canary("password"))
	g.Lock()

	sum, err := writeExport(t, t.Context(), t.TempDir(), export.Options{
		StoreDir: g.Dir, Now: at, Version: "dev", Env: g.Env(),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := readArchive(t, sum.Path)
	for _, name := range []string{"store/bank.gpg", "store/.gpg-id"} {
		if _, ok := got["passui-export-20261105-090807/"+name]; !ok {
			t.Errorf("archive lacks %s; has %v", name, keys(got))
		}
	}
	locked, err := g.Backend().Locked(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Error("store is unlocked after export")
	}
}
