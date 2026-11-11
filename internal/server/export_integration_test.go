package server_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/backpaper0/passui/internal/export"
	"github.com/backpaper0/passui/internal/passuitest"
)

// serveRealExport は、本物の export.Write で g のエクスポートファイルを作る Handler を立てる。
// エントリの名前と、ストアの鍵の keygrip をカナリアとして登録する。
func serveRealExport(t *testing.T, h *passuitest.Harness, g *passuitest.GPGStore) *passuitest.Server {
	t.Helper()
	entry := h.Canary("entry")
	g.Insert(entry, h.Canary("password"))
	for _, grip := range storeKeygrips(t, g) {
		h.AddCanary(grip)
	}
	return h.Serve(passuitest.Options{
		Store: g.Backend(),
		Export: func(ctx context.Context, w io.Writer) (export.Summary, error) {
			return export.Write(ctx, w, export.Options{
				StoreDir: g.Dir, Now: time.Now(), Version: "dev", Env: g.Env(),
			})
		},
	})
}

// storeKeygrips は、ストアの鍵の主鍵と副鍵の keygrip を返す。
func storeKeygrips(t *testing.T, g *passuitest.GPGStore) []string {
	t.Helper()
	out := g.Run("", "gpg", "--batch", "--with-colons", "--with-keygrip", "--list-keys", "--", g.KeyID)
	var grips []string
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Split(line, ":"); len(f) > 9 && f[0] == "grp" {
			grips = append(grips, f[9])
		}
	}
	if len(grips) == 0 {
		t.Fatalf("no keygrip for %s", g.KeyID)
	}
	return grips
}

// nonDebugLines は、debug 以外のレベルのログの行を返す。
func nonDebugLines(logs string) []string {
	var lines []string
	for _, line := range strings.Split(logs, "\n") {
		if line != "" && !strings.Contains(line, "level=DEBUG") {
			lines = append(lines, line)
		}
	}
	return lines
}

// tarNames は tar.gz の中身の名前を返す。
func tarNames(t *testing.T, b []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
}

// 本物のエクスポートでは、ダウンロードされるファイルの最上位のディレクトリがファイル名と
// 同じで、その下に store/、gnupg/、README.txt がある。ログにはエントリの名前も keygrip も
// 出ない（カナリアで確かめる）。指紋は debug のときだけ出る。
func TestIntegrationExportDownloadsExportFileAndLogsNeitherEntryNamesNorKeygrips(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	srv := serveRealExport(t, h, g)

	res, body := postExport(t, srv)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, body)
	}
	disp := res.Header.Get("Content-Disposition")
	_, name, ok := strings.Cut(disp, `filename=`)
	name = strings.Trim(name, `"`)
	if !ok || !strings.HasPrefix(name, "passui-export-") || !strings.HasSuffix(name, ".tar.gz") {
		t.Fatalf("Content-Disposition = %q, want passui-export-*.tar.gz", disp)
	}
	top := strings.TrimSuffix(name, ".tar.gz") + "/"
	names := tarNames(t, []byte(body))
	for _, want := range []string{top, top + "store/", top + "gnupg/", top + "README.txt"} {
		if !strings.Contains(strings.Join(names, "\n")+"\n", want+"\n") {
			t.Errorf("archive lacks %s; has %v", want, names)
		}
	}
	logs := h.Logs()
	if !strings.Contains(logs, "op=export") || !strings.Contains(logs, "secret_keys=1") {
		t.Fatalf("no operation log with the number of secret keys:\n%s", logs)
	}
	for _, line := range nonDebugLines(logs) {
		if strings.Contains(line, g.KeyID) {
			t.Errorf("fingerprint in a non-debug log line:\n%s", line)
		}
	}
}

// .key が読めずにエクスポートが失敗したら、ダウンロードされずに結果ページが出て、
// 失敗のログに keygrip もエントリの名前も出ない。
func TestIntegrationExportFailureShowsResultPageAndLogsNoKeygrip(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can read any file")
	}
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	srv := serveRealExport(t, h, g)
	grip := storeKeygrips(t, g)[0]
	key := filepath.Join(g.GnupgHome, "private-keys-v1.d", grip+".key")
	if err := os.Chmod(key, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(key, 0o600) })

	res, body := postExport(t, srv)

	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500:\n%s", res.StatusCode, body)
	}
	assertResultPage(t, res, body)
	if logs := h.Logs(); !strings.Contains(logs, "error_id=") || !strings.Contains(logs, "permission denied") {
		t.Errorf("logs lack the error ID and the cause:\n%s", logs)
	}
}
