package service_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/backpaper0/passui/internal/secret"
	"github.com/backpaper0/passui/internal/service"
)

const shim = "/home/alice/.local/share/mise/shims/passui"

// baseEnv はシェルの環境の例。mise activate の一時的なパスが PATH に入っている。
func baseEnv() map[string]string {
	return map[string]string{
		"HOME":            "/home/alice",
		"XDG_RUNTIME_DIR": "/run/user/1000",
		"PATH":            "/home/alice/.local/share/mise/installs/go/1.27.3/bin:/home/alice/.local/share/mise/shims:/usr/local/bin:/usr/bin:/bin",
	}
}

type result struct {
	code           int
	stdout, stderr string
}

// install は fake で install-service を動かす。ログイン URL は fake の urls から読む。
func install(t *testing.T, sys *fakeSystem, env map[string]string) result {
	t.Helper()
	return runInstall(t, sys, env, sys.urls)
}

func runInstall(t *testing.T, sys *fakeSystem, env map[string]string, urls *loginURLs) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := service.Install(context.Background(), service.Options{
		System:      sys,
		Env:         env,
		Stdout:      &stdout,
		Stderr:      &stderr,
		LoginURL:    urls.read,
		WaitTimeout: 300 * time.Millisecond,
	})
	return result{code, stdout.String(), stderr.String()}
}

func golden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertUnit(t *testing.T, sys *fakeSystem, goldenName string) {
	t.Helper()
	if got, want := sys.unit(t), golden(t, goldenName); got != want {
		t.Errorf("unit does not match testdata/%s\n--- got ---\n%s\n--- want ---\n%s", goldenName, got, want)
	}
}

func TestInstallWithMiseShim(t *testing.T) {
	sys := newFakeSystem(t)
	sys.files[shim] = true
	sys.files[filepath.Dir(shim)] = true

	r := install(t, sys, baseEnv())

	if r.code != 0 {
		t.Fatalf("code = %d, stderr:\n%s", r.code, r.stderr)
	}
	assertUnit(t, sys, "shim.service")
	want := []string{"daemon-reload", "enable passui.service", "restart passui.service"}
	if !slices.Equal(sys.calls, want) {
		t.Errorf("systemctl calls = %q, want %q", sys.calls, want)
	}
}

func TestInstallCopiesGnupgHomeAndPasswordStoreVars(t *testing.T) {
	sys := newFakeSystem(t)
	sys.files[shim] = true
	sys.files[filepath.Dir(shim)] = true
	env := baseEnv()
	env["GNUPGHOME"] = "/home/alice/.gnupg"
	env["PASSWORD_STORE_DIR"] = "/home/alice/my store"
	env["PASSWORD_STORE_GENERATED_LENGTH"] = "32"
	env["PASSWORD_STORE_CHARACTER_SET"] = `a-z%"\`
	env["EDITOR"] = "vim"
	env["PASSUI_PORT"] = "4000" // env ファイルで設定するもの。unit には書き写さない

	r := install(t, sys, env)

	if r.code != 0 {
		t.Fatalf("code = %d, stderr:\n%s", r.code, r.stderr)
	}
	assertUnit(t, sys, "env.service")
	if strings.Contains(r.stderr, "警告") {
		t.Errorf("unexpected warning for the default GNUPGHOME:\n%s", r.stderr)
	}
}

func TestInstallAddsDirectoryOfPassOutsideSystemPath(t *testing.T) {
	sys := newFakeSystem(t)
	sys.files[shim] = true
	sys.files[filepath.Dir(shim)] = true
	sys.passPath = "/home/alice/.nix-profile/bin/pass"

	r := install(t, sys, baseEnv())

	if r.code != 0 {
		t.Fatalf("code = %d, stderr:\n%s", r.code, r.stderr)
	}
	assertUnit(t, sys, "path.service")
}

func TestInstallDoesNotAddMiseInstallDirectoryOfPass(t *testing.T) {
	// mise で入れた pass は shims から見つかる。バージョン付きのパスは unit に入れない。
	sys := newFakeSystem(t)
	sys.files[shim] = true
	sys.files[filepath.Dir(shim)] = true
	sys.passPath = "/home/alice/.local/share/mise/installs/pass/1.7.4/bin/pass"

	r := install(t, sys, baseEnv())

	if r.code != 0 {
		t.Fatalf("code = %d, stderr:\n%s", r.code, r.stderr)
	}
	assertUnit(t, sys, "shim.service")
}

func TestInstallFailsWithoutWritingWhenPreconditionIsMissing(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*fakeSystem)
		want  []string // stderr に含まれるべき案内
	}{
		{"no systemd user session", func(s *fakeSystem) { s.noSystemd = true }, []string{"systemd"}},
		{"no systemd on WSL2", func(s *fakeSystem) { s.noSystemd = true; s.wsl = true }, []string{"/etc/wsl.conf", "systemd=true", "wsl --shutdown"}},
		{"no pass", func(s *fakeSystem) { s.passPath = "" }, []string{"pass"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sys := newFakeSystem(t)
			sys.files[shim] = true
			tt.setup(sys)

			r := install(t, sys, baseEnv())

			if r.code != 1 {
				t.Errorf("code = %d, want 1", r.code)
			}
			for _, s := range tt.want {
				if !strings.Contains(r.stderr, s) {
					t.Errorf("stderr lacks %q:\n%s", s, r.stderr)
				}
			}
			if _, err := os.Stat(sys.unitDir); !os.IsNotExist(err) {
				t.Errorf("unit directory was created (err=%v)", err)
			}
			if len(sys.calls) != 0 {
				t.Errorf("systemctl was called: %q", sys.calls)
			}
			if r.stdout != "" {
				t.Errorf("stdout = %q, want empty", r.stdout)
			}
		})
	}
}

func TestInstallWarnsAndContinues(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*fakeSystem, map[string]string)
		want  []string // stderr に含まれるべき案内
	}{
		{"linger disabled", func(s *fakeSystem, _ map[string]string) { s.linger = false }, []string{"警告", "loginctl enable-linger"}},
		{"gpg-agent.socket not enabled", func(s *fakeSystem, _ map[string]string) { s.agentSocket = false }, []string{"警告", "systemctl --user enable gpg-agent.socket"}},
		{"non-default GNUPGHOME", func(_ *fakeSystem, env map[string]string) { env["GNUPGHOME"] = "/home/alice/work/gnupg" }, []string{"警告", "GNUPGHOME", "/home/alice/work/gnupg"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sys := newFakeSystem(t)
			sys.files[shim] = true
			env := baseEnv()
			tt.setup(sys, env)

			r := install(t, sys, env)

			if r.code != 0 {
				t.Errorf("code = %d, want 0; stderr:\n%s", r.code, r.stderr)
			}
			for _, s := range tt.want {
				if !strings.Contains(r.stderr, s) {
					t.Errorf("stderr lacks %q:\n%s", s, r.stderr)
				}
			}
			if sys.unit(t) == "" {
				t.Error("unit was not written")
			}
			if !slices.Contains(sys.calls, "restart passui.service") {
				t.Errorf("service was not started: %q", sys.calls)
			}
		})
	}
}

// loginURLs はログイン URL のファイルの代わり。restart で新しい serve が書いたことにする。
type loginURLs struct {
	mu      sync.Mutex
	current string // 空なら起動していない
}

func (l *loginURLs) set(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.current = s
}

func (l *loginURLs) read() (secret.Value, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current == "" {
		return secret.Value{}, errors.New("passui は起動していません")
	}
	return secret.Of(l.current), nil
}

func TestInstallPrintsLoginURLWrittenByRestartedService(t *testing.T) {
	const old = "http://passui.localhost:3560/login?token=old"
	const fresh = "http://passui.localhost:3560/login?token=fresh"
	for name, before := range map[string]string{"first install": "", "already running": old} {
		t.Run(name, func(t *testing.T) {
			urls := &loginURLs{current: before}
			sys := newFakeSystem(t)
			sys.files[shim] = true
			// 新しい serve は少し遅れて URL ファイルを書く。
			sys.onRestart = func() {
				urls.set("")
				time.AfterFunc(50*time.Millisecond, func() { urls.set(fresh) })
			}

			r := runInstall(t, sys, baseEnv(), urls)

			if r.code != 0 {
				t.Fatalf("code = %d, stderr:\n%s", r.code, r.stderr)
			}
			if !strings.HasSuffix(r.stdout, fresh+"\n") {
				t.Errorf("stdout does not end with the fresh login URL:\n%s", r.stdout)
			}
			if strings.Contains(r.stdout+r.stderr, old) {
				t.Errorf("printed the stale login URL:\n%s%s", r.stdout, r.stderr)
			}
		})
	}
}

func TestInstallGuidesToJournalctlWhenLoginURLDoesNotAppear(t *testing.T) {
	const old = "http://passui.localhost:3560/login?token=old"
	for name, onRestart := range map[string]func(*loginURLs){
		"never written":       func(u *loginURLs) { u.set("") },
		"old file left as is": func(*loginURLs) {},
	} {
		t.Run(name, func(t *testing.T) {
			urls := &loginURLs{current: old}
			sys := newFakeSystem(t)
			sys.files[shim] = true
			sys.onRestart = func() { onRestart(urls) }

			r := runInstall(t, sys, baseEnv(), urls)

			if r.code != 1 {
				t.Errorf("code = %d, want 1", r.code)
			}
			if !strings.Contains(r.stderr, "journalctl --user -u passui") {
				t.Errorf("stderr does not guide to journalctl:\n%s", r.stderr)
			}
			if strings.Contains(r.stdout+r.stderr, old) {
				t.Errorf("printed the stale login URL:\n%s%s", r.stdout, r.stderr)
			}
		})
	}
}

func TestInstallWithoutMiseShimWritesExecutablePathAndWarns(t *testing.T) {
	sys := newFakeSystem(t)
	sys.executable = "/home/alice/bin/passui"

	r := install(t, sys, baseEnv())

	if r.code != 0 {
		t.Fatalf("code = %d, stderr:\n%s", r.code, r.stderr)
	}
	assertUnit(t, sys, "executable.service")
	if !strings.Contains(r.stderr, "/home/alice/bin/passui") || !strings.Contains(r.stderr, "install-service") {
		t.Errorf("stderr does not warn that the path changes on upgrade:\n%s", r.stderr)
	}
}
