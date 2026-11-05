package server_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/backpaper0/passui/internal/backup"
	"github.com/backpaper0/passui/internal/passuitest"
)

// serveRealBackup は、本物の backup.Write で g のバックアップを dest に置く Handler を立てる。
// エントリの名前と、ストアの鍵の keygrip をカナリアとして登録する。
func serveRealBackup(t *testing.T, h *passuitest.Harness, g *passuitest.GPGStore, dest string) *passuitest.Server {
	t.Helper()
	entry := h.Canary("entry")
	g.Insert(entry, h.Canary("password"))
	for _, grip := range storeKeygrips(t, g) {
		h.AddCanary(grip)
	}
	return h.Serve(passuitest.Options{
		Store: g.Backend(),
		Backup: func(ctx context.Context) (backup.Summary, error) {
			return backup.Write(ctx, backup.Options{
				StoreDir: g.Dir, Dir: dest, Now: time.Now(), Version: "dev", Env: g.Env(),
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

// 本物のバックアップを置いても、ログにエントリの名前も keygrip も出ない（カナリアで確かめる）。
// 指紋は debug のときだけ出る。
func TestIntegrationBackupLogsNeitherEntryNamesNorKeygrips(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	srv := serveRealBackup(t, h, g, t.TempDir())

	res, body := postBackup(t, srv, true)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, body)
	}
	logs := h.Logs()
	if !strings.Contains(logs, "op=backup") || !strings.Contains(logs, "secret_keys=1") {
		t.Fatalf("no operation log with the number of secret keys:\n%s", logs)
	}
	for _, line := range nonDebugLines(logs) {
		if strings.Contains(line, g.KeyID) {
			t.Errorf("fingerprint in a non-debug log line:\n%s", line)
		}
	}
}

// .key が読めずにバックアップが失敗しても、失敗のログに keygrip もエントリの名前も出ない。
func TestIntegrationBackupFailureLogsNoKeygrip(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can read any file")
	}
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	srv := serveRealBackup(t, h, g, t.TempDir())
	grip := storeKeygrips(t, g)[0]
	key := filepath.Join(g.GnupgHome, "private-keys-v1.d", grip+".key")
	if err := os.Chmod(key, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(key, 0o600) })

	res, body := postBackup(t, srv, true)

	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500:\n%s", res.StatusCode, body)
	}
	if logs := h.Logs(); !strings.Contains(logs, "error_id=") || !strings.Contains(logs, "permission denied") {
		t.Errorf("logs lack the error ID and the cause:\n%s", logs)
	}
}
