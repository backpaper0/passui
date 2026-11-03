package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PassConfig は Pass の設定。
type PassConfig struct {
	// Pass は pass の実行ファイルのパス。
	Pass string
	// Dir はストアのディレクトリ（PASSWORD_STORE_DIR）。
	Dir string
	// GnupgHome は GNUPGHOME。
	GnupgHome string
	// Env は子プロセスに渡す環境（"KEY=VALUE"）の土台。PATH や HOME、
	// PASSWORD_STORE_GENERATED_LENGTH などはここから引き継ぐ。
	// PASSWORD_STORE_DIR、GNUPGHOME、PASSWORD_STORE_GPG_OPTS、LC_ALL は
	// 呼び出しのたびに Pass が上書きする。
	Env []string
}

// Pass は本物のストア バックエンド。一覧・存在・ハッシュはストアのファイルを直接読み、
// 書き込みと復号は pass を子プロセスで呼ぶ。
type Pass struct {
	cfg    PassConfig
	logger *slog.Logger
}

var _ Backend = (*Pass)(nil)

// NewPass は Pass を作る。子プロセスの stderr などは logger に出す。
func NewPass(cfg PassConfig, logger *slog.Logger) *Pass {
	return &Pass{cfg: cfg, logger: logger}
}

// Entries はストアのディレクトリを走査して、*.gpg のエントリを名前順に返す。
// "." で始まるファイルとディレクトリ（.git、.gpg-id、.extensions など）は見ない。
// ストアのディレクトリがなければ空を返す。
func (p *Pass) Entries(ctx context.Context) ([]string, error) {
	var names []string
	err := filepath.WalkDir(p.cfg.Dir, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			if file == p.cfg.Dir && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return err
		}
		if file == p.cfg.Dir {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".gpg") {
			return nil
		}
		rel, err := filepath.Rel(p.cfg.Dir, file)
		if err != nil {
			return err
		}
		names = append(names, strings.TrimSuffix(filepath.ToSlash(rel), ".gpg"))
		return ctx.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("walk store: %w", err)
	}
	sort.Strings(names)
	return names, nil
}

// Exists はエントリのファイル（name.gpg）があるかを返す。
func (p *Pass) Exists(ctx context.Context, name string) (bool, error) {
	file, err := p.entryFile(name)
	if err != nil {
		return false, nil
	}
	fi, err := os.Stat(file)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat entry: %w", err)
	}
	return fi.Mode().IsRegular(), nil
}

// Hash はエントリの暗号文の SHA-256 を 16 進で返す。なければ ErrNotFound。
func (p *Pass) Hash(ctx context.Context, name string) (string, error) {
	file, err := p.entryFile(name)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("hash %s: %w", name, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("read entry: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// entryFile はエントリ名をストアの中のファイルのパスにする。ストアの外を指す名前や、
// 正規形でない名前（"a//b"、"./a"、末尾の "/"）は ErrNotFound にする。
func (p *Pass) entryFile(name string) (string, error) {
	if name == "" || path.Clean(name) != name || !filepath.IsLocal(filepath.FromSlash(name)) {
		return "", fmt.Errorf("entry name %q: %w", name, ErrNotFound)
	}
	return filepath.Join(p.cfg.Dir, filepath.FromSlash(name)+".gpg"), nil
}

// environ は pass を呼ぶときの環境を組み立てる。呼び出しのたびに作る。
func (p *Pass) environ() []string {
	env := make([]string, 0, len(p.cfg.Env)+4)
	for _, kv := range p.cfg.Env {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "PASSWORD_STORE_DIR", "GNUPGHOME", "PASSWORD_STORE_GPG_OPTS", "LC_ALL":
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"PASSWORD_STORE_DIR="+p.cfg.Dir,
		"GNUPGHOME="+p.cfg.GnupgHome,
		// pinentry を出さない。ロック中なら復号は失敗する（判定は KEYINFO で行う）。
		"PASSWORD_STORE_GPG_OPTS=--batch --pinentry-mode error",
		"LC_ALL=C",
	)
}

// run は pass を args で呼び、stdout を返す。stdin は子プロセスにだけ渡す。
// stdin と stdout は（シークレットを含みうるので）どこにも出さない。stderr は
// キャプチャし、失敗したときだけ warn でログに出す。返すエラーに stderr は入れない。
func (p *Pass) run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, p.cfg.Pass, args...)
	cmd.Env = p.environ()
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	p.logger.DebugContext(ctx, "pass", "args", args, "duration", time.Since(start))
	if err != nil {
		p.logger.WarnContext(ctx, "pass failed", "args", args, "err", err, "stderr", stderr.String())
		return nil, fmt.Errorf("pass %s: %w", strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}
