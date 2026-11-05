// Package backup はバックアップ（ストアなどをまとめた 1 つの tar.gz）をバックアップ先に置く。
// 入口は Write だけ。ストアにも gpg-agent にも書き込まない。
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"text/template"
	"time"
)

// Options は Write の設定。
type Options struct {
	// StoreDir はストアのディレクトリ（PASSWORD_STORE_DIR）。
	StoreDir string
	// Dir はバックアップ先（絶対パス）。
	Dir string
	// CreateDir が true なら、バックアップ先がなければ 0700 で（親も含めて）作る。
	// 既定のバックアップ先を使うときだけ true にする。
	CreateDir bool
	// Now は作成日時。ファイル名と README.txt に使う（ゲストのローカル時刻にして使う）。
	Now time.Time
	// Version は passui のバージョン。README.txt に書く。
	Version string
	// Env は gpg を呼ぶための子プロセスの環境（"KEY=VALUE"。PASSWORD_STORE_KEY や
	// GNUPGHOME を含む）。
	Env []string
}

// Summary は Write の結果の要約。ログに使う（画面への応答には Path だけを使う）。
type Summary struct {
	// Path は置いたバックアップの完全なパス。
	Path string
	// SkippedSpecialFiles は、通常ファイル・ディレクトリ・シンボリックリンクのどれでもない
	// ため、ストアから飛ばしたファイル（ソケットなど）の数。
	SkippedSpecialFiles int
	// Keys はストアの鍵（主鍵の指紋）。ログには数を出し、指紋は debug のときだけ出す。
	Keys []string
	// SecretKeys は、秘密鍵（.key）を入れたストアの鍵の数。keygrip はログに出さない。
	SecretKeys int
}

// Write が返す番兵エラー。*fs.PathError に包んで返すので、呼び出し側は errors.Is で
// 出し分け、errors.As で *fs.PathError の Path（問題のパス）を取り出せる。
var (
	// ErrNoDir: 「なければ作る」が偽で、バックアップ先がない。Path はバックアップ先。
	ErrNoDir = errors.New("backup directory does not exist")
	// ErrNotWritable: バックアップ先に書けない。Path はバックアップ先。
	ErrNotWritable = errors.New("backup directory is not writable")
	// ErrExists: 同じ名前のファイルがすでにある（上書きしない）。Path はそのファイル。
	ErrExists = errors.New("backup file already exists")
)

// mu はバックアップの作成をプロセス内で直列化する。
var mu sync.Mutex

//go:embed README.txt.tmpl
var readmeText string

var readmeTemplate = template.Must(template.New("README.txt").Parse(readmeText))

// Write はその時点のストアを 1 つの tar.gz にまとめ、バックアップ先に
// passui-backup-YYYYMMDD-HHMMSS.tar.gz（0600）として置く。
//
// 同じディレクトリの .passui-backup-YYYYMMDD-HHMMSS.tar.gz.partial に書いて fsync し、
// 同じ名前のファイルがないことを確かめてから rename する。失敗したら一時ファイルを消す。
func Write(ctx context.Context, opts Options) (Summary, error) {
	mu.Lock()
	defer mu.Unlock()

	if !filepath.IsAbs(opts.Dir) {
		return Summary{}, fmt.Errorf("backup directory is not an absolute path: %q", opts.Dir)
	}
	if opts.CreateDir {
		if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
			if isNotWritable(err) {
				return Summary{}, notWritable(opts.Dir, err)
			}
			return Summary{}, fmt.Errorf("create backup directory: %w", err)
		}
	} else if _, err := os.Stat(opts.Dir); errors.Is(err, fs.ErrNotExist) {
		return Summary{}, &fs.PathError{Op: "backup", Path: opts.Dir, Err: ErrNoDir}
	}

	now := opts.Now.Local()
	name := "passui-backup-" + now.Format("20060102-150405")
	final := filepath.Join(opts.Dir, name+".tar.gz")
	partial := filepath.Join(opts.Dir, "."+name+".tar.gz.partial")

	f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if isNotWritable(err) {
			return Summary{}, notWritable(opts.Dir, err)
		}
		return Summary{}, fmt.Errorf("create temporary file: %w", err)
	}
	// 正式な名前にできなかったら、一時ファイルを消す。
	renamed, closed := false, false
	defer func() {
		if !closed {
			f.Close()
		}
		if !renamed {
			os.Remove(partial)
		}
	}()

	got, err := writeArchive(ctx, f, name, now, opts)
	if err != nil {
		return Summary{}, err
	}
	if err := f.Sync(); err != nil {
		return Summary{}, fmt.Errorf("sync temporary file: %w", err)
	}
	closed = true
	if err := f.Close(); err != nil {
		return Summary{}, fmt.Errorf("close temporary file: %w", err)
	}
	// POSIX の rename は上書きするので、直前に同じ名前がないことを確かめる。
	if _, err := os.Lstat(final); err == nil {
		return Summary{}, &fs.PathError{Op: "backup", Path: final, Err: ErrExists}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Summary{}, err
	}
	if err := os.Rename(partial, final); err != nil {
		return Summary{}, fmt.Errorf("rename temporary file: %w", err)
	}
	renamed = true
	sum := Summary{Path: final, SkippedSpecialFiles: len(got.skipped)}
	for _, k := range got.keys {
		sum.Keys = append(sum.Keys, k.Fingerprint)
		if len(k.Secrets) > 0 {
			sum.SecretKeys++
		}
	}
	return sum, nil
}

// archived は writeArchive がアーカイブに入れたものの要約。
type archived struct {
	// keys は入れたストアの鍵。
	keys []storeKey
	// skipped はストアから飛ばした特殊ファイルの、アーカイブの中でのパス（top/ からの相対）。
	skipped []string
}

// isNotWritable は err が権限や読み取り専用のファイルシステムのせいで書けないことを表すか。
func isNotWritable(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS)
}

// notWritable は dir に書けないことを表す ErrNotWritable を、原因 cause をログ用に残して返す。
func notWritable(dir string, cause error) error {
	return &fs.PathError{Op: "backup", Path: dir, Err: fmt.Errorf("%w: %w", ErrNotWritable, cause)}
}

// writeArchive は w に tar.gz を書く。中身はすべて top/ の下に置く。
// ストアの鍵は、アーカイブを書き始める前に集める。
func writeArchive(ctx context.Context, w io.Writer, top string, now time.Time, opts Options) (archived, error) {
	g := newGPG(opts.Env)
	ids, err := keyIDs(ctx, opts.StoreDir, opts.Env)
	if err != nil {
		return archived{}, err
	}
	keys, err := collectKeys(ctx, g, ids)
	if err != nil {
		return archived{}, err
	}
	if err := collectSecretKeys(ctx, g, keys); err != nil {
		return archived{}, err
	}
	public, err := exportPublicKeys(ctx, g, keys)
	if err != nil {
		return archived{}, err
	}
	ownertrust, err := exportOwnertrust(ctx, g, keys)
	if err != nil {
		return archived{}, err
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeDir, Name: top + "/", Mode: 0o700, ModTime: now,
	}); err != nil {
		return archived{}, err
	}
	skipped, err := writeStore(ctx, tw, top, "store", opts.StoreDir)
	if err != nil {
		return archived{}, err
	}
	gnupg := path.Join(top, "gnupg")
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeDir, Name: gnupg + "/", Mode: 0o700, ModTime: now,
	}); err != nil {
		return archived{}, err
	}
	if err := writeBytes(tw, path.Join(gnupg, "public-keys.asc"), public, now); err != nil {
		return archived{}, err
	}
	if err := writeBytes(tw, path.Join(gnupg, "ownertrust.txt"), ownertrust, now); err != nil {
		return archived{}, err
	}
	if err := writeSecretKeys(tw, path.Join(gnupg, "private-keys-v1.d"), keys, now); err != nil {
		return archived{}, err
	}
	// 飛ばしたものを書くので、README.txt はストアの後に書く。
	if err := writeReadme(tw, top, readmeData{
		CreatedAt: now.Format("2006-01-02 15:04:05 -0700"),
		Version:   opts.Version,
		Keys:      keys,
		Skipped:   skipped,
	}, now); err != nil {
		return archived{}, err
	}

	if err := tw.Close(); err != nil {
		return archived{}, err
	}
	return archived{keys: keys, skipped: skipped}, gz.Close()
}

// writeSecretKeys は、ストアの鍵の .key を dir/（0700）の下に <keygrip>.key（0600）として
// 書く。.key が 1 つもなければ dir も作らない。
func writeSecretKeys(tw *tar.Writer, dir string, keys []storeKey, now time.Time) error {
	wroteDir := false
	for _, k := range keys {
		for _, s := range k.Secrets {
			if !wroteDir {
				if err := tw.WriteHeader(&tar.Header{
					Typeflag: tar.TypeDir, Name: dir + "/", Mode: 0o700, ModTime: now,
				}); err != nil {
					return err
				}
				wroteDir = true
			}
			if err := writeBytes(tw, path.Join(dir, s.Keygrip+".key"), s.data, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// readmeData は README.txt のテンプレートに渡す。
type readmeData struct {
	CreatedAt string
	Version   string
	// Keys はストアの鍵。
	Keys []storeKey
	// Skipped はストアから飛ばした特殊ファイルの、アーカイブの中でのパス（store/…）。
	Skipped []string
}

// MissingSecrets は、ゲストに秘密鍵のないストアの鍵があるか。
func (d readmeData) MissingSecrets() bool {
	return slices.ContainsFunc(d.Keys, func(k storeKey) bool { return len(k.Secrets) == 0 })
}

func writeReadme(tw *tar.Writer, top string, data readmeData, now time.Time) error {
	var buf bytes.Buffer
	if err := readmeTemplate.Execute(&buf, data); err != nil {
		return err
	}
	return writeBytes(tw, path.Join(top, "README.txt"), buf.Bytes(), now)
}

// writeBytes は b を name の通常ファイル（0600）として書く。
func writeBytes(tw *tar.Writer, name string, b []byte, now time.Time) error {
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: name, Mode: 0o600, Size: int64(len(b)), ModTime: now,
	}); err != nil {
		return err
	}
	_, err := tw.Write(b)
	return err
}

// writeStore はストアを丸ごと top/dir/ の下に書く。パーミッションは tar のヘッダーに保つ。
// シンボリックリンクはリンク先をたどらずにリンクのまま入れる。通常ファイル・ディレクトリ・
// シンボリックリンクのどれでもないもの（ソケットなど）は飛ばし、その dir/… の形のパスを返す。
func writeStore(ctx context.Context, tw *tar.Writer, top, dir, storeDir string) ([]string, error) {
	// ストアのディレクトリそのものがシンボリックリンクでも、その先を写す。
	root, err := filepath.EvalSymlinks(storeDir)
	if err != nil {
		return nil, err
	}
	var skipped []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		inner := path.Join(dir, filepath.ToSlash(rel))
		name := path.Join(top, inner)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			return writeHeader(tw, info, name+"/")
		case info.Mode().IsRegular():
			return writeFile(tw, info, name, p)
		case info.Mode()&fs.ModeSymlink != 0:
			// リンク先はたどらず、リンクのまま入れる。
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			h, err := tar.FileInfoHeader(info, target)
			if err != nil {
				return err
			}
			h.Name = name
			return tw.WriteHeader(h)
		}
		skipped = append(skipped, inner)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return skipped, nil
}

func writeHeader(tw *tar.Writer, info fs.FileInfo, name string) error {
	h, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	h.Name = name
	return tw.WriteHeader(h)
}

func writeFile(tw *tar.Writer, info fs.FileInfo, name, p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := writeHeader(tw, info, name); err != nil {
		return err
	}
	_, err = io.CopyN(tw, f, info.Size())
	return err
}
