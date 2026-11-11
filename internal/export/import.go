package export

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// ImportOptions は Import の設定。
type ImportOptions struct {
	// StoreDir はストアのディレクトリ（PASSWORD_STORE_DIR）。ないか空でなければならない。
	StoreDir string
	// Env は gpg を呼ぶための子プロセスの環境（"KEY=VALUE"。GNUPGHOME を含む）。
	Env []string
}

// ImportSummary は Import の結果の要約。ログに使う。
type ImportSummary struct {
	// Top はエクスポートファイルの最上位のディレクトリの名前。
	Top string
	// Keys は、ストアの鍵の公開鍵と ownertrust を取り込んだか（エクスポートファイルに
	// ストアの鍵があったか）。
	Keys bool
	// SecretKeys は置いた秘密鍵（.key）の数。keygrip はログに出さない。
	SecretKeys int
}

// ErrStoreNotEmpty は、ストアのディレクトリが空でないので、インポートしなかったことを表す。
var ErrStoreNotEmpty = errors.New("store is not empty")

// ErrSecretKeyExists は、エクスポートファイルの秘密鍵と同じ keygrip の .key がゲストに
// あるので、インポートしなかったことを表す（上書きもマージもしない）。Import は
// *SecretKeyExistsError で返す（errors.Is で ErrSecretKeyExists と見分けられる）。
var ErrSecretKeyExists = errors.New("secret key file already exists")

// SecretKeyExistsError は、どの鍵の .key がゲストにすでにあったかを持つ ErrSecretKeyExists。
type SecretKeyExistsError struct {
	// Keygrips は、ゲストにすでにあった .key の keygrip。画面に出すためのもので、
	// Error() には入れない（ログに keygrip を出さない）。
	Keygrips []string
}

func (e *SecretKeyExistsError) Error() string {
	return fmt.Sprintf("%s (%d keys)", ErrSecretKeyExists, len(e.Keygrips))
}

func (e *SecretKeyExistsError) Is(target error) bool { return target == ErrSecretKeyExists }

// secretKeyExists は names（<keygrip>.key）の *SecretKeyExistsError を作る。
func secretKeyExists(names ...string) error {
	grips := make([]string, len(names))
	for i, name := range names {
		grips[i] = strings.TrimSuffix(name, ".key")
	}
	return &SecretKeyExistsError{Keygrips: grips}
}

// InvalidExportFileError は、送られたものがエクスポートファイルとして受け付けられないことを表す。
// 何も戻していない。
type InvalidExportFileError struct {
	// Reason は理由。画面にもログにも出す。エントリの名前など、エクスポートファイルの中身は入れない。
	Reason string
	// Err は、読めなかったときの元のエラー（なければ nil）。送られてくる途中で切れた、
	// 大きすぎた、などを呼び出し側が見分けられるように包む。
	Err error
}

func (e *InvalidExportFileError) Error() string {
	if e.Err != nil {
		return "invalid export file: " + e.Reason + ": " + e.Err.Error()
	}
	return "invalid export file: " + e.Reason
}

func (e *InvalidExportFileError) Unwrap() error { return e.Err }

// unreadable は、r を tar.gz として読めなかったことを表す。
func unreadable(err error) error {
	return &InvalidExportFileError{Reason: "tar.gz として読めません", Err: err}
}

// exportFileReader は、読めなかったエラーを unreadable で包む（ファイルの中身を書いている
// 途中で読めなくなっても、書けなかったのと見分けられるように）。
type exportFileReader struct{ r io.Reader }

func (a exportFileReader) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = unreadable(err)
	}
	return n, err
}

func invalid(reason string) error {
	return &InvalidExportFileError{Reason: reason}
}

// インポートで展開するものの上限。取り違えたファイルや、gzip で膨らむものでディスクを
// 埋めないため。数字は README の「インポート」に書いたものと揃える。
const (
	// maxImportBytes は、展開する通常ファイルの大きさの合計の上限。
	maxImportBytes = 256 << 20
	// maxImportFiles は、エクスポートファイルに入っているファイル（ディレクトリ、
	// シンボリックリンクを含む）の数の上限。
	maxImportFiles = 50000
)

// StoreEmpty は、ストアのディレクトリ storeDir がないか空かを返す。インポートを受け付けるのは
// このときだけ。
func StoreEmpty(storeDir string) (bool, error) {
	des, err := os.ReadDir(storeDir)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return len(des) == 0, nil
}

// importMu はインポートをプロセス内で直列化する（空かを確かめてから書くまでの間に、
// 別のインポートが書かないように）。
var importMu sync.Mutex

// エクスポートファイルの中の配置（最上位のディレクトリからの相対パス）。
const (
	importStore      = "store"
	importGnupg      = "gnupg"
	importPublicKeys = "gnupg/public-keys.asc"
	importOwnertrust = "gnupg/ownertrust.txt"
	importSecretDir  = "gnupg/private-keys-v1.d"
	importReadme     = "README.txt"
)

// keyFileName は private-keys-v1.d の鍵ファイルの名前（<keygrip>.key）。
var keyFileName = regexp.MustCompile(`^[0-9A-Fa-f]{40}\.key$`)

// Import は、エクスポートファイル r から、ストアとストアの鍵を空のゲストに戻す。
//
// r は一時ディレクトリに展開し、配置を確かめてから戻す。パストラバーサルや、ストアの外を
// 指すシンボリックリンクがあれば *InvalidExportFileError を返し、何も戻さない。最上位の
// ディレクトリが 1 つだけあれば、名前は問わない（旧形式の passui-backup-* も受け付ける）。
//
// 戻す順番は、公開鍵の取り込み、.key を gpg-agent の鍵ファイルの場所に置く、ownertrust の
// 取り込み、ストアを置く、の順。ストアの鍵がない（public-keys.asc が空の）エクスポート
// ファイルでは、鍵の手順を飛ばす（gpg を呼ばない）。途中で失敗したら、置いた .key と
// ストアを消す。取り込んだ公開鍵と ownertrust は残す。一時ディレクトリは最後に消す。
//
// ストアのディレクトリが空でなければ ErrStoreNotEmpty を、同じ keygrip の .key がゲストに
// あれば *SecretKeyExistsError（ErrSecretKeyExists）を返し、何も書かない。パスフレーズは
// 扱わないので、戻した鍵で復号できるかは確かめない。呼び出しはプロセス内で直列化する。
func Import(ctx context.Context, r io.Reader, opts ImportOptions) (ImportSummary, error) {
	importMu.Lock()
	defer importMu.Unlock()
	if err := checkStoreEmpty(opts.StoreDir); err != nil {
		return ImportSummary{}, err
	}
	tmp, err := os.MkdirTemp("", "passui-import-")
	if err != nil {
		return ImportSummary{}, err
	}
	defer os.RemoveAll(tmp)

	l, err := extract(ctx, r, tmp)
	if err != nil {
		return ImportSummary{}, unwrapPath(err)
	}
	root := filepath.Join(tmp, l.top)
	summary := ImportSummary{Top: l.top, Keys: l.hasKeys, SecretKeys: len(l.secretKeys)}

	var placed []string // 置いた .key（失敗したら消す）
	ok := false
	defer func() {
		if !ok {
			for _, p := range placed {
				_ = os.Remove(p)
			}
		}
	}()
	if l.hasKeys {
		g := newGPG(opts.Env)
		keyDir, err := secretKeyDir(ctx, g)
		if err != nil {
			return ImportSummary{}, err
		}
		if err := checkNoSecretKeys(keyDir, l.secretKeys); err != nil {
			return ImportSummary{}, err
		}
		if _, err := g.run(ctx, "--import", "--", filepath.Join(root, filepath.FromSlash(importPublicKeys))); err != nil {
			return ImportSummary{}, err
		}
		if len(l.secretKeys) > 0 {
			placed, err = placeSecretKeys(filepath.Join(root, filepath.FromSlash(importSecretDir)), keyDir, l.secretKeys)
			if err != nil {
				return ImportSummary{}, err
			}
			// gpg-agent に鍵ファイルを読み直させる（手作業の手順と同じ）。
			if _, err := g.exec(ctx, "gpgconf", "--kill", "gpg-agent"); err != nil {
				return ImportSummary{}, err
			}
		}
		if _, err := g.run(ctx, "--import-ownertrust", "--", filepath.Join(root, filepath.FromSlash(importOwnertrust))); err != nil {
			return ImportSummary{}, err
		}
		// 取り込んだ ownertrust を trustdb に反映しておく（次の gpg が検査のメッセージを出さないように）。
		if _, err := g.run(ctx, "--check-trustdb"); err != nil {
			return ImportSummary{}, err
		}
	}
	// 鍵を戻している間に、ストアが作られていないかを確かめ直す。
	if err := checkStoreEmpty(opts.StoreDir); err != nil {
		return ImportSummary{}, err
	}
	if err := placeStore(ctx, filepath.Join(root, importStore), opts.StoreDir, l.modes); err != nil {
		return ImportSummary{}, fmt.Errorf("put the store in %s: %w", opts.StoreDir, unwrapPath(err))
	}
	ok = true
	return summary, nil
}

func checkStoreEmpty(storeDir string) error {
	empty, err := StoreEmpty(storeDir)
	if err != nil {
		return err
	}
	if !empty {
		return ErrStoreNotEmpty
	}
	return nil
}

// secretKeyDir は gpg-agent の鍵ファイルの場所（<homedir>/private-keys-v1.d）を返す。
func secretKeyDir(ctx context.Context, g *gpg) (string, error) {
	home, err := g.homedir(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "private-keys-v1.d"), nil
}

// checkNoSecretKeys は、keyDir に names（<keygrip>.key）のどれもないことを確かめる。
// あれば、あったものすべてを *SecretKeyExistsError で返す。
func checkNoSecretKeys(keyDir string, names []string) error {
	var found []string
	for _, name := range names {
		_, err := os.Lstat(filepath.Join(keyDir, name))
		if err == nil {
			found = append(found, name)
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("check a secret key file in %s: %w", keyDir, unwrapPath(err))
		}
	}
	if len(found) > 0 {
		return secretKeyExists(found...)
	}
	return nil
}

// placeSecretKeys は src の names（<keygrip>.key）を keyDir（なければ 0700 で作る）に 0600 で
// 置き、置いたもののパスを返す（途中で失敗しても、それまでに置いたものを返す）。
// 返すエラーの文には keygrip を入れない。
func placeSecretKeys(src, keyDir string, names []string) ([]string, error) {
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(keyDir, 0o700); err != nil {
		return nil, err
	}
	var placed []string
	for _, name := range names {
		in, err := os.Open(filepath.Join(src, name))
		if err != nil {
			return placed, fmt.Errorf("open an extracted secret key file: %w", unwrapPath(err))
		}
		dst := filepath.Join(keyDir, name)
		err = writeNew(dst, in, 0o600, func() { placed = append(placed, dst) })
		in.Close()
		if errors.Is(err, fs.ErrExist) {
			return placed, secretKeyExists(name)
		}
		if err != nil {
			return placed, fmt.Errorf("write a secret key file in %s: %w", keyDir, unwrapPath(err))
		}
	}
	return placed, nil
}

// unwrapPath は *fs.PathError と *os.LinkError からパスを落とす（パスには keygrip や
// エントリの名前が入る）。失敗の原因と操作だけを残す。
func unwrapPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("%s: %w", pe.Op, pe.Err)
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return fmt.Errorf("%s: %w", le.Op, le.Err)
	}
	return err
}

// writeNew は新しいファイル p（なければ作る。あれば fs.ErrExist）に r を書き、パーミッションを
// mode にする。ファイルを作ったら created を呼ぶ。
func writeNew(p string, r io.Reader, mode fs.FileMode, created func()) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if created != nil {
		created()
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// layout は extract が確かめた、エクスポートファイルの配置。
type layout struct {
	// top は最上位のディレクトリの名前。
	top string
	// hasKeys は public-keys.asc が空でないか（ストアの鍵があるか）。
	hasKeys bool
	// secretKeys は private-keys-v1.d の鍵ファイルの名前（<keygrip>.key）。
	secretKeys []string
	// modes は store/ の下のディレクトリと通常ファイルのパーミッション（store/ からの相対パス。
	// store/ そのものは "."）。展開するときは 0700 / 0600 にしておき、ストアに置くときに付ける。
	modes map[string]fs.FileMode
}

// extract は tar.gz の r を dir の下に展開し、配置を確かめて返す。受け付けないものは
// *InvalidExportFileError を返す（dir に書いたものは呼び出し側が消す）。
//
// 書けるのは、ディレクトリ、通常ファイル、シンボリックリンク（store/ の下だけ）。
// シンボリックリンクの下には書かない（リンクをたどって外に書かないため）。
func extract(ctx context.Context, r io.Reader, dir string) (layout, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return layout{}, unreadable(err)
	}
	tr := tar.NewReader(gz)
	l := layout{modes: map[string]fs.FileMode{}}
	seen := map[string]bool{}    // 書いたパス（最上位のディレクトリからの相対パス）
	links := map[string]string{} // store/ の下のシンボリックリンク（store/ からの相対パス → リンク先）
	var total int64
	files := 0
	for {
		if err := ctx.Err(); err != nil {
			return layout{}, err
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return layout{}, unreadable(err)
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if files++; files > maxImportFiles {
			return layout{}, invalid(fmt.Sprintf("ファイルが多すぎます（上限は %d 個）", maxImportFiles))
		}
		parts, ok := splitName(h.Name)
		if !ok {
			return layout{}, invalid("正しくないパス（../ や絶対パスなど）があります")
		}
		if l.top == "" {
			l.top = parts[0]
		} else if parts[0] != l.top {
			return layout{}, invalid("最上位のディレクトリが 1 つではありません")
		}
		rel := strings.Join(parts[1:], "/")
		if seen[rel] {
			return layout{}, invalid("同じパスのエントリが 2 つあります")
		}
		if err := checkPlace(rel, h.Typeflag); err != nil {
			return layout{}, err
		}
		inStore, storeRel := storePath(rel)
		if inStore {
			for p := path.Dir(storeRel); p != "."; p = path.Dir(p) {
				if _, ok := links[p]; ok {
					return layout{}, invalid("シンボリックリンクの先に書くエントリがあります")
				}
			}
		}
		seen[rel] = true

		p := filepath.Join(dir, filepath.FromSlash(path.Join(parts...)))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return layout{}, err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.Mkdir(p, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return layout{}, err
			}
		case tar.TypeReg:
			if h.Size > maxImportBytes-total {
				return layout{}, invalid(fmt.Sprintf("展開すると大きすぎます（上限は合計 %d MiB）", maxImportBytes>>20))
			}
			total += h.Size
			if err := writeNew(p, exportFileReader{tr}, 0o600, nil); errors.Is(err, fs.ErrExist) {
				return layout{}, invalid("同じパスのエントリが 2 つあります")
			} else if err != nil {
				return layout{}, err
			}
			switch {
			case rel == importPublicKeys:
				l.hasKeys = h.Size > 0
			case path.Dir(rel) == importSecretDir:
				l.secretKeys = append(l.secretKeys, path.Base(rel))
			}
		case tar.TypeSymlink:
			if h.Linkname == "" || strings.HasPrefix(h.Linkname, "/") {
				return layout{}, invalid("ストアの外を指すシンボリックリンクがあります")
			}
			if err := os.Symlink(h.Linkname, p); errors.Is(err, fs.ErrExist) {
				// 親として先に作ったディレクトリと同じパスのリンク。
				return layout{}, invalid("同じパスのエントリが 2 つあります")
			} else if err != nil {
				return layout{}, err
			}
			links[storeRel] = h.Linkname
		}
		if inStore && h.Typeflag != tar.TypeSymlink {
			l.modes[storeRel] = fs.FileMode(h.Mode).Perm()
		}
	}
	if l.top == "" {
		return layout{}, invalid("空です")
	}
	for _, need := range []string{importStore, importPublicKeys, importOwnertrust} {
		if !seen[need] {
			return layout{}, invalid(need + " がありません")
		}
	}
	if len(l.secretKeys) > 0 && !l.hasKeys {
		return layout{}, invalid("秘密鍵があるのに公開鍵がありません")
	}
	// 後から足されたリンクを通って外に出ることもあるので、すべてのリンクを集めてから確かめる。
	for name := range links {
		if !confined(links, name) {
			return layout{}, invalid("ストアの外を指すシンボリックリンクがあります")
		}
	}
	return l, nil
}

// splitName は tar のエントリ名を要素に分ける。空、絶対パス、"." や ".." や空の要素を
// 含むものは ok = false。末尾の "/"（ディレクトリ）は落とす。
func splitName(name string) ([]string, bool) {
	name = strings.TrimSuffix(name, "/")
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsRune(name, 0) {
		return nil, false
	}
	parts := strings.Split(name, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return nil, false
		}
	}
	return parts, true
}

// storePath は、最上位のディレクトリからの相対パス rel が store/ の中（store/ そのものを含む）
// なら、store/ からの相対パス（store/ そのものは "."）を返す。
func storePath(rel string) (bool, string) {
	if rel == importStore {
		return true, "."
	}
	if s, ok := strings.CutPrefix(rel, importStore+"/"); ok {
		return true, s
	}
	return false, ""
}

// checkPlace は、最上位のディレクトリからの相対パス rel に typeflag のエントリを置けるかを
// 確かめる。
func checkPlace(rel string, typeflag byte) error {
	if typeflag != tar.TypeDir && typeflag != tar.TypeReg && typeflag != tar.TypeSymlink {
		return invalid("ディレクトリ・通常ファイル・シンボリックリンクのどれでもないエントリがあります")
	}
	if inStore, storeRel := storePath(rel); inStore {
		if storeRel == "." && typeflag != tar.TypeDir {
			return invalid("store がディレクトリではありません")
		}
		return nil
	}
	if typeflag == tar.TypeSymlink {
		return invalid("store/ の外にシンボリックリンクがあります")
	}
	var want byte
	switch {
	case rel == "", rel == importGnupg, rel == importSecretDir:
		want = tar.TypeDir
	case rel == importPublicKeys, rel == importOwnertrust, rel == importReadme:
		want = tar.TypeReg
	case path.Dir(rel) == importSecretDir && keyFileName.MatchString(path.Base(rel)):
		want = tar.TypeReg
	default:
		return invalid("エクスポートファイルにないはずのファイルがあります")
	}
	if typeflag != want {
		return invalid("エクスポートファイルの配置と種類が違うエントリがあります")
	}
	return nil
}

// maxLinkHops は confined がたどるシンボリックリンクの数の上限（ループを拒むため）。
const maxLinkHops = 40

// confined は、store/ の下のシンボリックリンク name（store/ からの相対パス）を、エクスポート
// ファイルの中のリンク links（store/ からの相対パス → リンク先）もたどって解決したとき、store/ の外に
// 出ないかを返す。リンク先がエクスポートファイルにない（ぶら下がった）リンクでも、パスが store/ の中に
// とどまるならよい。
func confined(links map[string]string, name string) bool {
	var stack []string // いま解決しているパス（store/ からの要素）
	if d := path.Dir(name); d != "." {
		stack = strings.Split(d, "/")
	}
	queue := strings.Split(links[name], "/")
	hops := 0
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return false
			}
			stack = stack[:len(stack)-1]
			continue
		}
		stack = append(stack, c)
		target, ok := links[strings.Join(stack, "/")]
		if !ok {
			continue
		}
		hops++
		if hops > maxLinkHops || strings.HasPrefix(target, "/") {
			return false
		}
		stack = stack[:len(stack)-1]
		queue = append(strings.Split(target, "/"), queue...)
	}
	return true
}

// placeStore は展開したストア src を storeDir に置く。storeDir はないか空のディレクトリ。
// パーミッションは modes（store/ からの相対パス → パーミッション）のとおりにし、
// シンボリックリンクはリンクのまま置く。失敗したら置いたものを消す（storeDir が空の
// ディレクトリとしてあったなら、ディレクトリは残す）。
func placeStore(ctx context.Context, src, storeDir string, modes map[string]fs.FileMode) (err error) {
	_, statErr := os.Lstat(storeDir)
	existed := statErr == nil
	defer func() {
		if err == nil {
			return
		}
		if !existed {
			_ = os.RemoveAll(storeDir)
			return
		}
		des, _ := os.ReadDir(storeDir)
		for _, de := range des {
			_ = os.RemoveAll(filepath.Join(storeDir, de.Name()))
		}
	}()
	if !existed {
		if err := os.MkdirAll(filepath.Dir(storeDir), 0o700); err != nil {
			return err
		}
	}
	var dirs []string // パーミッションを付けるディレクトリ（store/ からの相対パス。親が先）
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(storeDir, rel)
		switch t := d.Type(); {
		case t.IsDir():
			if rel == "." && existed {
				// 空のディレクトリとしてあるストアのディレクトリ（シンボリックリンクでもよい）に置く。
			} else if err := os.Mkdir(dst, 0o700); err != nil {
				return err
			}
			dirs = append(dirs, rel)
		case t.IsRegular():
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			return writeNew(dst, in, modeOf(modes, rel, 0o600), nil)
		case t&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(target, dst)
		default:
			return fmt.Errorf("unexpected file type in the extracted store: %v", t)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// 書き込めないディレクトリにもしておけるよう、子から先にパーミッションを付ける。
	slices.Reverse(dirs)
	for _, rel := range dirs {
		if err := os.Chmod(filepath.Join(storeDir, rel), modeOf(modes, rel, 0o700)); err != nil {
			return err
		}
	}
	return nil
}

// modeOf は modes の rel（OS の区切り）のパーミッションを返す。エクスポートファイルにエントリがなかった
// （親として作った）ディレクトリなどは def。
func modeOf(modes map[string]fs.FileMode, rel string, def fs.FileMode) fs.FileMode {
	if m, ok := modes[filepath.ToSlash(rel)]; ok {
		return m
	}
	return def
}
