package cli

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/backpaper0/passui/internal/store"
)

// DefaultPort は PASSUI_PORT を省いたときのポート。
const DefaultPort = 3560

// knownVars は passui が読む PASSUI_* の一覧。これ以外の PASSUI_* は警告する。
// 設定を足すときはここにも足すこと。
var knownVars = []string{"PASSUI_PORT", "PASSUI_LOG_LEVEL", "PASSUI_BACKUP_DIR"}

// config は serve の設定。値はすべて環境変数から読む（フラグは持たない）。
type config struct {
	Port     int
	LogLevel slog.Level
}

// environ は "KEY=VALUE" の一覧を引けるようにしたもの。後に出た値が勝つ。
type environ map[string]string

func parseEnviron(env []string) environ {
	m := environ{}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok {
			m[k] = v
		}
	}
	return m
}

func (e environ) lookup(key string) (string, bool) {
	v, ok := e[key]
	return v, ok
}

// loadConfig は環境変数から設定を読む。知らない PASSUI_* は警告として返す。
func loadConfig(env environ) (config, []string, error) {
	cfg := config{Port: DefaultPort, LogLevel: slog.LevelInfo}

	var warnings []string
	for k := range env {
		if strings.HasPrefix(k, "PASSUI_") && !contains(knownVars, k) {
			warnings = append(warnings, k)
		}
	}
	sort.Strings(warnings)

	if v, ok := env.lookup("PASSUI_PORT"); ok {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			return config{}, warnings, fmt.Errorf("PASSUI_PORT は 1〜65535 の整数で指定してください: %q", v)
		}
		cfg.Port = p
	}

	if v, ok := env.lookup("PASSUI_LOG_LEVEL"); ok {
		switch v {
		case "info":
			cfg.LogLevel = slog.LevelInfo
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		default:
			return config{}, warnings, fmt.Errorf("PASSUI_LOG_LEVEL は info か debug で指定してください: %q", v)
		}
	}

	// バックアップ先は絶対パスかだけ確かめる。存在は確かめず、作りもしない（ホスト側の
	// フォルダが後から現れることもある）。値は backupDir で使う。
	if v, ok := env.lookup("PASSUI_BACKUP_DIR"); ok && !filepath.IsAbs(v) {
		return config{}, warnings, fmt.Errorf("PASSUI_BACKUP_DIR は絶対パスで指定してください: %q", v)
	}

	return cfg, warnings, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// passConfig はストア バックエンドの設定を env から組み立てる。ストアは
// PASSWORD_STORE_DIR（既定は ~/.password-store）、GNUPGHOME（既定は ~/.gnupg）。
// 子プロセスには env をそのまま土台として渡す。
func passConfig(env environ, passPath string) (store.PassConfig, error) {
	home, _ := env.lookup("HOME")
	dir, _ := env.lookup("PASSWORD_STORE_DIR")
	gnupg, _ := env.lookup("GNUPGHOME")
	if (dir == "" || gnupg == "") && home == "" {
		return store.PassConfig{}, fmt.Errorf("HOME が空です（PASSWORD_STORE_DIR と GNUPGHOME の既定値を決められません）")
	}
	if dir == "" {
		dir = filepath.Join(home, ".password-store")
	}
	if gnupg == "" {
		gnupg = filepath.Join(home, ".gnupg")
	}
	list := make([]string, 0, len(env))
	for k, v := range env {
		list = append(list, k+"="+v)
	}
	sort.Strings(list)
	return store.PassConfig{Pass: passPath, Dir: dir, GnupgHome: gnupg, Env: list}, nil
}

// backupDir はバックアップ先を env から決める。PASSUI_BACKUP_DIR があればそれを使い、
// create（なければ作る）を false にする（絶対パスであることは loadConfig で確かめ済み）。
// なければ既定の場所 ${XDG_DATA_HOME:-$HOME/.local/share}/passui/backup を使い、create を
// true にする。XDG_DATA_HOME が絶対パスでなければ（XDG の仕様どおり）無視する。
// ここではディレクトリの存在を確かめず、作りもしない（バックアップを作るときに作る）。
func backupDir(env environ) (dir string, create bool, err error) {
	if v, ok := env.lookup("PASSUI_BACKUP_DIR"); ok {
		return v, false, nil
	}
	base, _ := env.lookup("XDG_DATA_HOME")
	if !filepath.IsAbs(base) {
		home, _ := env.lookup("HOME")
		if home == "" {
			return "", false, fmt.Errorf("HOME が空です（バックアップ先の既定値を決められません）")
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "passui", "backup"), true, nil
}

// lookPath は env の PATH から実行可能な name を探す（os.Getenv の PATH は見ない）。
func lookPath(env environ, name string) (string, error) {
	path, _ := env.lookup("PATH")
	for _, dir := range filepath.SplitList(path) {
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
