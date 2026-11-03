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
	"slices"
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
// pass ls（tree -l）と同じくシンボリックリンクのディレクトリもたどるが、祖先を指す
// リンク（循環）はたどらない。リンク先がないリンクは無視する。
// ストアのディレクトリがなければ空を返す。
func (p *Pass) Entries(ctx context.Context) ([]string, error) {
	var names []string
	err := p.walk(ctx, func(dir, rel string, files []fs.DirEntry) {
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".gpg") {
				names = append(names, strings.TrimSuffix(path.Join(rel, f.Name()), ".gpg"))
			}
		}
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// walk はストアのディレクトリ（ルートを含む）を 1 つずつ visit に渡す。dir は実際の
// パス、rel はストアからの相対パス（ルートは ""）、files はその中の "." で始まらない
// 通常のファイル（シンボリックリンクはリンク先で判断する）。"." で始まるディレクトリには
// 入らない。シンボリックリンクのディレクトリもたどるが、祖先を指すリンク（循環）は
// たどらない。リンク先がないリンクは無視する。ストアのディレクトリがなければ何もしない。
func (p *Pass) walk(ctx context.Context, visit func(dir, rel string, files []fs.DirEntry)) error {
	root, err := os.Stat(p.cfg.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err == nil {
		err = p.walkDir(ctx, p.cfg.Dir, "", []fs.FileInfo{root}, visit)
	}
	if err != nil {
		return fmt.Errorf("walk store: %w", err)
	}
	return nil
}

// walkDir は walk の再帰。ancestors は dir とその祖先で、循環の検出に使う。
func (p *Pass) walkDir(ctx context.Context, dir, rel string, ancestors []fs.FileInfo, visit func(dir, rel string, files []fs.DirEntry)) error {
	des, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var files []fs.DirEntry
	type sub struct {
		dir, rel string
		fi       fs.FileInfo
	}
	var subs []sub
	for _, d := range des {
		if strings.HasPrefix(d.Name(), ".") {
			continue
		}
		file := filepath.Join(dir, d.Name())
		fi, err := os.Stat(file) // シンボリックリンクはリンク先を見る
		if err != nil {
			if absent(err) {
				continue // リンク先がない
			}
			return err
		}
		switch {
		case fi.IsDir():
			if !slices.ContainsFunc(ancestors, func(a fs.FileInfo) bool { return os.SameFile(a, fi) }) {
				subs = append(subs, sub{file, path.Join(rel, d.Name()), fi})
			}
		case fi.Mode().IsRegular():
			files = append(files, d)
		}
	}
	visit(dir, rel, files)
	for _, s := range subs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.walkDir(ctx, s.dir, s.rel, append(slices.Clip(ancestors), s.fi), visit); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Exists はエントリのファイル（name.gpg）があるかを返す。エントリの名前として
// 正しくない名前（ストアの外を指すなど）は、そういうエントリはないので false。
func (p *Pass) Exists(ctx context.Context, name string) (bool, error) {
	file, err := p.entryFile(name)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	fi, err := os.Stat(file)
	if absent(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat entry %q: %w", name, err)
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
	if absent(err) {
		return "", fmt.Errorf("hash %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("hash %q: %w", name, err)
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
	var out []byte
	err := p.decrypt(ctx, "show", name, func() (err error) {
		out, err = p.run(ctx, nil, "show", "--", name)
		return err
	})
	return out, err
}

// decrypt は、エントリ name の復号が要る pass の呼び出し call（閲覧、再生成）を、失敗の
// 分類と一緒に行う。呼ぶ前にエントリがなければ ErrNotFound で、call を呼ばない。
// call が失敗したら、エントリが消えていれば ErrNotFound、エントリの宛先の鍵が
// gpg-agent にキャッシュされていなければ ErrLocked、それ以外は call のエラーを返す
// （stderr の文言は見ない）。op はエラー文に入れる操作の名前。
func (p *Pass) decrypt(ctx context.Context, op, name string, call func() error) error {
	if ok, err := p.Exists(ctx, name); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%s %q: %w", op, name, ErrNotFound)
	}
	err := call()
	if err == nil {
		return nil
	}
	if ok, _ := p.Exists(ctx, name); !ok {
		return fmt.Errorf("%s %q: %w", op, name, ErrNotFound)
	}
	if locked, lerr := p.locked(ctx, path.Dir(name)); lerr != nil {
		p.logger.WarnContext(ctx, "cannot get lock status", "err", lerr)
	} else if locked {
		return fmt.Errorf("%s %q: %w", op, name, ErrLocked)
	}
	return err
}
