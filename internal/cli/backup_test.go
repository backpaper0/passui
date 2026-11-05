package cli_test

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// serveUntilListen は serve を、ポートが使用中で待ち受けに失敗するところまで動かし、
// stderr（ログ）を返す。設定の解決は待ち受けの前に済むので、それをログで確かめるのに使う。
func serveUntilListen(t *testing.T, env ...string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	env = append(env, "PATH="+fakePassDir(t), "PASSUI_PORT="+port, runtimeDirEnv(t))
	r := run(t, env, "serve")
	if r.code != 1 || !strings.Contains(r.stderr, "cannot listen") {
		t.Fatalf("serve did not stop at listening: code=%d\n%s", r.code, r.stderr)
	}
	return r.stderr
}

func TestServeUsesDefaultBackupDirectory(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"no XDG_DATA_HOME", nil, filepath.Join(home, ".local", "share", "passui", "backup")},
		{"absolute XDG_DATA_HOME", []string{"XDG_DATA_HOME=" + xdg}, filepath.Join(xdg, "passui", "backup")},
		{"empty XDG_DATA_HOME", []string{"XDG_DATA_HOME="}, filepath.Join(home, ".local", "share", "passui", "backup")},
		{"relative XDG_DATA_HOME", []string{"XDG_DATA_HOME=data"}, filepath.Join(home, ".local", "share", "passui", "backup")},
	} {
		logs := serveUntilListen(t, append(tc.env, "HOME="+home)...)

		if !strings.Contains(logs, "msg=backup dir="+tc.want+" ") {
			t.Errorf("%s: logs lack the backup directory %s:\n%s", tc.name, tc.want, logs)
		}
	}
	// 起動時にはバックアップ先を作らない（押したときに作る）。
	for _, base := range []string{home, xdg} {
		if _, err := os.Stat(filepath.Join(base, ".local")); err == nil {
			t.Errorf("serve created a directory under %s", base)
		}
		if _, err := os.Stat(filepath.Join(base, "passui")); err == nil {
			t.Errorf("serve created a directory under %s", base)
		}
	}
}
