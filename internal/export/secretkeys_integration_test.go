package export_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/export"
	"github.com/backpaper0/passui/internal/passuitest"
)

// keygripsOf は gpg --with-keygrip で、鍵 fpr の主鍵と副鍵の keygrip を返す。
func keygripsOf(t *testing.T, g *passuitest.GPGStore, fpr string) []string {
	t.Helper()
	out := g.Run("", "gpg", "--batch", "--with-colons", "--with-keygrip", "--list-keys", "--", fpr)
	var grips []string
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Split(line, ":"); len(f) > 9 && f[0] == "grp" {
			grips = append(grips, f[9])
		}
	}
	if len(grips) == 0 {
		t.Fatalf("no keygrip for %s", fpr)
	}
	return grips
}

// secretKeyEntries は、アーカイブの gnupg/private-keys-v1.d/ の下の .key の keygrip を返す。
func secretKeyEntries(got map[string]entry) []string {
	var grips []string
	for name := range got {
		if rest, ok := strings.CutPrefix(name, keysTop+"gnupg/private-keys-v1.d/"); ok && strings.HasSuffix(rest, ".key") {
			grips = append(grips, strings.TrimSuffix(rest, ".key"))
		}
	}
	slices.Sort(grips)
	return grips
}

func keyFile(g *passuitest.GPGStore, grip string) string {
	return filepath.Join(g.GnupgHome, "private-keys-v1.d", grip+".key")
}

// パスフレーズ付きのストアの鍵の .key（主鍵と副鍵）が、元のファイルと同じ中身で入る。
// GPGStore の GNUPGHOME は既定（~/.gnupg）以外の場所なので、そこから見つけられることも確かめる。
// ストアと関係のない鍵の .key は入らない。
func TestIntegrationWritePutsSecretKeyFilesOfStoreKeys(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	unrelated := g.AddKey("unrelated")

	sum, err := writeExport(t, t.Context(), t.TempDir(), export.Options{
		StoreDir: g.Dir, Now: at, Version: "dev", Env: g.Env(),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := readArchive(t, sum.Path)
	want := sortedCopy(keygripsOf(t, g, g.KeyID))
	if grips := secretKeyEntries(got); !slices.Equal(grips, want) {
		t.Fatalf("archive has .key for %v, want %v (not %v)", grips, want, keygripsOf(t, g, unrelated))
	}
	for _, grip := range want {
		orig, err := os.ReadFile(keyFile(g, grip))
		if err != nil {
			t.Fatal(err)
		}
		e := got[keysTop+"gnupg/private-keys-v1.d/"+grip+".key"]
		if e.body != string(orig) {
			t.Errorf("%s.key differs from the original", grip)
		}
		if mode := e.header.FileInfo().Mode().Perm(); mode != 0o600 {
			t.Errorf("%s.key mode = %o, want 600", grip, mode)
		}
	}
	dir, ok := got[keysTop+"gnupg/private-keys-v1.d/"]
	if !ok {
		t.Fatalf("archive lacks gnupg/private-keys-v1.d/; has %v", keys(got))
	}
	if mode := dir.header.FileInfo().Mode().Perm(); mode != 0o700 {
		t.Errorf("private-keys-v1.d mode = %o, want 700", mode)
	}
	if sum.SecretKeys != 1 {
		t.Errorf("Summary.SecretKeys = %d, want 1", sum.SecretKeys)
	}
	readme := got[keysTop+"README.txt"].body
	for _, grip := range want {
		if !strings.Contains(readme, grip) {
			t.Errorf("README.txt lacks the keygrip %s:\n%s", grip, readme)
		}
	}
}

// 主鍵がオフライン（sec#）の鍵では、秘密部分のある副鍵の .key だけが入る。
func TestIntegrationWritePutsOnlySubkeySecretsOfOfflinePrimaryKey(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	grips := keygripsOf(t, g, g.KeyID) // 主鍵、暗号化副鍵の順
	if len(grips) != 2 {
		t.Fatalf("keygrips = %v, want primary and one subkey", grips)
	}
	// 主鍵の鍵ファイルを消すと、gpg は主鍵を sec# と表示する。
	if err := os.Remove(keyFile(g, grips[0])); err != nil {
		t.Fatal(err)
	}
	if out := g.Run("", "gpg", "--batch", "--list-secret-keys", "--", g.KeyID); !strings.Contains(out, "sec#") {
		t.Fatalf("primary key is not offline:\n%s", out)
	}

	sum, err := writeExport(t, t.Context(), t.TempDir(), export.Options{
		StoreDir: g.Dir, Now: at, Version: "dev", Env: g.Env(),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := readArchive(t, sum.Path)
	if entries := secretKeyEntries(got); !slices.Equal(entries, grips[1:]) {
		t.Errorf("archive has .key for %v, want only the subkey %v", entries, grips[1:])
	}
	if sum.SecretKeys != 1 {
		t.Errorf("Summary.SecretKeys = %d, want 1", sum.SecretKeys)
	}
}

// ゲストに秘密鍵のないストアの鍵は、公開鍵だけが入り、.key はない。README.txt に、
// その鍵には秘密鍵がないことと、その鍵だけで暗号化したエントリは復号できないことが書かれる。
func TestIntegrationWriteNotesStoreKeysWithoutSecretKey(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	public := g.AddPublicKey("public")
	g.Run("", "pass", "init", "-p", "team", public)

	sum, err := writeExport(t, t.Context(), t.TempDir(), export.Options{
		StoreDir: g.Dir, Now: at, Version: "dev", Env: g.Env(),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := readArchive(t, sum.Path)
	want := sortedCopy(keygripsOf(t, g, g.KeyID))
	if entries := secretKeyEntries(got); !slices.Equal(entries, want) {
		t.Errorf("archive has .key for %v, want only %v (none of %v)", entries, want, keygripsOf(t, g, public))
	}
	if sum.SecretKeys != 1 {
		t.Errorf("Summary.SecretKeys = %d, want 1", sum.SecretKeys)
	}
	readme := got[keysTop+"README.txt"].body
	_, after, ok := strings.Cut(readme, public)
	if !ok {
		t.Fatalf("README.txt lacks %s:\n%s", public, readme)
	}
	if line, _, _ := strings.Cut(strings.TrimPrefix(after, "\n"), "\n"); !strings.Contains(line, "秘密鍵: なし") {
		t.Errorf("README.txt does not say %s has no secret key:\n%s", public, readme)
	}
	if !strings.Contains(readme, "復号できません") {
		t.Errorf("README.txt lacks the note on entries that cannot be decrypted:\n%s", readme)
	}
}

// どのストアの鍵にも秘密鍵があれば、復号できないという注意は書かない。
func TestIntegrationWriteOmitsNoteWhenAllStoreKeysHaveSecretKey(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)

	sum, err := writeExport(t, t.Context(), t.TempDir(), export.Options{
		StoreDir: g.Dir, Now: at, Version: "dev", Env: g.Env(),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	readme := readArchive(t, sum.Path)[keysTop+"README.txt"].body
	if strings.Contains(readme, "復号できません") || strings.Contains(readme, "秘密鍵: なし") {
		t.Errorf("README.txt has the no-secret note though every store key has one:\n%s", readme)
	}
}

// ロック中でも .key を入れられ、エクスポートの後もロックのまま
// （gpg-agent にパスフレーズを求めず、キャッシュもされない）。
func TestIntegrationWriteSecretKeysWhileLockedKeepsLocked(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Lock()

	sum, err := writeExport(t, t.Context(), t.TempDir(), export.Options{
		StoreDir: g.Dir, Now: at, Version: "dev", Env: g.Env(),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if entries := secretKeyEntries(readArchive(t, sum.Path)); len(entries) == 0 {
		t.Error("archive has no .key")
	}
	locked, err := g.Backend().Locked(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Error("store is unlocked after export")
	}
}

// .key が読めなければ、エクスポート全体が失敗し、何も返さない。
// エラーはログに出るので、keygrip を含めない。
func TestIntegrationWriteFailsWhenSecretKeyFileIsUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read any file")
	}
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	grip := keygripsOf(t, g, g.KeyID)[1]
	if err := os.Chmod(keyFile(g, grip), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(keyFile(g, grip), 0o600) })
	_, err := writeExport(t, t.Context(), t.TempDir(), export.Options{
		StoreDir: g.Dir, Now: at, Version: "dev", Env: g.Env(),
	})
	if err == nil {
		t.Fatal("Write succeeded with an unreadable .key")
	}
	if strings.Contains(err.Error(), grip) {
		t.Errorf("error has the keygrip: %v", err)
	}
}
