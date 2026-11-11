package export_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/backpaper0/passui/internal/export"
)

// at はテストで与える作成日時（ゲストのローカル時刻）。
var at = time.Date(2026, 11, 5, 9, 8, 7, 0, time.Local)

// newStore は使い捨てのストアのディレクトリを作り、files（スラッシュ区切りの相対パス → 中身）を置く。
func newStore(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// entry はアーカイブの 1 つのエントリ。
type entry struct {
	header *tar.Header
	body   string
}

// readArchive は tar.gz を読み、エントリ名 → エントリを返す。
func readArchive(t *testing.T, path string) map[string]entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	got := map[string]entry{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return got
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		got[h.Name] = entry{h, string(b)}
	}
}

// exported は writeExport の結果。
type exported struct {
	export.Summary
	// Path は、Write が書いたものを置いたファイル（dir/Summary.Name）。
	Path string
}

// writeExport は Write でエクスポートファイルを作り、ブラウザがダウンロードして保存するのと
// 同じく、dir に Summary.Name の名前で置く。失敗したら何も置かない。
func writeExport(t *testing.T, ctx context.Context, dir string, opts export.Options) (exported, error) {
	t.Helper()
	var buf bytes.Buffer
	sum, err := export.Write(ctx, &buf, opts)
	if err != nil {
		return exported{Summary: sum}, err
	}
	p := filepath.Join(dir, sum.Name)
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return exported{Summary: sum, Path: p}, nil
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, de := range des {
		names = append(names, de.Name())
	}
	return names
}

func TestWritePutsStoreAndReadmeIntoArchiveNamedByTime(t *testing.T) {
	storeDir := newStore(t, map[string]string{
		".extensions/x":     "echo x\n",
		"bank.gpg":          "ciphertext-bank",
		"email/work.gpg":    "ciphertext-work",
		".git/HEAD":         "ref: refs/heads/main\n",
		"notes/readme.text": "plain",
	})

	sum, err := writeExport(t, t.Context(), t.TempDir(), export.Options{
		StoreDir: storeDir,
		Now:      at,
		Version:  "1.2.3",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if want := "passui-export-20261105-090807.tar.gz"; sum.Name != want {
		t.Errorf("Name = %q, want %q", sum.Name, want)
	}

	got := readArchive(t, sum.Path)
	const top = "passui-export-20261105-090807/"
	for name, body := range map[string]string{
		"store/.extensions/x":     "echo x\n",
		"store/bank.gpg":          "ciphertext-bank",
		"store/email/work.gpg":    "ciphertext-work",
		"store/.git/HEAD":         "ref: refs/heads/main\n",
		"store/notes/readme.text": "plain",
	} {
		e, ok := got[top+name]
		if !ok {
			t.Errorf("archive lacks %s; has %v", top+name, keys(got))
			continue
		}
		if e.body != body {
			t.Errorf("%s = %q, want %q", name, e.body, body)
		}
		if e.header.Typeflag != tar.TypeReg || e.header.FileInfo().Mode().Perm() != 0o600 {
			t.Errorf("%s: type %c mode %v, want a regular file with 0600", name, e.header.Typeflag, e.header.FileInfo().Mode().Perm())
		}
	}
	for _, d := range []string{"store/", "store/email/", "store/.git/"} {
		if e, ok := got[top+d]; !ok || e.header.Typeflag != tar.TypeDir {
			t.Errorf("archive lacks directory %s; has %v", top+d, keys(got))
		}
	}
	for name := range got {
		if !strings.HasPrefix(name, top) {
			t.Errorf("entry %q is outside %s", name, top)
		}
	}

	readme, ok := got[top+"README.txt"]
	if !ok {
		t.Fatalf("archive lacks README.txt; has %v", keys(got))
	}
	for _, s := range []string{"2026-11-05 09:08:07", "1.2.3"} {
		if !strings.Contains(readme.body, s) {
			t.Errorf("README.txt lacks %q:\n%s", s, readme.body)
		}
	}
}

func keys(m map[string]entry) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// Write は w に書くだけで、ゲストには何も残さない（一時ファイルも作らない）。
func TestWriteLeavesNothingOnGuest(t *testing.T) {
	storeDir := newStore(t, map[string]string{"bank.gpg": "x"})
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Chdir(t.TempDir())

	var buf bytes.Buffer
	if _, err := export.Write(t.Context(), &buf, export.Options{StoreDir: storeDir, Now: at}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if buf.Len() == 0 {
		t.Error("Write wrote nothing to w")
	}
	for _, dir := range []string{tmp, "."} {
		if names := listDir(t, dir); len(names) != 0 {
			t.Errorf("%s has %v, want nothing", dir, names)
		}
	}
	if names := listDir(t, storeDir); len(names) != 1 {
		t.Errorf("store has %v, want only bank.gpg", names)
	}
}

func TestWriteFailsWhenStoreFileIsUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read files without permission")
	}
	storeDir := newStore(t, map[string]string{"bank.gpg": "x", "secret/unreadable.gpg": "y"})
	if err := os.Chmod(filepath.Join(storeDir, "secret", "unreadable.gpg"), 0); err != nil {
		t.Fatal(err)
	}

	if _, err := export.Write(t.Context(), io.Discard, export.Options{StoreDir: storeDir, Now: at}); err == nil {
		t.Fatal("Write succeeded, want an error")
	}
}

func TestWriteFailsWhenCancelled(t *testing.T) {
	storeDir := newStore(t, map[string]string{"bank.gpg": "x"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := export.Write(ctx, io.Discard, export.Options{StoreDir: storeDir, Now: at}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestWriteSkipsSocketsAndListsThemInReadme(t *testing.T) {
	// UNIX ソケットのパスは短くないといけないので、t.TempDir より短い場所にストアを作る。
	storeDir, err := os.MkdirTemp("", "passui")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(storeDir) })
	if err := os.WriteFile(filepath.Join(storeDir, "bank.gpg"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(storeDir, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agent.sock", "run/other.sock"} {
		l, err := net.Listen("unix", filepath.Join(storeDir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { l.Close() })
	}
	dest := t.TempDir()

	sum, err := writeExport(t, t.Context(), dest, export.Options{StoreDir: storeDir, Now: at})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if sum.SkippedSpecialFiles != 2 {
		t.Errorf("SkippedSpecialFiles = %d, want 2", sum.SkippedSpecialFiles)
	}
	got := readArchive(t, sum.Path)
	const top = "passui-export-20261105-090807/"
	for _, name := range []string{"store/agent.sock", "store/run/other.sock"} {
		if _, ok := got[top+name]; ok {
			t.Errorf("archive has %s, want it skipped", name)
		}
	}
	if _, ok := got[top+"store/bank.gpg"]; !ok {
		t.Errorf("archive lacks store/bank.gpg; has %v", keys(got))
	}
	readme := got[top+"README.txt"].body
	for _, s := range []string{"store/agent.sock", "store/run/other.sock"} {
		if !strings.Contains(readme, s) {
			t.Errorf("README.txt lacks %q:\n%s", s, readme)
		}
	}
}

func TestWriteSaysNothingWasSkippedWhenStoreHasNoSpecialFiles(t *testing.T) {
	storeDir := newStore(t, map[string]string{"bank.gpg": "x"})

	sum, err := writeExport(t, t.Context(), t.TempDir(), export.Options{StoreDir: storeDir, Now: at})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if sum.SkippedSpecialFiles != 0 {
		t.Errorf("SkippedSpecialFiles = %d, want 0", sum.SkippedSpecialFiles)
	}
	readme := readArchive(t, sum.Path)["passui-export-20261105-090807/README.txt"].body
	if !strings.Contains(readme, "飛ばしたファイル: なし") {
		t.Errorf("README.txt does not say nothing was skipped:\n%s", readme)
	}
}

func TestWriteKeepsSymlinksAsLinksWithoutFollowingThem(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside-the-store"), 0o600); err != nil {
		t.Fatal(err)
	}
	storeDir := newStore(t, map[string]string{"bank.gpg": "ciphertext-bank"})
	links := map[string]string{
		"alias.gpg":   "bank.gpg",
		"outside.txt": filepath.Join(outside, "secret.txt"),
		"outside-dir": outside,
	}
	for link, target := range links {
		if err := os.Symlink(target, filepath.Join(storeDir, link)); err != nil {
			t.Fatal(err)
		}
	}
	dest := t.TempDir()

	sum, err := writeExport(t, t.Context(), dest, export.Options{StoreDir: storeDir, Now: at})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := readArchive(t, sum.Path)
	const top = "passui-export-20261105-090807/"
	for link, target := range links {
		e, ok := got[top+"store/"+link]
		if !ok {
			t.Errorf("archive lacks store/%s; has %v", link, keys(got))
			continue
		}
		if e.header.Typeflag != tar.TypeSymlink || e.header.Linkname != target {
			t.Errorf("%s: type %c linkname %q, want a symlink to %q", link, e.header.Typeflag, e.header.Linkname, target)
		}
	}
	for name, e := range got {
		if strings.Contains(e.body, "outside-the-store") || strings.HasPrefix(name, top+"store/outside-dir/") {
			t.Errorf("archive has %s from outside the store", name)
		}
	}
}

func TestWriteKeepsPermissionsWhenExtractedWithTar(t *testing.T) {
	tarCmd, err := exec.LookPath("tar")
	if err != nil {
		t.Skip("tar is not installed")
	}
	storeDir := newStore(t, map[string]string{
		".git/HEAD":             "ref: refs/heads/main\n",
		".git/hooks/pre-commit": "#!/bin/sh\n",
		".git/objects/ab/cdef":  "object",
		"shared/notes.txt":      "plain",
	})
	modes := map[string]os.FileMode{
		".git/hooks/pre-commit": 0o755,
		".git/objects/ab/cdef":  0o444,
		"shared/notes.txt":      0o644,
		".git/objects/ab":       0o755,
		"shared":                0o750,
	}
	for name, mode := range modes {
		if err := os.Chmod(filepath.Join(storeDir, filepath.FromSlash(name)), mode); err != nil {
			t.Fatal(err)
		}
	}

	sum, err := writeExport(t, t.Context(), t.TempDir(), export.Options{StoreDir: storeDir, Now: at})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	out := t.TempDir()
	cmd := exec.Command(tarCmd, "xzf", sum.Path)
	cmd.Dir = out
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tar xzf: %v\n%s", err, b)
	}
	restored := filepath.Join(out, "passui-export-20261105-090807", "store")
	for name, mode := range modes {
		fi, err := os.Stat(filepath.Join(restored, filepath.FromSlash(name)))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		// tar xzf は umask を当てるので、umask で落ちうるビットは比べない。
		if got, want := fi.Mode().Perm()&^0o022, mode&^0o022; got != want {
			t.Errorf("%s mode = %v, want %v", name, fi.Mode().Perm(), mode)
		}
	}
	if b, err := os.ReadFile(filepath.Join(restored, ".git", "HEAD")); err != nil || string(b) != "ref: refs/heads/main\n" {
		t.Errorf(".git/HEAD = %q, %v", b, err)
	}
}

func TestWriteCopiesStoreThroughSymlinkedStoreDir(t *testing.T) {
	real := newStore(t, map[string]string{"bank.gpg": "ciphertext-bank"})
	storeDir := filepath.Join(t.TempDir(), "password-store")
	if err := os.Symlink(real, storeDir); err != nil {
		t.Fatal(err)
	}

	sum, err := writeExport(t, t.Context(), t.TempDir(), export.Options{StoreDir: storeDir, Now: at})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := readArchive(t, sum.Path)
	const top = "passui-export-20261105-090807/"
	if e := got[top+"store/"]; e.header == nil || e.header.Typeflag != tar.TypeDir {
		t.Errorf("store/ is not a directory; has %v", keys(got))
	}
	if e := got[top+"store/bank.gpg"]; e.body != "ciphertext-bank" {
		t.Errorf("store/bank.gpg = %q; has %v", e.body, keys(got))
	}
}
