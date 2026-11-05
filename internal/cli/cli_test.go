package cli_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/backpaper0/passui/internal/cli"
)

type result struct {
	code           int
	stdout, stderr string
}

// run は cli.Run を同期で実行する。サーバーが起動してしまうケースに備えて期限を付ける。
func run(t *testing.T, env []string, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	code := cli.Run(ctx, args, env, &stdout, &stderr)
	if ctx.Err() != nil {
		t.Fatalf("Run did not return by itself (stderr: %s)", stderr.String())
	}
	return result{code, stdout.String(), stderr.String()}
}

// fakePassDir は実行可能な pass だけを置いたディレクトリを作る。
func fakePassDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pass"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestNoArgsPrintsHelpAndDoesNotServe(t *testing.T) {
	r := run(t, nil)

	if r.code != 0 {
		t.Errorf("code = %d, want 0", r.code)
	}
	for _, sub := range []string{"serve", "version"} {
		if !strings.Contains(r.stdout, sub) {
			t.Errorf("help lacks %q:\n%s", sub, r.stdout)
		}
	}
}

func TestUnknownSubcommandIsError(t *testing.T) {
	r := run(t, nil, "bogus")

	if r.code == 0 {
		t.Errorf("code = 0, want non-zero")
	}
	if !strings.Contains(r.stderr, "bogus") {
		t.Errorf("stderr does not mention the subcommand:\n%s", r.stderr)
	}
}

func TestVersion(t *testing.T) {
	old := cli.Version
	cli.Version = "1.2.3"
	t.Cleanup(func() { cli.Version = old })

	r := run(t, nil, "version")

	if r.code != 0 || strings.TrimSpace(r.stdout) != "passui 1.2.3" {
		t.Errorf("got code=%d stdout=%q, want passui 1.2.3", r.code, r.stdout)
	}
}

func TestServeRejectsInvalidConfig(t *testing.T) {
	path := "PATH=" + fakePassDir(t)
	for _, tc := range []struct {
		env  string
		want string
	}{
		{"PASSUI_PORT=abc", "PASSUI_PORT"},
		{"PASSUI_PORT=0", "PASSUI_PORT"},
		{"PASSUI_PORT=65536", "PASSUI_PORT"},
		{"PASSUI_PORT=", "PASSUI_PORT"},
		{"PASSUI_LOG_LEVEL=trace", "PASSUI_LOG_LEVEL"},
		{"PASSUI_BACKUP_DIR=backup", "PASSUI_BACKUP_DIR"},
		{"PASSUI_BACKUP_DIR=", "PASSUI_BACKUP_DIR"},
	} {
		r := run(t, []string{path, tc.env}, "serve")
		if r.code != 1 {
			t.Errorf("%s: code = %d, want 1", tc.env, r.code)
		}
		if !strings.Contains(r.stderr, `level=ERROR msg="invalid configuration"`) || !strings.Contains(r.stderr, tc.want) {
			t.Errorf("%s: stderr lacks an error about %s:\n%s", tc.env, tc.want, r.stderr)
		}
	}
}

func TestServeFailsWithoutPass(t *testing.T) {
	r := run(t, []string{"PATH=" + t.TempDir()}, "serve")

	if r.code != 1 {
		t.Errorf("code = %d, want 1", r.code)
	}
	if !strings.Contains(r.stderr, "level=ERROR") || !strings.Contains(r.stderr, "pass") {
		t.Errorf("stderr lacks an error about pass:\n%s", r.stderr)
	}
}

func TestServeFailsWhenPortInUse(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)

	r := run(t, []string{"PATH=" + fakePassDir(t), "PASSUI_PORT=" + port, runtimeDirEnv(t), homeEnv(t)}, "serve")

	if r.code != 1 {
		t.Errorf("code = %d, want 1", r.code)
	}
	if !strings.Contains(r.stderr, "level=ERROR") || !strings.Contains(r.stderr, port) {
		t.Errorf("stderr lacks an error about the port:\n%s", r.stderr)
	}
}

func TestServeListensOnLoopbackUntilCancelled(t *testing.T) {
	port := strconv.Itoa(freePort(t))
	env := []string{"PATH=" + fakePassDir(t), "PASSUI_PORT=" + port, "PASSUI_TYPO=1", runtimeDirEnv(t), homeEnv(t)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stderr := &lockedBuffer{}
	done := make(chan int, 1)
	go func() { done <- cli.Run(ctx, []string{"serve"}, env, io.Discard, stderr) }()

	res := getWithRetry(t, "http://127.0.0.1:"+port+"/", "passui.localhost:"+port)
	res.Body.Close()
	// 応答が返れば待ち受けている。ログインしていないので 401 になる。
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", res.StatusCode)
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("code = %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop after cancel")
	}

	logs := stderr.String()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "PASSUI_TYPO") {
		t.Errorf("no warning about unknown PASSUI_TYPO:\n%s", logs)
	}
	if !strings.Contains(logs, "127.0.0.1:"+port) {
		t.Errorf("logs lack the listen address:\n%s", logs)
	}
	if strings.Contains(logs, "time=") {
		t.Errorf("logs contain timestamps:\n%s", logs)
	}
}

// ブラウザは keep-alive の接続を開いたままにし、先回りで接続だけを張ることもある。
// そうした接続が残っていても、serve は止まり、正常終了（0）になる。
func TestServeStopsNormallyWithIdleConnections(t *testing.T) {
	port := strconv.Itoa(freePort(t))
	env := []string{"PATH=" + fakePassDir(t), "PASSUI_PORT=" + port, runtimeDirEnv(t), homeEnv(t)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stderr := &lockedBuffer{}
	done := make(chan int, 1)
	go func() { done <- cli.Run(ctx, []string{"serve"}, env, io.Discard, stderr) }()
	getWithRetry(t, "http://127.0.0.1:"+port+"/", "passui.localhost:"+port).Body.Close()

	// 応答を読み終えて keep-alive で残る接続。
	keepAlive := &http.Client{Transport: &http.Transport{}}
	defer keepAlive.CloseIdleConnections()
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+port+"/", nil)
	req.Host = "passui.localhost:" + port
	res, err := keepAlive.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	// 接続しただけで、リクエストを送らない接続。
	conn, err := net.Dial("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(100 * time.Millisecond) // サーバーが接続を受け付けるのを待つ

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("code = %d, want 0:\n%s", code, stderr.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("serve did not stop after cancel:\n%s", stderr.String())
	}
}

func TestServeDefaultsToPort3560(t *testing.T) {
	// 既定ポートを実際に掴むのは避け、3560 が使用中のときのエラーで確かめる。
	l, err := net.Listen("tcp", "127.0.0.1:3560")
	if err != nil {
		t.Skipf("port 3560 is not available for this test: %v", err)
	}
	defer l.Close()

	r := run(t, []string{"PATH=" + fakePassDir(t), runtimeDirEnv(t), homeEnv(t)}, "serve")

	if r.code != 1 || !strings.Contains(r.stderr, "127.0.0.1:3560") {
		t.Errorf("code=%d stderr=%s, want an error about 127.0.0.1:3560", r.code, r.stderr)
	}
}

// testClient は接続を使い回さない。使い回すと Transport が使われない接続を先に張ることがあり、
// その接続が serve の停止を 5 秒待たせる（StateNew の接続は 5 秒たつまで idle とみなされず、
// 待つ期限が来てから切られる）。テストを遅くしないため。
var testClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

func getWithRetry(t *testing.T, url, host string) *http.Response {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Host = host
		res, err := testClient.Do(req)
		if err == nil {
			return res
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not come up: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
