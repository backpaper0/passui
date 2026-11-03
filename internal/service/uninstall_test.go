package service_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/service"
)

func uninstall(t *testing.T, sys *fakeSystem, env map[string]string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := service.Uninstall(context.Background(), service.Options{
		System: sys,
		Env:    env,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	return result{code, stdout.String(), stderr.String()}
}

func TestUninstallDisablesStopsRemovesAndReloads(t *testing.T) {
	sys := newFakeSystem(t)
	sys.files[shim] = true
	configHome := filepath.Dir(filepath.Dir(sys.unitDir))
	envFile := filepath.Join(configHome, "passui", "env")
	if err := os.MkdirAll(filepath.Dir(envFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envFile, []byte("PASSUI_PORT=4000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := baseEnv()
	env["XDG_CONFIG_HOME"] = configHome
	if r := install(t, sys, env); r.code != 0 {
		t.Fatalf("install: code = %d, stderr:\n%s", r.code, r.stderr)
	}
	sys.calls = nil

	r := uninstall(t, sys, env)

	if r.code != 0 {
		t.Fatalf("code = %d, stderr:\n%s", r.code, r.stderr)
	}
	// linger と gpg-agent.socket には触らない（ここにない呼び出しはしない）。
	want := []string{"disable --now passui.service", "daemon-reload"}
	if !slices.Equal(sys.calls, want) {
		t.Errorf("systemctl calls = %q, want %q", sys.calls, want)
	}
	if sys.unit(t) != "" {
		t.Error("unit was not removed")
	}
	if b, err := os.ReadFile(envFile); err != nil || string(b) != "PASSUI_PORT=4000\n" {
		t.Errorf("env file changed: %q (err=%v)", b, err)
	}
}

func TestUninstallWhenNotInstalledDoesNothing(t *testing.T) {
	sys := newFakeSystem(t)

	r := uninstall(t, sys, baseEnv())

	if r.code != 0 {
		t.Errorf("code = %d, stderr:\n%s", r.code, r.stderr)
	}
	if len(sys.calls) != 0 {
		t.Errorf("systemctl was called: %q", sys.calls)
	}
	if !strings.Contains(r.stdout, "登録されていません") {
		t.Errorf("stdout does not say it is not installed:\n%s", r.stdout)
	}
}

func TestUninstallFailsWithoutSystemd(t *testing.T) {
	sys := newFakeSystem(t)
	sys.files[shim] = true
	if r := install(t, sys, baseEnv()); r.code != 0 {
		t.Fatalf("install: code = %d, stderr:\n%s", r.code, r.stderr)
	}
	sys.calls = nil
	sys.noSystemd = true

	r := uninstall(t, sys, baseEnv())

	if r.code != 1 || !strings.Contains(r.stderr, "systemd") {
		t.Errorf("code = %d, stderr:\n%s", r.code, r.stderr)
	}
	if len(sys.calls) != 0 {
		t.Errorf("systemctl was called: %q", sys.calls)
	}
	if sys.unit(t) == "" {
		t.Error("unit was removed")
	}
}
