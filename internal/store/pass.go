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
	"sync"
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
	// PASSWORD_STORE_DIR、GNUPGHOME、LC_ALL は呼び出しのたびに Pass が上書きする。
	// PASSWORD_STORE_GPG_OPTS は、ここの値の後ろに "--batch --pinentry-mode error" を足す。
	Env []string
}

// Pass は本物のストア バックエンド。一覧・存在・ハッシュはストアのファイルを直接読み、
// 書き込みと復号は pass を子プロセスで呼ぶ。
type Pass struct {
	cfg    PassConfig
	logger *slog.Logger
	// writeMu は書き込みの mutex（write.go の write）。serve は Pass を 1 つだけ作るので、
	// プロセス全体で 1 つになる。
	writeMu sync.Mutex
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
	var userOpts string
	for _, kv := range p.cfg.Env {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "PASSWORD_STORE_GPG_OPTS":
			userOpts = v
			continue
		case "PASSWORD_STORE_DIR", "GNUPGHOME", "LC_ALL":
			continue
		}
		env = append(env, kv)
	}
	// pinentry を出さない。ロック中なら復号は失敗する（判定は KEYINFO で行う）。
	// ユーザーの PASSWORD_STORE_GPG_OPTS は互換のため残し、後ろに足す（gpg は後に
	// 書いたオプションを優先するので、--pinentry-mode error が効く）。
	gpgOpts := "--batch --pinentry-mode error"
	if userOpts = strings.TrimSpace(userOpts); userOpts != "" {
		gpgOpts = userOpts + " " + gpgOpts
	}
	return append(env,
		"PASSWORD_STORE_DIR="+p.cfg.Dir,
		"GNUPGHOME="+p.cfg.GnupgHome,
		"PASSWORD_STORE_GPG_OPTS="+gpgOpts,
		"LC_ALL=C",
	)
}

// run は pass を args で呼び、stdout を返す。stdin は子プロセスにだけ渡す。
// stdin と stdout は（シークレットを含みうるので）どこにも出さない。stderr は
// キャプチャし、失敗したときだけ warn でログに出す。返すエラーに stderr は入れない。
func (p *Pass) run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	return p.exec(ctx, p.cfg.Pass, stdin, args...)
}

// exec は prog（pass、gpg、gpg-connect-agent）を run と同じ環境と同じログの扱いで呼ぶ。
func (p *Pass) exec(ctx context.Context, prog string, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, prog, args...)
	cmd.Env = p.environ()
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// ログのメッセージはコマンド名（"pass"、"pass failed"、"gpg failed" など）。
	name := filepath.Base(prog)
	start := time.Now()
	err := cmd.Run()
	p.logger.DebugContext(ctx, name, "args", args, "duration", time.Since(start))
	if err != nil {
		p.logger.WarnContext(ctx, name+" failed", "args", args, "err", err, "stderr", stderr.String())
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}

// Show は pass show でエントリを復号し、平文を返す。復号に失敗したら、エントリが
// 消えていれば ErrNotFound、エントリの宛先の鍵が gpg-agent にキャッシュされていなければ
// ErrLocked、それ以外はそのほかの失敗を返す（stderr の文言は見ない）。
func (p *Pass) Show(ctx context.Context, name string) ([]byte, error) {
	if ok, err := p.Exists(ctx, name); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("show %s: %w", name, ErrNotFound)
	}
	out, err := p.run(ctx, nil, "show", "--", name)
	if err == nil {
		return out, nil
	}
	if ok, _ := p.Exists(ctx, name); !ok {
		return nil, fmt.Errorf("show %s: %w", name, ErrNotFound)
	}
	if locked, lerr := p.locked(ctx, path.Dir(name)); lerr == nil && locked {
		return nil, fmt.Errorf("show %s: %w", name, ErrLocked)
	} else if lerr != nil {
		p.logger.WarnContext(ctx, "cannot get lock status", "err", lerr)
	}
	return nil, err
}

// Locked はストアのルートの .gpg-id の鍵が、どれも gpg-agent にキャッシュされて
// いない（ロック中）かを返す。
func (p *Pass) Locked(ctx context.Context) (bool, error) {
	return p.locked(ctx, ".")
}

// locked は、ストアの中のディレクトリ dir（"." はルート）に効く .gpg-id の鍵について、
// 暗号化用の秘密鍵の keygrip を gpg-agent の KEYINFO に問い合わせ、どれも cached で
// なければ true を返す。.gpg-id が読めなければ、すべての秘密鍵を見る。
func (p *Pass) locked(ctx context.Context, dir string) (bool, error) {
	grips, err := p.keygrips(ctx, p.recipients(dir))
	if err != nil {
		return false, err
	}
	if len(grips) == 0 {
		return false, errors.New("no secret key for the store")
	}
	args := make([]string, 0, len(grips)+1)
	for _, g := range grips {
		args = append(args, "KEYINFO "+g)
	}
	out, err := p.exec(ctx, p.tool("gpg-connect-agent"), nil, append(args, "/bye")...)
	if err != nil {
		return false, err
	}
	// "S KEYINFO <keygrip> <type> <serialno> <idstr> <cached> ..."。cached は "1" か "-"。
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) > 6 && f[0] == "S" && f[1] == "KEYINFO" && f[6] == "1" {
			return false, nil
		}
	}
	return true, nil
}

// recipients は dir に効く .gpg-id（dir から上にたどって最初に見つかるもの）の鍵を返す。
// pass と同じく、"#" より後ろと空行は無視する。見つからなければ nil。
func (p *Pass) recipients(dir string) []string {
	for {
		b, err := os.ReadFile(filepath.Join(p.cfg.Dir, filepath.FromSlash(dir), ".gpg-id"))
		if err == nil {
			var ids []string
			for _, line := range strings.Split(string(b), "\n") {
				line, _, _ = strings.Cut(line, "#")
				if line = strings.TrimSpace(line); line != "" {
					ids = append(ids, line)
				}
			}
			return ids
		}
		if dir == "." || dir == "/" || dir == "" {
			return nil
		}
		dir = path.Dir(dir)
	}
}

// keygrips は ids（空ならすべて）の秘密鍵のうち、暗号化に使う鍵（主鍵と副鍵）の
// keygrip を返す。
func (p *Pass) keygrips(ctx context.Context, ids []string) ([]string, error) {
	args := append([]string{"--batch", "--with-colons", "--with-keygrip", "--list-secret-keys", "--"}, ids...)
	out, err := p.exec(ctx, p.tool("gpg"), nil, args...)
	if err != nil {
		return nil, err
	}
	var grips []string
	encrypt := false
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, ":")
		switch {
		case len(f) > 11 && (f[0] == "sec" || f[0] == "ssb"):
			encrypt = strings.Contains(f[11], "e")
		case len(f) > 9 && f[0] == "grp":
			if encrypt {
				grips = append(grips, f[9])
			}
			encrypt = false
		}
	}
	return grips, nil
}

// tool は gpg などの補助コマンドを、Env の PATH から探す。見つからなければ名前を
// そのまま返す（プロセスの PATH で探される）。
func (p *Pass) tool(name string) string {
	for i := len(p.cfg.Env) - 1; i >= 0; i-- {
		k, v, _ := strings.Cut(p.cfg.Env[i], "=")
		if k != "PATH" {
			continue
		}
		for _, dir := range filepath.SplitList(v) {
			file := filepath.Join(dir, name)
			if fi, err := os.Stat(file); dir != "" && err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
				return file
			}
		}
		break
	}
	return name
}
