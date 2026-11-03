package service_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/service"
)

// fakeSystem は service.System のフェイク。systemctl の呼び出しを記録し、
// unit の置き場所には一時ディレクトリを使う。
type fakeSystem struct {
	unitDir     string
	files       map[string]bool // Exists が true を返すパス
	executable  string
	passPath    string // 空なら pass が見つからない
	noSystemd   bool
	wsl         bool
	linger      bool
	agentSocket bool
	calls       []string
	// urls はログイン URL のファイルの代わり。onRestart が nil なら、restart のたびに
	// 新しい serve が新しいトークンで書いたことにする。
	urls      *loginURLs
	restarts  int
	onRestart func() // restart のときに呼ぶ（serve の起動の代わり）
}

func newFakeSystem(t *testing.T) *fakeSystem {
	t.Helper()
	return &fakeSystem{
		unitDir:     filepath.Join(t.TempDir(), "systemd", "user"),
		files:       map[string]bool{},
		executable:  "/home/alice/.local/share/mise/installs/passui/1.2.3/passui",
		passPath:    "/usr/bin/pass",
		linger:      true,
		agentSocket: true,
		urls:        &loginURLs{},
	}
}

func (f *fakeSystem) CheckUserManager(context.Context) error {
	if f.noSystemd {
		return errors.New("Failed to connect to bus: No medium found")
	}
	return nil
}

func (f *fakeSystem) IsWSL() bool { return f.wsl }

func (f *fakeSystem) Linger(context.Context) (bool, error) { return f.linger, nil }

func (f *fakeSystem) UnitEnabled(_ context.Context, name string) (bool, error) {
	if name != "gpg-agent.socket" {
		return false, errors.New("unexpected unit " + name)
	}
	return f.agentSocket, nil
}

func (f *fakeSystem) Systemctl(_ context.Context, args ...string) error {
	f.calls = append(f.calls, strings.Join(args, " "))
	if len(args) > 0 && args[0] == "restart" {
		if f.onRestart != nil {
			f.onRestart()
		} else {
			f.restarts++
			f.urls.set(fmt.Sprintf("http://passui.localhost:3560/login?token=t%d", f.restarts))
		}
	}
	return nil
}

func (f *fakeSystem) UnitDir() (string, error) { return f.unitDir, nil }

func (f *fakeSystem) LookPass() (string, error) {
	if f.passPath == "" {
		return "", errors.New("pass が PATH に見つかりません")
	}
	return f.passPath, nil
}

func (f *fakeSystem) Executable() (string, error) { return f.executable, nil }

func (f *fakeSystem) Exists(path string) bool { return f.files[path] }

// unit は書き出された unit を読む。なければ空文字列。
func (f *fakeSystem) unit(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.unitDir, "passui.service"))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var _ service.System = (*fakeSystem)(nil)
