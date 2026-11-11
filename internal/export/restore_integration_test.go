package export_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/export"
	"github.com/backpaper0/passui/internal/passuitest"
)

// restoreCommands は README.txt の「手作業で戻す手順」から、コマンドの行（7 文字下げた行）を
// 書かれた順に返す。
func restoreCommands(t *testing.T, readme string) []string {
	t.Helper()
	_, steps, ok := strings.Cut(readme, "\n手作業で戻す手順\n")
	if !ok {
		t.Fatalf("README.txt lacks the manual restore steps:\n%s", readme)
	}
	var cmds []string
	for _, line := range strings.Split(steps, "\n") {
		if cmd, ok := strings.CutPrefix(line, "       "); ok {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		t.Fatalf("restore steps have no commands:\n%s", steps)
	}
	return cmds
}

// guest は戻す先の、使い捨ての新しいゲスト（HOME、GNUPGHOME、PASSWORD_STORE_DIR）。
// GNUPGHOME には、パスフレーズを答える pinentry だけを設定しておく（人が打つ代わり）。
type guest struct {
	home, gnupgHome, storeDir string
}

func newGuest(t *testing.T, passphrase string) *guest {
	t.Helper()
	root, err := os.MkdirTemp(passuitest.ShortTempDir(), "passui-rs-")
	if err != nil {
		t.Fatal(err)
	}
	g := &guest{
		home:      filepath.Join(root, "home"),
		gnupgHome: filepath.Join(root, "gnupg"),
		storeDir:  filepath.Join(root, "store"),
	}
	t.Cleanup(func() {
		cmd := exec.Command("gpgconf", "--kill", "gpg-agent")
		cmd.Env = g.env()
		_ = cmd.Run()
		_ = os.RemoveAll(root)
	})
	for _, d := range []string{g.home, g.gnupgHome} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	pinentry := filepath.Join(root, "pinentry")
	script := "#!/bin/sh\necho 'OK ready'\nwhile read -r cmd rest; do\n  case $cmd in\n" +
		"    GETPIN) printf 'D %s\\nOK\\n' '" + passphrase + "' ;;\n" +
		"    BYE) echo OK; exit 0 ;;\n" +
		"    *) echo OK ;;\n  esac\ndone\n"
	if err := os.WriteFile(pinentry, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	conf := "pinentry-program " + pinentry + "\n"
	if err := os.WriteFile(filepath.Join(g.gnupgHome, "gpg-agent.conf"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	return g
}

func (g *guest) env() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "PASSWORD_STORE_") || strings.HasPrefix(k, "GPG_") || k == "GNUPGHOME" || k == "HOME" {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HOME="+g.home, "GNUPGHOME="+g.gnupgHome, "PASSWORD_STORE_DIR="+g.storeDir, "LC_ALL=C")
}

// runRestoreSteps は README.txt の手作業で戻す手順のコマンドを、書かれたとおりに ng で、
// エクスポートファイルがあるディレクトリ dir で実行し、出力を返す。手順に書いてあるとおり、
// エントリの印だけは entry に置き換える。失敗したらテストを止める。
func runRestoreSteps(t *testing.T, ng *guest, dir, readme, entry string) string {
	t.Helper()
	cmds := restoreCommands(t, readme)
	script := "set -eu\n" + strings.ReplaceAll(strings.Join(cmds, "\n"), "<エントリ>", entry) + "\n"
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir = dir
	cmd.Env = ng.env()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("restore steps failed: %v\n--- script\n%s--- output\n%s", err, script, out.String())
	}
	return out.String()
}

// エクスポートファイルの README.txt の手作業で戻す手順を、そのとおりに別の使い捨てのゲストで実行すると、
// 元のパスフレーズで pass show が通り、ownertrust が効いて gpg -e -r <指紋> が通る。
func TestIntegrationRestoreStepsInReadmeRoundTrip(t *testing.T) {
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
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", "restore roundtrip")
	g.Lock()

	dest := t.TempDir()
	sum, err := writeExport(t, t.Context(), dest, export.Options{
		StoreDir: g.Dir, Now: at, Version: "dev", Env: g.Env(),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	readme := readArchive(t, sum.Path)[keysTop+"README.txt"].body
	ng := newGuest(t, g.Passphrase())
	out := runRestoreSteps(t, ng, dest, readme, "bank")

	if !strings.Contains(out, secret) {
		t.Errorf("pass show did not print the entry:\n%s", out)
	}
	if !strings.Contains(out, "restore roundtrip") {
		t.Errorf("git log -1 did not show the last commit:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(ng.storeDir, "bank.gpg")); err != nil {
		t.Errorf("restored store lacks bank.gpg: %v", err)
	}
	if names := listDir(t, dest); len(names) != 1 {
		t.Errorf("destination has %v after cleanup, want only the export file", names)
	}
}

// .gpg-id のない（ストアの鍵が 1 つもない）ストアのエクスポートファイルでも、README.txt の
// 手作業で戻す手順はそのとおりに最後まで通り、ストアが戻る。
func TestIntegrationRestoreStepsWithoutStoreKeysRunCleanly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not found")
	}
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	if err := os.Remove(filepath.Join(g.Dir, ".gpg-id")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g.Dir, "notes.txt"), []byte("plain\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g.Run("", "git", "-C", g.Dir, "init", "-q")
	g.Run("", "git", "-C", g.Dir, "add", "-A")
	g.Run("", "git", "-C", g.Dir, "-c", "user.name=passui", "-c", "user.email=passui@example.invalid",
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", "no store keys")

	dest := t.TempDir()
	sum, err := writeExport(t, t.Context(), dest, export.Options{
		StoreDir: g.Dir, Now: at, Version: "dev", Env: g.Env(),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(sum.Keys) != 0 {
		t.Fatalf("store keys = %v, want none", sum.Keys)
	}
	readme := readArchive(t, sum.Path)[keysTop+"README.txt"].body
	out := runRestoreSteps(t, newGuest(t, g.Passphrase()), dest, readme, "notes")

	if !strings.Contains(out, "no store keys") {
		t.Errorf("git log -1 did not show the last commit:\n%s", out)
	}
}
