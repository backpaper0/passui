package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// NewSystem は本物の OS を操作する System を返す。env はシェルの環境変数で、
// 子プロセス（systemctl、loginctl）にもこれを渡す。
func NewSystem(env map[string]string) System {
	return osSystem{env: env}
}

type osSystem struct {
	env map[string]string
}

func (s osSystem) run(ctx context.Context, name string, args ...string) (string, error) {
	path, err := s.lookPath(name)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = s.environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("%w: %s", err, msg)
		}
		return out, err
	}
	return out, nil
}

func (s osSystem) environ() []string {
	env := make([]string, 0, len(s.env))
	for k, v := range s.env {
		env = append(env, k+"="+v)
	}
	return env
}

func (s osSystem) CheckUserManager(ctx context.Context) error {
	// ユーザーマネージャのバスに繋がらなければ失敗する。
	_, err := s.run(ctx, "systemctl", "--user", "show-environment")
	return err
}

func (s osSystem) IsWSL() bool {
	if s.env["WSL_DISTRO_NAME"] != "" {
		return true
	}
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

func (s osSystem) Linger(ctx context.Context) (bool, error) {
	out, err := s.run(ctx, "loginctl", "show-user", strconv.Itoa(os.Getuid()), "--property=Linger", "--value")
	if err != nil {
		return false, err
	}
	return out == "yes", nil
}

func (s osSystem) UnitEnabled(ctx context.Context, name string) (bool, error) {
	// is-enabled は enabled でないとき 0 以外で終わるので、出力で判断する。
	out, err := s.run(ctx, "systemctl", "--user", "is-enabled", name)
	if out != "" {
		return out == "enabled", nil
	}
	return false, err
}

func (s osSystem) Systemctl(ctx context.Context, args ...string) error {
	_, err := s.run(ctx, "systemctl", append([]string{"--user"}, args...)...)
	return err
}

func (s osSystem) UnitDir() (string, error) {
	if d := s.env["XDG_CONFIG_HOME"]; d != "" {
		return filepath.Join(d, "systemd", "user"), nil
	}
	if h := s.env["HOME"]; h != "" {
		return filepath.Join(h, ".config", "systemd", "user"), nil
	}
	return "", errors.New("HOME が設定されていません")
}

func (s osSystem) LookPass() (string, error) { return s.lookPath("pass") }

func (s osSystem) Executable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

func (s osSystem) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// lookPath は env の PATH から実行可能な name を探す（os.Getenv の PATH は見ない）。
func (s osSystem) lookPath(name string) (string, error) {
	for _, dir := range filepath.SplitList(s.env["PATH"]) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s が PATH に見つかりません", name)
}
