package export_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/export"
	"github.com/backpaper0/passui/internal/passuitest"
)

const keysTop = "passui-export-20261105-090807/"

// importedFingerprints は、armor の公開鍵 asc を取り込まずに読み、主鍵の指紋を返す。
func importedFingerprints(t *testing.T, g *passuitest.GPGStore, asc string) []string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "public-keys.asc")
	if err := os.WriteFile(file, []byte(asc), 0o600); err != nil {
		t.Fatal(err)
	}
	out := g.Run("", "gpg", "--batch", "--with-colons", "--import-options", "show-only", "--import", file)
	var fprs []string
	primary := false
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, ":")
		switch {
		case f[0] == "pub":
			primary = true
		case f[0] == "sub":
			primary = false
		case len(f) > 9 && f[0] == "fpr" && primary:
			fprs = append(fprs, f[9])
			primary = false
		}
	}
	slices.Sort(fprs)
	return fprs
}

// ストアの鍵は、ルートとサブフォルダの .gpg-id と PASSWORD_STORE_KEY のすべての鍵。
// その公開鍵がすべて public-keys.asc に入り、関係のない鍵は入らない。
func TestIntegrationWritePutsPublicKeysOfAllStoreKeys(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	sub := g.AddKey("sub")
	g.Run("", "pass", "init", "-p", "team", sub)
	fromEnv := g.AddPublicKey("env")
	unrelated := g.AddKey("unrelated")

	sum, err := writeExport(t, t.Context(), t.TempDir(), export.Options{
		StoreDir: g.Dir, Now: at, Version: "dev",
		Env: append(g.Env(), "PASSWORD_STORE_KEY="+fromEnv),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := readArchive(t, sum.Path)
	pub, ok := got[keysTop+"gnupg/public-keys.asc"]
	if !ok {
		t.Fatalf("archive lacks gnupg/public-keys.asc; has %v", keys(got))
	}
	want := []string{g.KeyID, sub, fromEnv}
	slices.Sort(want)
	if fprs := importedFingerprints(t, g, pub.body); !slices.Equal(fprs, want) {
		t.Errorf("public-keys.asc has %v, want %v (not %s)", fprs, want, unrelated)
	}
	readme := got[keysTop+"README.txt"].body
	for _, fpr := range want {
		if !strings.Contains(readme, fpr) {
			t.Errorf("README.txt lacks the store key %s:\n%s", fpr, readme)
		}
	}
	if strings.Contains(readme, unrelated) {
		t.Errorf("README.txt has the unrelated key %s:\n%s", unrelated, readme)
	}
	if !slices.Equal(sortedCopy(sum.Keys), want) {
		t.Errorf("Summary.Keys = %v, want %v", sum.Keys, want)
	}
}

func sortedCopy(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return s
}

// ownertrust.txt には、ストアの鍵の行だけがある。
func TestIntegrationWritePutsOwnertrustOfStoreKeysOnly(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	unrelated := g.AddKey("unrelated") // ownertrust は ultimate
	noTrust := g.AddPublicKey("notrust")
	g.Run("", "pass", "init", "-p", "team", noTrust)

	sum, err := writeExport(t, t.Context(), t.TempDir(), export.Options{
		StoreDir: g.Dir, Now: at, Version: "dev", Env: g.Env(),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := readArchive(t, sum.Path)
	trust, ok := got[keysTop+"gnupg/ownertrust.txt"]
	if !ok {
		t.Fatalf("archive lacks gnupg/ownertrust.txt; has %v", keys(got))
	}
	if want := g.KeyID + ":6:\n"; trust.body != want {
		t.Errorf("ownertrust.txt = %q, want %q (no line for %s or %s)", trust.body, want, unrelated, noTrust)
	}
}

// ストアの鍵に ownertrust が 1 つもなくても、エクスポートは成功する。
func TestIntegrationWriteSucceedsWithoutOwnertrust(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	noTrust := g.AddPublicKey("notrust")
	if err := os.WriteFile(filepath.Join(g.Dir, ".gpg-id"), []byte(noTrust+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sum, err := writeExport(t, t.Context(), t.TempDir(), export.Options{
		StoreDir: g.Dir, Now: at, Version: "dev", Env: g.Env(),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := readArchive(t, sum.Path)
	trust, ok := got[keysTop+"gnupg/ownertrust.txt"]
	if !ok {
		t.Fatalf("archive lacks gnupg/ownertrust.txt; has %v", keys(got))
	}
	if trust.body != "" {
		t.Errorf("ownertrust.txt = %q, want empty", trust.body)
	}
}

// 解決できない ID が 1 つでもあれば *UnresolvedKeysError（その ID の一覧付き）で、
// 何も返さない。
func TestIntegrationWriteFailsWithUnresolvedKeys(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	sub := filepath.Join(g.Dir, "team")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".gpg-id"), []byte(g.KeyID+"\nnobody@example.invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const missingEnv = "0123456789ABCDEF0123456789ABCDEF01234567"
	_, err := writeExport(t, t.Context(), t.TempDir(), export.Options{
		StoreDir: g.Dir, Now: at, Version: "dev",
		Env: append(g.Env(), "PASSWORD_STORE_KEY="+missingEnv),
	})

	var unresolved *export.UnresolvedKeysError
	if !errors.As(err, &unresolved) {
		t.Fatalf("err = %v, want *UnresolvedKeysError", err)
	}
	if want := []string{"nobody@example.invalid", missingEnv}; !slices.Equal(sortedCopy(unresolved.IDs), sortedCopy(want)) {
		t.Errorf("IDs = %v, want %v", unresolved.IDs, want)
	}
}
