package export_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/backpaper0/passui/internal/export"
	"github.com/backpaper0/passui/internal/passuitest"
)

// エクスポートは取るだけで、gpg の状態を変えない。ownertrust を変えた直後で trustdb の
// 検査が待っていても、trustdb.gpg を書き換えない。
func TestIntegrationWriteDoesNotUpdateTrustdb(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	other := g.AddPublicKey("other")
	// ownertrust を変えると、trustdb の検査が要る状態になる。
	g.Run(other+":4:\n", "gpg", "--batch", "--import-ownertrust")
	trustdb := filepath.Join(g.GnupgHome, "trustdb.gpg")
	before, err := os.ReadFile(trustdb)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := writeExport(t, t.Context(), t.TempDir(), export.Options{
		StoreDir: g.Dir, Now: at, Version: "dev",
		Env: append(g.Env(), "PASSWORD_STORE_KEY="+other),
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	after, err := os.ReadFile(trustdb)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("export updated trustdb.gpg")
	}
}
