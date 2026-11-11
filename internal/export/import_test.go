package export_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/export"
)

// tarEntry は手で作るアーカイブの 1 つのエントリ。
type tarEntry struct {
	name     string
	typeflag byte
	mode     int64
	body     string
	link     string
}

func dirEntry(name string, mode int64) tarEntry {
	return tarEntry{name: name, typeflag: tar.TypeDir, mode: mode}
}

func fileEntry(name, body string, mode int64) tarEntry {
	return tarEntry{name: name, typeflag: tar.TypeReg, mode: mode, body: body}
}

func linkEntry(name, target string) tarEntry {
	return tarEntry{name: name, typeflag: tar.TypeSymlink, mode: 0o777, link: target}
}

// buildArchive は entries をこの順に入れた tar.gz を返す。
func buildArchive(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: e.mode, Linkname: e.link}
		if e.typeflag == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// keylessLayout は、ストアの鍵がない（.gpg-id のないストアの）エクスポートファイルの配置を、
// 最上位のディレクトリ top の下に作る。store の下には extra を足す。
func keylessLayout(top string, extra ...tarEntry) []tarEntry {
	entries := []tarEntry{
		dirEntry(top+"/", 0o700),
		dirEntry(top+"/store/", 0o700),
	}
	entries = append(entries, extra...)
	return append(entries,
		dirEntry(top+"/gnupg/", 0o700),
		fileEntry(top+"/gnupg/public-keys.asc", "", 0o600),
		fileEntry(top+"/gnupg/ownertrust.txt", "", 0o600),
		fileEntry(top+"/README.txt", "readme\n", 0o600),
	)
}

const importTop = "passui-export-20261105-090807"

// importTemp は一時ディレクトリ（TMPDIR）をテスト専用にし、そのパスを返す。
// インポートが一時ディレクトリを後始末したかを確かめるのに使う。
func importTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	return dir
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	if names := listDir(t, dir); len(names) != 0 {
		t.Errorf("%s has %v, want nothing", dir, names)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertMode(t *testing.T, p string, want fs.FileMode) {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != want {
		t.Errorf("mode of %s = %v, want %v", p, got, want)
	}
}

// ストアの鍵がないエクスポートファイルなら、gpg を呼ばずにストアだけを置く。ファイルの中身と
// パーミッション、.git、シンボリックリンクを保ち、一時ディレクトリを残さない。
func TestImportPutsStoreKeepingPermissionsSymlinksAndGit(t *testing.T) {
	tmp := importTemp(t)
	storeDir := filepath.Join(t.TempDir(), "store")
	archive := buildArchive(t, keylessLayout(importTop,
		fileEntry(importTop+"/store/notes.txt", "plain\n", 0o640),
		dirEntry(importTop+"/store/.git/", 0o755),
		fileEntry(importTop+"/store/.git/HEAD", "ref: refs/heads/main\n", 0o644),
		dirEntry(importTop+"/store/email/", 0o750),
		fileEntry(importTop+"/store/email/work.gpg", "ciphertext", 0o600),
		linkEntry(importTop+"/store/alias.gpg", "email/work.gpg"),
		fileEntry(importTop+"/store/.extensions-script", "#!/bin/sh\n", 0o700),
	)...)

	_, err := export.Import(t.Context(), bytes.NewReader(archive), export.ImportOptions{StoreDir: storeDir})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	for name, body := range map[string]string{
		"notes.txt":          "plain\n",
		".git/HEAD":          "ref: refs/heads/main\n",
		"email/work.gpg":     "ciphertext",
		".extensions-script": "#!/bin/sh\n",
	} {
		if got := readFile(t, filepath.Join(storeDir, name)); got != body {
			t.Errorf("%s = %q, want %q", name, got, body)
		}
	}
	assertMode(t, storeDir, 0o700)
	assertMode(t, filepath.Join(storeDir, "notes.txt"), 0o640)
	assertMode(t, filepath.Join(storeDir, ".git"), 0o755)
	assertMode(t, filepath.Join(storeDir, "email"), 0o750)
	assertMode(t, filepath.Join(storeDir, ".extensions-script"), 0o700)
	if target, err := os.Readlink(filepath.Join(storeDir, "alias.gpg")); err != nil || target != "email/work.gpg" {
		t.Errorf("alias.gpg: link to %q (%v), want email/work.gpg", target, err)
	}
	assertEmptyDir(t, tmp)
}

// ストアのディレクトリが空でなければ、何も書かずに ErrStoreNotEmpty で断る。
func TestImportRefusesNonEmptyStoreWithoutWriting(t *testing.T) {
	tmp := importTemp(t)
	storeDir := newStore(t, map[string]string{".gpg-id": "someone\n"})
	archive := buildArchive(t, keylessLayout(importTop,
		fileEntry(importTop+"/store/notes.txt", "plain\n", 0o600),
	)...)

	_, err := export.Import(t.Context(), bytes.NewReader(archive), export.ImportOptions{StoreDir: storeDir})

	if !errors.Is(err, export.ErrStoreNotEmpty) {
		t.Fatalf("Import: %v, want ErrStoreNotEmpty", err)
	}
	if names := listDir(t, storeDir); len(names) != 1 || names[0] != ".gpg-id" {
		t.Errorf("store has %v, want only .gpg-id", names)
	}
	assertEmptyDir(t, tmp)
}

// ストアのディレクトリが空ならインポートできる。StoreEmpty はないときも空のときも true。
func TestImportIntoEmptyStoreDir(t *testing.T) {
	storeDir := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing")
	for _, dir := range []string{storeDir, missing} {
		if empty, err := export.StoreEmpty(dir); err != nil || !empty {
			t.Errorf("StoreEmpty(%s) = %v, %v; want true", dir, empty, err)
		}
	}
	archive := buildArchive(t, keylessLayout(importTop,
		fileEntry(importTop+"/store/notes.txt", "plain\n", 0o600),
	)...)

	if _, err := export.Import(t.Context(), bytes.NewReader(archive), export.ImportOptions{StoreDir: storeDir}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got := readFile(t, filepath.Join(storeDir, "notes.txt")); got != "plain\n" {
		t.Errorf("notes.txt = %q", got)
	}
	if empty, err := export.StoreEmpty(storeDir); err != nil || empty {
		t.Errorf("StoreEmpty after import = %v, %v; want false", empty, err)
	}
}

// 危ないアーカイブや配置の違うアーカイブは *InvalidExportFileError で拒み、ストアにも一時ディレクトリにも
// 何も残さない。
func TestImportRejectsUnsafeOrMalformedArchives(t *testing.T) {
	s := importTop + "/store/"
	tests := map[string][]tarEntry{
		"entry with ../": keylessLayout(importTop, fileEntry(s+"../../evil", "x", 0o600)),
		"absolute entry": append(keylessLayout(importTop), fileEntry("/tmp/evil", "x", 0o600)),
		"absolute link":  keylessLayout(importTop, linkEntry(s+"l", "/etc/passwd")),
		"link out of store": keylessLayout(importTop,
			linkEntry(s+"l", "../../outside")),
		"link to the archive but out of store": keylessLayout(importTop,
			linkEntry(s+"l", "../README.txt")),
		"link escaping through another link": keylessLayout(importTop,
			linkEntry(s+"self", "."),
			linkEntry(s+"l", "self/self/../..")),
		"entry through a link": keylessLayout(importTop,
			dirEntry(s+"real/", 0o700),
			linkEntry(s+"l", "real"),
			fileEntry(s+"l/x", "x", 0o600)),
		"link outside store/": append(keylessLayout(importTop), linkEntry(importTop+"/gnupg/x", "public-keys.asc")),
		"hard link": keylessLayout(importTop,
			fileEntry(s+"a", "x", 0o600),
			tarEntry{name: s + "b", typeflag: tar.TypeLink, link: s + "a"}),
		"two top directories": append(keylessLayout(importTop), dirEntry("other/", 0o700)),
		"no store": {
			dirEntry(importTop+"/", 0o700),
			dirEntry(importTop+"/gnupg/", 0o700),
			fileEntry(importTop+"/gnupg/public-keys.asc", "", 0o600),
			fileEntry(importTop+"/gnupg/ownertrust.txt", "", 0o600),
		},
		"no public keys": {
			dirEntry(importTop+"/", 0o700),
			dirEntry(importTop+"/store/", 0o700),
			dirEntry(importTop+"/gnupg/", 0o700),
			fileEntry(importTop+"/gnupg/ownertrust.txt", "", 0o600),
		},
		"unknown file in gnupg/": append(keylessLayout(importTop), fileEntry(importTop+"/gnupg/secring.gpg", "x", 0o600)),
		"badly named key file": append(keylessLayout(importTop),
			dirEntry(importTop+"/gnupg/private-keys-v1.d/", 0o700),
			fileEntry(importTop+"/gnupg/private-keys-v1.d/not-a-keygrip.key", "x", 0o600)),
		"key file without public keys": append(keylessLayout(importTop),
			dirEntry(importTop+"/gnupg/private-keys-v1.d/", 0o700),
			fileEntry(importTop+"/gnupg/private-keys-v1.d/0123456789ABCDEF0123456789ABCDEF01234567.key", "x", 0o600)),
		"link over an implicitly created directory": keylessLayout(importTop,
			fileEntry(s+"a/b", "x", 0o600),
			linkEntry(s+"a", "c")),
		"duplicate entry": keylessLayout(importTop,
			fileEntry(s+"a", "x", 0o600),
			fileEntry(s+"a", "y", 0o600)),
	}
	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			tmp := importTemp(t)
			storeDir := filepath.Join(t.TempDir(), "store")

			_, err := export.Import(t.Context(), bytes.NewReader(buildArchive(t, entries...)), export.ImportOptions{StoreDir: storeDir})

			var invalid *export.InvalidExportFileError
			if !errors.As(err, &invalid) {
				t.Fatalf("Import: %v, want *InvalidExportFileError", err)
			}
			if _, err := os.Lstat(storeDir); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("store dir exists after a rejected import: %v", err)
			}
			assertEmptyDir(t, tmp)
		})
	}
}

// tar.gz でないものは *InvalidExportFileError で拒む。
func TestImportRejectsNonGzip(t *testing.T) {
	_, err := export.Import(t.Context(), bytes.NewReader([]byte("not a tar.gz")), export.ImportOptions{StoreDir: filepath.Join(t.TempDir(), "store")})
	var invalid *export.InvalidExportFileError
	if !errors.As(err, &invalid) {
		t.Fatalf("Import: %v, want *InvalidExportFileError", err)
	}
}

// 最上位のディレクトリが 1 つだけなら名前は問わない（旧形式の passui-backup-* も受け付ける）。
func TestImportAcceptsAnyTopDirectoryName(t *testing.T) {
	for _, top := range []string{"passui-backup-20261001-120000", "renamed"} {
		storeDir := filepath.Join(t.TempDir(), "store")
		archive := buildArchive(t, keylessLayout(top, fileEntry(top+"/store/notes.txt", "plain\n", 0o600))...)

		sum, err := export.Import(t.Context(), bytes.NewReader(archive), export.ImportOptions{StoreDir: storeDir})
		if err != nil {
			t.Fatalf("Import %s: %v", top, err)
		}
		if sum.Top != top {
			t.Errorf("Top = %q, want %q", sum.Top, top)
		}
		if got := readFile(t, filepath.Join(storeDir, "notes.txt")); got != "plain\n" {
			t.Errorf("notes.txt = %q", got)
		}
	}
}

// ファイルの中身の途中で切れたもの（送られてくる途中で切れたなど）も *InvalidExportFileError で拒む。
func TestImportRejectsTruncatedArchive(t *testing.T) {
	tmp := importTemp(t)
	storeDir := filepath.Join(t.TempDir(), "store")
	big := make([]byte, 256<<10)
	for i := range big {
		big[i] = byte(i * 7919 >> 3)
	}
	archive := buildArchive(t, keylessLayout(importTop, fileEntry(importTop+"/store/big", string(big), 0o600))...)
	truncated := archive[:len(archive)/2]

	_, err := export.Import(t.Context(), bytes.NewReader(truncated), export.ImportOptions{StoreDir: storeDir})

	var invalid *export.InvalidExportFileError
	if !errors.As(err, &invalid) {
		t.Fatalf("Import: %v, want *InvalidExportFileError", err)
	}
	if _, err := os.Lstat(storeDir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("store dir exists after a rejected import: %v", err)
	}
	assertEmptyDir(t, tmp)
}

// エクスポートファイルの README.txt では、passui でインポートする方法を、手作業で戻す手順より先に書く。
func TestReadmeTxtExplainsImportBeforeManualSteps(t *testing.T) {
	sum, err := writeExport(t, t.Context(), t.TempDir(), export.Options{StoreDir: newStore(t, nil), Now: at, Version: "dev"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	readme := readArchive(t, sum.Path)[importTop+"/README.txt"].body
	imp := strings.Index(readme, "\npassui でインポートする\n")
	manual := strings.Index(readme, "\n手作業で戻す手順\n")
	if imp < 0 || manual < 0 || imp > manual {
		t.Errorf("README.txt does not explain the import before the manual steps (import at %d, manual at %d):\n%s", imp, manual, readme)
	}
	if !strings.Contains(readme, "passui を使えないときは") {
		t.Errorf("README.txt does not say the manual steps are for when passui is unavailable:\n%s", readme)
	}
}

// store/ の下のシンボリックリンクと同じ名前のものが store/ の外にあっても、取り違えない。
func TestImportDoesNotConfuseStoreLinksWithPathsOutsideStore(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "store")
	archive := buildArchive(t, keylessLayout(importTop,
		dirEntry(importTop+"/store/real/", 0o700),
		linkEntry(importTop+"/store/gnupg", "real"),
	)...)

	if _, err := export.Import(t.Context(), bytes.NewReader(archive), export.ImportOptions{StoreDir: storeDir}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if target, err := os.Readlink(filepath.Join(storeDir, "gnupg")); err != nil || target != "real" {
		t.Errorf("gnupg: link to %q (%v), want real", target, err)
	}
}
