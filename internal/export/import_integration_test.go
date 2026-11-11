package export_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/export"
	"github.com/backpaper0/passui/internal/passuitest"
)

// exportForImport は、git で管理したストアに bank を入れてロックし、そのエクスポートファイルを返す。
func exportForImport(t *testing.T) (*passuitest.GPGStore, string, []byte) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not found")
	}
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	secret := h.Canary("secret")
	g.Insert("bank", secret)
	g.Run("", "git", "-C", g.Dir, "init", "-q")
	g.Run("", "git", "-C", g.Dir, "add", "-A")
	g.Run("", "git", "-C", g.Dir, "-c", "user.name=passui", "-c", "user.email=passui@example.invalid",
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", "import roundtrip")
	g.Lock()

	var buf bytes.Buffer
	if _, err := export.Write(t.Context(), &buf, export.Options{StoreDir: g.Dir, Now: at, Version: "dev", Env: g.Env()}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return g, secret, buf.Bytes()
}

// run は ng の環境で sh -c script を実行し、stdout と stderr を分けて返す。失敗したらテストを止める。
func (ng *guest) run(t *testing.T, script string) (string, string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = ng.env()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\n%s%s", script, err, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String()
}

// assertImported は、ng で元のパスフレーズで pass show が通り、gpg -e -r <指紋> が警告なしに通り、
// git log -1 がエクスポートした時点のコミットを出すことを確かめる。
func assertImported(t *testing.T, ng *guest, fpr, secret string) {
	t.Helper()
	if out, _ := ng.run(t, "pass show bank"); !strings.Contains(out, secret) {
		t.Errorf("pass show did not print the entry:\n%s", out)
	}
	if _, stderr := ng.run(t, "echo test | gpg --batch -e -r "+fpr+" >/dev/null"); stderr != "" {
		t.Errorf("gpg -e printed warnings (ownertrust not imported?):\n%s", stderr)
	}
	if out, _ := ng.run(t, `git -C "$PASSWORD_STORE_DIR" log -1`); !strings.Contains(out, "import roundtrip") {
		t.Errorf("git log -1 did not show the last commit:\n%s", out)
	}
}

// 空の GNUPGHOME と PASSWORD_STORE_DIR にエクスポートファイルをインポートすると、元のパスフレーズで
// pass show が通り、gpg -e -r <指紋> が警告なしに通り、git log -1 がエクスポートした時点のコミットを出す。
func TestIntegrationImportRoundTrip(t *testing.T) {
	g, secret, archive := exportForImport(t)
	ng := newGuest(t, g.Passphrase())

	sum, err := export.Import(t.Context(), bytes.NewReader(archive), export.ImportOptions{StoreDir: ng.storeDir, Env: ng.env()})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !sum.Keys || sum.SecretKeys == 0 {
		t.Errorf("summary = %+v, want keys and secret keys", sum)
	}
	assertMode(t, filepath.Join(ng.gnupgHome, "private-keys-v1.d"), 0o700)
	for _, grip := range keygripsOf(t, g, g.KeyID) {
		if p := filepath.Join(ng.gnupgHome, "private-keys-v1.d", grip+".key"); fileExists(p) {
			assertMode(t, p, 0o600)
		}
	}
	assertImported(t, ng, g.KeyID, secret)
}

// 旧形式（最上位のディレクトリが passui-backup-*）のファイルもインポートできる。
func TestIntegrationImportAcceptsOldBackupFile(t *testing.T) {
	g, secret, archive := exportForImport(t)
	ng := newGuest(t, g.Passphrase())
	old := retop(t, archive, "passui-backup-20261105-090807")

	if _, err := export.Import(t.Context(), bytes.NewReader(old), export.ImportOptions{StoreDir: ng.storeDir, Env: ng.env()}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	assertImported(t, ng, g.KeyID, secret)
}

// 同じ keygrip の .key がゲストにあれば、どの鍵かを *SecretKeyExistsError で返して断り、ストアを置かない。
// エラー文（ログに出る）には keygrip を入れない。
func TestIntegrationImportRefusesExistingSecretKey(t *testing.T) {
	g, _, archive := exportForImport(t)

	storeDir := filepath.Join(t.TempDir(), "store")
	_, err := export.Import(t.Context(), bytes.NewReader(archive), export.ImportOptions{StoreDir: storeDir, Env: g.Env()})

	if !errors.Is(err, export.ErrSecretKeyExists) {
		t.Fatalf("Import: %v, want ErrSecretKeyExists", err)
	}
	var exists *export.SecretKeyExistsError
	if !errors.As(err, &exists) {
		t.Fatalf("Import: %v, want *SecretKeyExistsError", err)
	}
	var want []string
	for _, grip := range keygripsOf(t, g, g.KeyID) {
		if fileExists(keyFile(g, grip)) {
			want = append(want, grip)
		}
	}
	slices.Sort(want)
	if got := slices.Sorted(slices.Values(exists.Keygrips)); !slices.Equal(got, want) {
		t.Errorf("Keygrips = %v, want %v", got, want)
	}
	for _, grip := range want {
		if strings.Contains(err.Error(), grip) {
			t.Errorf("error %q has a keygrip", err)
		}
	}
	if fileExists(storeDir) {
		t.Error("store was put though the import was refused")
	}
}

// .key を置き、ストアを途中まで置いた後で失敗したら、置いた .key とストアを消す。取り込んだ公開鍵と
// ownertrust は残す。
func TestIntegrationImportRollsBackKeysAndStoreOnFailure(t *testing.T) {
	g, _, archive := exportForImport(t)
	ng := newGuest(t, g.Passphrase())
	importTemp(t)
	// ストアの最後（名前の順で後ろ）に深いファイルを足す。一時ディレクトリには展開できるが、
	// パスの長いストアのディレクトリでは PATH_MAX を超えるので、ほかのファイルを置いた後で失敗する。
	deep := "zz"
	for range 18 {
		deep += "/" + strings.Repeat("a", 200)
	}
	top := archiveTop(t, archive)
	archive = appendEntries(t, archive, fileEntry(top+"/store/"+deep, "x", 0o600))
	storeDir := filepath.Join(t.TempDir(), strings.Repeat("b", 250), strings.Repeat("c", 250), "store")

	_, err := export.Import(t.Context(), bytes.NewReader(archive), export.ImportOptions{StoreDir: storeDir, Env: ng.env()})

	if err == nil {
		t.Fatal("Import succeeded, want an error")
	}
	if fileExists(storeDir) {
		t.Error("store was left after the failed import")
	}
	keyDir := filepath.Join(ng.gnupgHome, "private-keys-v1.d")
	if names, err := os.ReadDir(keyDir); err == nil && len(names) != 0 {
		t.Errorf("%s has %d files after the failed import, want none", keyDir, len(names))
	}
	if out, _ := ng.run(t, "gpg --batch --list-keys "+g.KeyID); !strings.Contains(out, g.KeyID) {
		t.Errorf("public key was not kept:\n%s", out)
	}
	if out, _ := ng.run(t, "gpg --export-ownertrust"); !strings.Contains(out, g.KeyID) {
		t.Errorf("ownertrust was not kept:\n%s", out)
	}
}

// archiveTop は tar.gz の最上位のディレクトリの名前を返す。
func archiveTop(t *testing.T, archive []byte) string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	h, err := tar.NewReader(gz).Next()
	if err != nil {
		t.Fatal(err)
	}
	top, _, _ := strings.Cut(h.Name, "/")
	return top
}

// appendEntries は tar.gz の最後に entries を足したものを返す。
func appendEntries(t *testing.T, archive []byte, entries ...tarEntry) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var buf bytes.Buffer
	ogz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(ogz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(tw, tr); err != nil {
			t.Fatal(err)
		}
	}
	writeEntries(t, tw, entries)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ogz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func fileExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// retop は tar.gz の最上位のディレクトリの名前を top に替えたものを返す。
func retop(t *testing.T, archive []byte, top string) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var buf bytes.Buffer
	ogz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(ogz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		_, rest, _ := strings.Cut(h.Name, "/")
		h.Name = top + "/" + rest
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(tw, tr); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ogz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
