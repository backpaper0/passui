package service_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestInstallTwiceGivesSameResult(t *testing.T) {
	sys := newFakeSystem(t)
	sys.files[shim] = true
	sys.files[filepath.Dir(shim)] = true

	first := install(t, sys, baseEnv())
	unit1, calls1, files1 := sys.unit(t), sys.calls, listDir(t, sys.unitDir)
	sys.calls = nil
	second := install(t, sys, baseEnv())

	if first.code != 0 || second.code != 0 {
		t.Fatalf("codes = %d, %d; stderr:\n%s%s", first.code, second.code, first.stderr, second.stderr)
	}
	if got := sys.unit(t); got != unit1 {
		t.Errorf("unit changed on the second run:\n%s\n---\n%s", unit1, got)
	}
	if !slices.Equal(sys.calls, calls1) {
		t.Errorf("systemctl calls = %q, first run %q", sys.calls, calls1)
	}
	if got := listDir(t, sys.unitDir); !slices.Equal(got, files1) || !slices.Equal(got, []string{"passui.service"}) {
		t.Errorf("unit directory = %q, first run %q", got, files1)
	}
	if first.stderr != second.stderr {
		t.Errorf("stderr differs:\n%s\n---\n%s", first.stderr, second.stderr)
	}
}

func TestInstallDoesNotTouchEnvFile(t *testing.T) {
	for name, existing := range map[string]*string{"absent": nil, "present": ptr("PASSUI_PORT=4000\n")} {
		t.Run(name, func(t *testing.T) {
			sys := newFakeSystem(t)
			sys.files[shim] = true
			// unit の置き場所は <XDG_CONFIG_HOME>/systemd/user。env ファイルは <XDG_CONFIG_HOME>/passui/env。
			configHome := filepath.Dir(filepath.Dir(sys.unitDir))
			envFile := filepath.Join(configHome, "passui", "env")
			if existing != nil {
				if err := os.MkdirAll(filepath.Dir(envFile), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(envFile, []byte(*existing), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			env := baseEnv()
			env["XDG_CONFIG_HOME"] = configHome

			if r := install(t, sys, env); r.code != 0 {
				t.Fatalf("code = %d, stderr:\n%s", r.code, r.stderr)
			}

			b, err := os.ReadFile(envFile)
			switch {
			case existing == nil && !errors.Is(err, os.ErrNotExist):
				t.Errorf("env file was created (err=%v)", err)
			case existing != nil && string(b) != *existing:
				t.Errorf("env file = %q, want %q (err=%v)", b, *existing, err)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
