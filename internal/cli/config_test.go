package cli_test

import (
	"errors"
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

// PASSUI_BACKUP_DIR はもう読まない。残っていても、知らない PASSUI_* として警告されるだけで、
// 絶対パスでなくても起動は止まらず、そのディレクトリも以前の既定の場所も作らない。
func TestServeWarnsAboutLeftoverBackupDir(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "sync", "passui")
	for _, v := range []string{dir, "relative"} {
		logs := serveUntilListen(t, "HOME="+home, "PASSUI_BACKUP_DIR="+v)

		if !strings.Contains(logs, `level=WARN msg="unknown environment variable" name=PASSUI_BACKUP_DIR`) {
			t.Errorf("PASSUI_BACKUP_DIR=%s is not warned as unknown:\n%s", v, logs)
		}
		if strings.Contains(logs, "invalid configuration") {
			t.Errorf("PASSUI_BACKUP_DIR=%s is still validated:\n%s", v, logs)
		}
	}
	for _, p := range []string{filepath.Dir(dir), filepath.Join(home, ".local")} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("serve created %s: %v", p, err)
		}
	}
}
