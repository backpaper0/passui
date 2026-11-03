package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/secret"
	"github.com/backpaper0/passui/internal/store"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newTestPass は cfg で Pass を作る。cfg.Dir が空なら一時ディレクトリにする。
// 返す PassConfig は補ったもの。
func newTestPass(t *testing.T, cfg store.PassConfig) (*store.Pass, store.PassConfig, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}
	return store.NewPass(cfg, logger), cfg, &logs
}

func TestEntriesUsesOnlyGPGFileNames(t *testing.T) {
	p, cfg, _ := newTestPass(t, store.PassConfig{})
	for _, f := range []string{
		"bank.gpg", "email/work.gpg", "email/personal.gpg", "a/b/c/deep.gpg",
		".gpg-id", "email/.gpg-id", "notes.txt", ".git/objects/x.gpg", ".extensions/ext.gpg", ".hidden.gpg",
	} {
		writeFile(t, cfg.Dir, f, "x")
	}
	if err := os.MkdirAll(filepath.Join(cfg.Dir, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := p.Entries(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a/b/c/deep", "bank", "email/personal", "email/work"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Entries = %q, want %q", got, want)
	}
}

func TestEntriesOfMissingStoreIsEmpty(t *testing.T) {
	p, _, _ := newTestPass(t, store.PassConfig{Dir: filepath.Join(t.TempDir(), "no-such-store")})

	got, err := p.Entries(context.Background())

	if err != nil || len(got) != 0 {
		t.Errorf("Entries = %q, %v, want empty and no error", got, err)
	}
}

func TestExistsAndHash(t *testing.T) {
	p, cfg, _ := newTestPass(t, store.PassConfig{})
	writeFile(t, cfg.Dir, "email/work.gpg", "ciphertext")
	ctx := context.Background()

	if ok, err := p.Exists(ctx, "email/work"); !ok || err != nil {
		t.Errorf("Exists(email/work) = %v, %v, want true", ok, err)
	}
	for _, name := range []string{"email/missing", "email", "", "../outside", "/etc/passwd", "email//work", "email/work/"} {
		if ok, err := p.Exists(ctx, name); ok || err != nil {
			t.Errorf("Exists(%q) = %v, %v, want false", name, ok, err)
		}
	}

	got, err := p.Hash(ctx, "email/work")
	sum := sha256.Sum256([]byte("ciphertext"))
	if want := hex.EncodeToString(sum[:]); got != want || err != nil {
		t.Errorf("Hash = %q, %v, want %q", got, err, want)
	}
	writeFile(t, cfg.Dir, "email/work.gpg", "changed")
	if changed, _ := p.Hash(ctx, "email/work"); changed == got {
		t.Errorf("Hash did not change after the ciphertext changed")
	}
	for _, name := range []string{"email/missing", "../outside"} {
		if _, err := p.Hash(ctx, name); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Hash(%q) err = %v, want ErrNotFound", name, err)
		}
	}
}

// fakePass は、呼ばれたときの環境を自分と同じディレクトリの env に書き、stdout に
// STDOUT-SECRET を出す。stdin は読み捨てる。引数に "fail" があれば stderr に書いて失敗する。
const fakePass = `#!/bin/sh
cat >/dev/null
env >"$(dirname "$0")/env"
for a in "$@"; do
  if [ "$a" = fail ]; then echo "gpg: decryption failed: STDERR-DETAIL" >&2; exit 2; fi
done
echo "STDOUT-SECRET"
`

// newFakePass は fakePass を pass として使う Pass を作る。ユーザーの環境（Env）には、
// passui が上書きする変数をわざと入れておく。env はさらに足す変数。
func newFakePass(t *testing.T, env ...string) (*store.Pass, store.PassConfig, *bytes.Buffer) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(bin, []byte(fakePass), 0o700); err != nil {
		t.Fatal(err)
	}
	return newTestPass(t, store.PassConfig{
		Pass:      bin,
		GnupgHome: "/gnupg",
		Env: append([]string{
			"KEEP=kept",
			"PASSWORD_STORE_DIR=/user/store",
			"GNUPGHOME=/user/gnupg",
			"LC_ALL=ja_JP.UTF-8",
		}, env...),
	})
}

// passEnv は fakePass が最後に呼ばれたときの環境を返す。
func passEnv(t *testing.T, cfg store.PassConfig) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(filepath.Dir(cfg.Pass), "env"))
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	return env
}

func TestPassIsCalledWithStoreEnvironment(t *testing.T) {
	p, cfg, _ := newFakePass(t)

	if err := p.Create(context.Background(), "new", secret.Of("x")); err != nil {
		t.Fatal(err)
	}

	env := passEnv(t, cfg)
	for k, want := range map[string]string{
		"PASSWORD_STORE_DIR":      cfg.Dir,
		"GNUPGHOME":               "/gnupg",
		"PASSWORD_STORE_GPG_OPTS": "--batch --pinentry-mode error",
		"LC_ALL":                  "C",
		"KEEP":                    "kept",
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
}

func TestPassLogsStderrOnlyOnFailureAndNeverStdinOrStdout(t *testing.T) {
	p, _, logs := newFakePass(t)
	ctx := context.Background()

	if err := p.Generate(ctx, "ok", store.GenerateOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("warned on success:\n%s", logs)
	}

	err := p.Create(ctx, "fail", secret.Of("STDIN-SECRET"))

	if err == nil {
		t.Fatal("Create(fail) succeeded")
	}
	if strings.Contains(err.Error(), "STDERR-DETAIL") {
		t.Errorf("error contains stderr: %v", err)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "STDERR-DETAIL") {
		t.Errorf("stderr is not logged at warn:\n%s", logs)
	}
	for _, s := range []string{"STDIN-SECRET", "STDOUT-SECRET"} {
		if strings.Contains(logs.String(), s) {
			t.Errorf("logs contain stdin or stdout %q:\n%s", s, logs)
		}
	}
}
