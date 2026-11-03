package store_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

// testKey は統合テストのストアの GNUPGHOME に足した、パスフレーズ付きの鍵。
type testKey struct {
	t          *testing.T
	env        []string
	id         string
	passphrase string
	probe      string // この鍵あての暗号文
}

// newTestKey は g の GNUPGHOME に、パスフレーズ付きの鍵を 1 つ作る。
func newTestKey(t *testing.T, h *passuitest.Harness, g *passuitest.GPGStore, label string) *testKey {
	t.Helper()
	k := &testKey{t: t, env: append(g.Env(), "LC_ALL=C"), passphrase: h.Canary(label)}
	uid := "passui " + label + " <" + label + "@example.invalid>"
	k.run(k.passphrase+"\n", "gpg", "--batch", "--pinentry-mode", "loopback", "--passphrase-fd", "0",
		"--quick-gen-key", uid, "future-default", "default", "never")
	out := k.run("", "gpg", "--batch", "--with-colons", "--list-secret-keys", "--", uid)
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Split(line, ":"); len(f) > 9 && f[0] == "fpr" {
			k.id = f[9]
			break
		}
	}
	if k.id == "" {
		t.Fatalf("no fingerprint for %s", label)
	}
	k.probe = filepath.Join(t.TempDir(), label+".gpg")
	k.run("probe", "gpg", "--batch", "--trust-model", "always", "--encrypt", "--recipient", k.id, "--output", k.probe)
	return k
}

// unlock はパスフレーズを loopback で渡して 1 回復号し、gpg-agent にキャッシュさせる。
func (k *testKey) unlock() {
	k.t.Helper()
	k.run(k.passphrase+"\n", "gpg", "--batch", "--pinentry-mode", "loopback", "--passphrase-fd", "0",
		"--decrypt", "--output", os.DevNull, k.probe)
}

// cached は、パスフレーズなしで（pinentry を出さずに）復号できるかを返す。
func (k *testKey) cached() bool {
	cmd := exec.Command("gpg", "--batch", "--pinentry-mode", "error", "--decrypt", "--output", os.DevNull, k.probe)
	cmd.Env = k.env
	return cmd.Run() == nil
}

func (k *testKey) run(stdin, name string, args ...string) string {
	k.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = k.env
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		k.t.Fatalf("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return string(out)
}

// ロックはストアの鍵（ルートとサブフォルダの .gpg-id の鍵）だけを対象にし、ストアと
// 関係のない鍵の解錠のキャッシュは消さない。
func TestLockClearsOnlyKeysOfTheStore(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	p := g.Backend()
	sub := newTestKey(t, h, g, "sub")
	sub.run("", "pass", "init", "-p", "team", sub.id)
	other := newTestKey(t, h, g, "other")
	g.Unlock()
	sub.unlock()
	other.unlock()
	ctx := context.Background()

	if err := p.Lock(ctx); err != nil {
		t.Fatal(err)
	}

	if locked, err := p.Locked(ctx); err != nil || !locked {
		t.Errorf("Locked = %v, %v, want true", locked, err)
	}
	if sub.cached() {
		t.Errorf("the key of the subfolder's .gpg-id is still unlocked")
	}
	if !other.cached() {
		t.Errorf("the key unrelated to the store was locked")
	}
}
