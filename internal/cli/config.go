package cli

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DefaultPort は PASSUI_PORT を省いたときのポート。
const DefaultPort = 3560

// knownVars は passui が読む PASSUI_* の一覧。これ以外の PASSUI_* は警告する。
// 設定を足すときはここにも足すこと。
var knownVars = []string{"PASSUI_PORT", "PASSUI_LOG_LEVEL"}

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
