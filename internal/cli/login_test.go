package cli_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/backpaper0/passui/internal/cli"
)

// runtimeDirEnv は使い捨ての XDG_RUNTIME_DIR を "KEY=VALUE" で返す。
func runtimeDirEnv(t *testing.T) string {
	t.Helper()
	return "XDG_RUNTIME_DIR=" + t.TempDir()
}

// homeEnv は空の HOME を返す。ストア（~/.password-store）と GNUPGHOME（~/.gnupg）の
// 既定値はここから決まる。
func homeEnv(t *testing.T) string {
	t.Helper()
	return "HOME=" + t.TempDir()
}

func TestLoginWhenNotRunning(t *testing.T) {
	r := run(t, []string{runtimeDirEnv(t)}, "login")

	if r.code != 1 {
		t.Errorf("code = %d, want 1", r.code)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty", r.stdout)
	}
	if !strings.Contains(r.stderr, "起動していない") {
		t.Errorf("stderr does not say passui is not running:\n%s", r.stderr)
	}
}

func TestLoginWithoutRuntimeDirIsError(t *testing.T) {
	r := run(t, nil, "login")

	if r.code != 1 || !strings.Contains(r.stderr, "XDG_RUNTIME_DIR") {
		t.Errorf("code=%d stderr=%s, want an error about XDG_RUNTIME_DIR", r.code, r.stderr)
	}
}

func TestServeWithoutHomeOrStoreDirIsError(t *testing.T) {
	r := run(t, []string{"PATH=" + fakePassDir(t), "PASSUI_PORT=" + strconv.Itoa(freePort(t)), runtimeDirEnv(t)}, "serve")

	if r.code != 1 || !strings.Contains(r.stderr, "HOME") {
		t.Errorf("code=%d stderr=%s, want an error about HOME", r.code, r.stderr)
	}
}

func TestServeWithoutRuntimeDirIsError(t *testing.T) {
	r := run(t, []string{"PATH=" + fakePassDir(t), "PASSUI_PORT=" + strconv.Itoa(freePort(t))}, "serve")

	if r.code != 1 || !strings.Contains(r.stderr, "XDG_RUNTIME_DIR") {
		t.Errorf("code=%d stderr=%s, want an error about XDG_RUNTIME_DIR", r.code, r.stderr)
	}
}

func TestHelpListsLogin(t *testing.T) {
	if r := run(t, nil); !strings.Contains(r.stdout, "login") {
		t.Errorf("help lacks login:\n%s", r.stdout)
	}
}

func TestServeWritesTokenAndLoginPrintsWorkingURL(t *testing.T) {
	runtimeDir := t.TempDir()
	// 前回の残りがゆるいパーミッションで残っていても 0700 に直す。
	if err := os.MkdirAll(filepath.Join(runtimeDir, "passui"), 0o755); err != nil {
		t.Fatal(err)
	}
	storeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(storeDir, "from-store-dir.gpg"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(freePort(t))
	env := []string{"PATH=" + fakePassDir(t), "PASSUI_PORT=" + port, "XDG_RUNTIME_DIR=" + runtimeDir, "PASSUI_LOG_LEVEL=debug",
		"PASSWORD_STORE_DIR=" + storeDir, "GNUPGHOME=" + t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stderr := &lockedBuffer{}
	done := make(chan int, 1)
	go func() { done <- cli.Run(ctx, []string{"serve"}, env, &lockedBuffer{}, stderr) }()

	res := getWithRetry(t, "http://127.0.0.1:"+port+"/", "passui.localhost:"+port)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("top without login: status = %d, want 401", res.StatusCode)
	}

	// login の URL でログインできる。
	r := run(t, env, "login")
	if r.code != 0 {
		t.Fatalf("login: code = %d stderr = %s", r.code, r.stderr)
	}
	loginURL := strings.TrimSpace(r.stdout)
	prefix := "http://passui.localhost:" + port + "/login?token="
	if !strings.HasPrefix(loginURL, prefix) || len(loginURL) < len(prefix)+32 {
		t.Fatalf("login URL = %q, want %s<token>", loginURL, prefix)
	}
	token := strings.TrimPrefix(loginURL, prefix)
	cookie := loginVia(t, port, loginURL)
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+port+"/", nil)
	req.Host = "passui.localhost:" + port
	req.AddCookie(cookie)
	res, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("top with session: status = %d, want 200", res.StatusCode)
	}
	// ツリーは PASSWORD_STORE_DIR のストアから作る。
	if !strings.Contains(string(body), `data-entry="from-store-dir"`) {
		t.Errorf("top does not show the store in PASSWORD_STORE_DIR:\n%s", body)
	}

	// パーミッション
	dir := filepath.Join(runtimeDir, "passui")
	assertPerm(t, dir, 0o700)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no files in %s: %v", dir, err)
	}
	for _, e := range entries {
		assertPerm(t, filepath.Join(dir, e.Name()), 0o600)
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("serve code = %d, want 0", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("serve did not stop after cancel:\n%s", stderr.String())
	}

	// 正常に止まるとトークンが消え、login は「起動していない」と出す。
	entries, _ = os.ReadDir(dir)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if strings.Contains(string(b), token) {
			t.Errorf("token remains in %s after stop", e.Name())
		}
	}
	if r := run(t, env, "login"); r.code != 1 || !strings.Contains(r.stderr, "起動していない") {
		t.Errorf("login after stop: code=%d stdout=%q stderr=%q", r.code, r.stdout, r.stderr)
	}

	logs := stderr.String()
	if strings.Contains(logs, token) || strings.Contains(logs, cookie.Value) {
		t.Errorf("token or session cookie leaked into logs:\n%s", logs)
	}
}

func TestServeKeepsAnotherInstancesTokenWhenPortInUse(t *testing.T) {
	runtimeDir := t.TempDir()
	dir := filepath.Join(runtimeDir, "passui")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"token": "other-token\n", "url": "http://passui.localhost:3560/\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)

	if r := run(t, []string{"PATH=" + fakePassDir(t), "PASSUI_PORT=" + port, "XDG_RUNTIME_DIR=" + runtimeDir, homeEnv(t)}, "serve"); r.code != 1 {
		t.Fatalf("serve: code = %d, want 1", r.code)
	}

	r := run(t, []string{"XDG_RUNTIME_DIR=" + runtimeDir}, "login")
	if strings.TrimSpace(r.stdout) != "http://passui.localhost:3560/login?token=other-token" {
		t.Errorf("login = %q, want the other instance's URL", r.stdout)
	}
}

func loginVia(t *testing.T, port, loginURL string) *http.Cookie {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, strings.Replace(loginURL, "passui.localhost", "127.0.0.1", 1), nil)
	req.Host = "passui.localhost:" + port
	client := &http.Client{Transport: testClient.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: status = %d, want 303", res.StatusCode)
	}
	for _, c := range res.Cookies() {
		if c.Name == "passui_session" {
			return c
		}
	}
	t.Fatal("login: no session cookie")
	return nil
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != want {
		t.Errorf("%s: perm = %o, want %o", path, got, want)
	}
}
