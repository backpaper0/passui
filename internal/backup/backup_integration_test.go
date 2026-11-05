package backup_test

import (
	"testing"

	"github.com/backpaper0/passui/internal/backup"
	"github.com/backpaper0/passui/internal/passuitest"
)

func TestIntegrationWriteWhileLockedSucceedsAndKeepsLocked(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("bank", h.Canary("password"))
	g.Lock()

	sum, err := backup.Write(t.Context(), backup.Options{
		StoreDir: g.Dir, Dir: t.TempDir(), Now: at, Version: "dev", Env: g.Env(),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := readArchive(t, sum.Path)
	for _, name := range []string{"store/bank.gpg", "store/.gpg-id"} {
		if _, ok := got["passui-backup-20261105-090807/"+name]; !ok {
			t.Errorf("archive lacks %s; has %v", name, keys(got))
		}
	}
	locked, err := g.Backend().Locked(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Error("store is unlocked after backup")
	}
}
