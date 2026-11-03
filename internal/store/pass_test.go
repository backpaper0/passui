package store

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

func newTestPass(t *testing.T, cfg PassConfig) (*Pass, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}
	return NewPass(cfg, logger), &logs
}

func TestEntriesUsesOnlyGPGFileNames(t *testing.T) {
	p, _ := newTestPass(t, PassConfig{})
	for _, f := range []string{
		"bank.gpg", "email/work.gpg", "email/personal.gpg", "a/b/c/deep.gpg",
		".gpg-id", "email/.gpg-id", "notes.txt", ".git/objects/x.gpg", ".extensions/ext.gpg", ".hidden.gpg",
	} {
		writeFile(t, p.cfg.Dir, f, "x")
	}
	if err := os.MkdirAll(filepath.Join(p.cfg.Dir, "empty"), 0o700); err != nil {
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
	p, _ := newTestPass(t, PassConfig{Dir: filepath.Join(t.TempDir(), "no-such-store")})

	got, err := p.Entries(context.Background())

	if err != nil || len(got) != 0 {
		t.Errorf("Entries = %q, %v, want empty and no error", got, err)
	}
}

func TestExistsAndHash(t *testing.T) {
	p, _ := newTestPass(t, PassConfig{})
	writeFile(t, p.cfg.Dir, "email/work.gpg", "ciphertext")
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
	writeFile(t, p.cfg.Dir, "email/work.gpg", "changed")
	if changed, _ := p.Hash(ctx, "email/work"); changed == got {
		t.Errorf("Hash did not change after the ciphertext changed")
	}
	for _, name := range []string{"email/missing", "../outside"} {
		if _, err := p.Hash(ctx, name); !errors.Is(err, ErrNotFound) {
			t.Errorf("Hash(%q) err = %v, want ErrNotFound", name, err)
		}
	}
}

// fakePass は環境と引数を stdout に出し、"fail" を渡されたら stderr に書いて失敗する。
const fakePass = `#!/bin/sh
if [ "$1" = fail ]; then echo "gpg: decryption failed: STDERR-DETAIL" >&2; exit 2; fi
cat >/dev/null
echo "PASSWORD_STORE_DIR=$PASSWORD_STORE_DIR"
echo "GNUPGHOME=$GNUPGHOME"
echo "PASSWORD_STORE_GPG_OPTS=$PASSWORD_STORE_GPG_OPTS"
echo "LC_ALL=$LC_ALL"
echo "KEEP=$KEEP"
`

func newFakePass(t *testing.T) (*Pass, *bytes.Buffer) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(bin, []byte(fakePass), 0o700); err != nil {
		t.Fatal(err)
	}
	return newTestPass(t, PassConfig{
		Pass:      bin,
		Dir:       "/store",
		GnupgHome: "/gnupg",
		Env: []string{
			"KEEP=kept",
			"PASSWORD_STORE_DIR=/user/store",
			"GNUPGHOME=/user/gnupg",
			"PASSWORD_STORE_GPG_OPTS=--pinentry-mode loopback",
			"LC_ALL=ja_JP.UTF-8",
		},
	})
}

func TestRunBuildsEnvironmentForPass(t *testing.T) {
	p, _ := newFakePass(t)

	out, err := p.run(context.Background(), nil, "ls")

	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"PASSWORD_STORE_DIR=/store\n",
		"GNUPGHOME=/gnupg\n",
		"PASSWORD_STORE_GPG_OPTS=--batch --pinentry-mode error\n",
		"LC_ALL=C\n",
		"KEEP=kept\n",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("environment does not contain %q:\n%s", want, out)
		}
	}
}

func TestRunLogsStderrOnlyOnFailureAndNeverStdinOrStdout(t *testing.T) {
	p, logs := newFakePass(t)
	ctx := context.Background()

	if _, err := p.run(ctx, []byte("STDIN-SECRET"), "ls"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("warned on success:\n%s", logs)
	}

	_, err := p.run(ctx, []byte("STDIN-SECRET"), "fail")

	if err == nil {
		t.Fatal("run(fail) succeeded")
	}
	if strings.Contains(err.Error(), "STDERR-DETAIL") {
		t.Errorf("error contains stderr: %v", err)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "STDERR-DETAIL") {
		t.Errorf("stderr is not logged at warn:\n%s", logs)
	}
	for _, secret := range []string{"STDIN-SECRET", "PASSWORD_STORE_DIR=/store"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("logs contain stdin or stdout %q:\n%s", secret, logs)
		}
	}
}
