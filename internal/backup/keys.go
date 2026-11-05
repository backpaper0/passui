package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/backpaper0/passui/internal/store"
)

// UnresolvedKeysError は、ストアの鍵の ID（.gpg-id や PASSWORD_STORE_KEY に書かれたもの）の
// うち、公開鍵に解決できなかったものがあることを表す。バックアップ先には何も置かない。
type UnresolvedKeysError struct {
	// IDs は解決できなかった鍵 ID（書かれたまま）。秘密ではないので画面に出してよい。
	IDs []string
}

func (e *UnresolvedKeysError) Error() string {
	return "store keys not found: " + strings.Join(e.IDs, ", ")
}

// storeKey はバックアップに入れるストアの鍵 1 つ。
type storeKey struct {
	// Fingerprint は主鍵の指紋。
	Fingerprint string
	// Secrets は、ゲストに秘密部分がある主鍵・副鍵の鍵ファイル。空ならゲストに秘密鍵がない。
	Secrets []secretKey
}

// gpg はバックアップのために gpg を呼ぶ。ストアにも gpg-agent にも書き込む呼び出しはしない。
type gpg struct {
	// prog は gpg の実行ファイル（env の PATH から探す）。
	prog string
	// env は子プロセスの環境（GNUPGHOME などを含む）。LC_ALL は C に上書きする。
	env []string
}

func newGPG(env []string) *gpg {
	g := &gpg{env: make([]string, 0, len(env)+1)}
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); k != "LC_ALL" {
			g.env = append(g.env, kv)
		}
	}
	g.env = append(g.env, "LC_ALL=C")
	g.prog = lookPath(env, "gpg")
	return g
}

// lookPath は env の PATH から実行可能な name を探す。見つからなければ name を
// そのまま返す（プロセスの PATH で探される）。
func lookPath(env []string, name string) string {
	for _, dir := range filepath.SplitList(lookupEnv(env, "PATH")) {
		if dir == "" {
			continue
		}
		file := filepath.Join(dir, name)
		if fi, err := os.Stat(file); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return file
		}
	}
	return name
}

// run は gpg --batch --no-auto-check-trustdb <args> を呼び、stdout を返す。終了コードが
// 0 でなければ、stderr を含むエラーを返す（呼び出し側がログに出す）。
// --no-auto-check-trustdb は、一覧を出すついでに trustdb を検査して trustdb.gpg を
// 書き換えないため（バックアップは取るだけで、gpg の状態を変えない）。
func (g *gpg) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, g.prog, append([]string{"--batch", "--no-auto-check-trustdb"}, args...)...)
	cmd.Env = g.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("gpg %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// keyIDs はストアの鍵の ID（すべての .gpg-id と、あれば PASSWORD_STORE_KEY）を、重複なしで返す。
func keyIDs(ctx context.Context, storeDir string, env []string) ([]string, error) {
	ids, err := store.KeyIDs(ctx, storeDir)
	if err != nil {
		return nil, err
	}
	// pass と同じく、PASSWORD_STORE_KEY は空白区切りの鍵の一覧。
	for _, id := range strings.Fields(lookupEnv(env, "PASSWORD_STORE_KEY")) {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// lookupEnv は env（"KEY=VALUE"）の key の値を返す。複数あれば後のものを使う。
func lookupEnv(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, _ := strings.Cut(env[i], "="); k == key {
			return v
		}
	}
	return ""
}

// collectKeys はストアの鍵の ID を、gpg --list-keys で主鍵の指紋に解決する。
// 解決できない ID が 1 つでもあれば *UnresolvedKeysError を返す（gpg --export* は
// 存在しない ID を黙って飛ばすため、ここで ID ごとに確かめる）。
// ID が 1 つもなければ（.gpg-id のないストア）、gpg を呼ばずに空を返す。
func collectKeys(ctx context.Context, g *gpg, ids []string) ([]storeKey, error) {
	var keys []storeKey
	var unresolved []string
	for _, id := range ids {
		out, err := g.run(ctx, "--with-colons", "--list-keys", "--", id)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			unresolved = append(unresolved, id)
			continue
		}
		fprs := primaryFingerprints(out)
		if len(fprs) == 0 {
			unresolved = append(unresolved, id)
		}
		for _, fpr := range fprs {
			if !slices.ContainsFunc(keys, func(k storeKey) bool { return k.Fingerprint == fpr }) {
				keys = append(keys, storeKey{Fingerprint: fpr})
			}
		}
	}
	if len(unresolved) > 0 {
		// gpg そのものが動かないのか、ID が見つからないのかを分ける。
		if _, err := g.run(ctx, "--with-colons", "--list-keys"); err != nil {
			return nil, err
		}
		return nil, &UnresolvedKeysError{IDs: unresolved}
	}
	return keys, nil
}

// primaryFingerprints は gpg --with-colons の出力から、主鍵（pub / sec）の指紋を返す。
func primaryFingerprints(out []byte) []string {
	var fprs []string
	primary := false
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, ":")
		switch {
		case f[0] == "pub" || f[0] == "sec":
			primary = true
		case f[0] == "sub" || f[0] == "ssb":
			primary = false
		case f[0] == "fpr" && len(f) > 9 && primary:
			fprs = append(fprs, f[9])
			primary = false
		}
	}
	return fprs
}

// secretKey は、ゲストに秘密部分がある鍵（主鍵か副鍵）の gpg-agent の鍵ファイル 1 つ。
type secretKey struct {
	// Keygrip は鍵ファイルの名前（<keygrip>.key）。
	Keygrip string
	// data は .key の中身（元のパスフレーズで保護されたまま）。ログにも README.txt にも出さない。
	data []byte
}

// collectSecretKeys は、ストアの鍵 keys の主鍵・副鍵のうちゲストに秘密部分があるものの
// .key を <homedir>/private-keys-v1.d/ から読み、keys[i].Secrets に入れる。
//
// 秘密部分があるかは、.key があるかで決める。gpg --list-secret-keys の sec / ssb の
// 第 15 フィールドの "#"（主鍵がオフラインの sec# など）も gpg-agent が .key の有無から
// 出すものだが、gpg-agent は読めない .key も「ない」と扱うため、そのまま使うと読めない
// .key を黙って飛ばしてしまう。ここでは .key がないときだけ飛ばし、読めなければ失敗にする。
// gpg-agent には問い合わせないので、ロック状態もキャッシュも変わらない。
// 返すエラーには keygrip を入れない（ログに出るため）。
func collectSecretKeys(ctx context.Context, g *gpg, keys []storeKey) error {
	if len(keys) == 0 {
		return nil
	}
	args := []string{"--with-colons", "--with-keygrip", "--list-keys", "--"}
	for _, k := range keys {
		args = append(args, k.Fingerprint)
	}
	out, err := g.run(ctx, args...)
	if err != nil {
		return err
	}
	grips := keygrips(out)
	home, err := g.homedir(ctx)
	if err != nil {
		return err
	}
	for i := range keys {
		for _, grip := range grips[keys[i].Fingerprint] {
			data, err := os.ReadFile(filepath.Join(home, "private-keys-v1.d", grip+".key"))
			if errors.Is(err, fs.ErrNotExist) {
				continue // ゲストにこの主鍵・副鍵の秘密部分がない
			}
			if err != nil {
				var pe *fs.PathError
				if errors.As(err, &pe) {
					err = pe.Err // パスには keygrip が入る
				}
				return fmt.Errorf("read a secret key file in %s: %w", filepath.Join(home, "private-keys-v1.d"), err)
			}
			keys[i].Secrets = append(keys[i].Secrets, secretKey{Keygrip: grip, data: data})
		}
	}
	return nil
}

// keygrips は gpg --with-colons --with-keygrip --list-keys の出力から、
// 主鍵の指紋 → 主鍵と副鍵の keygrip（出てきた順）を返す。
func keygrips(out []byte) map[string][]string {
	grips := map[string][]string{}
	var fpr string   // いまの鍵の主鍵の指紋
	wantFpr := false // 次の fpr 行が主鍵の指紋
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, ":")
		switch f[0] {
		case "pub":
			wantFpr = true
		case "sub":
			wantFpr = false
		case "fpr":
			if wantFpr && len(f) > 9 {
				fpr = f[9]
				wantFpr = false
			}
		case "grp":
			if fpr != "" && len(f) > 9 && f[9] != "" && !slices.Contains(grips[fpr], f[9]) {
				grips[fpr] = append(grips[fpr], f[9])
			}
		}
	}
	return grips
}

// homedir は gpgconf --list-dirs homedir で gpg の homedir を求める（GNUPGHOME を反映する）。
func (g *gpg) homedir(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, lookPath(g.env, "gpgconf"), "--list-dirs", "homedir")
	cmd.Env = g.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("gpgconf --list-dirs homedir: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	dir := unescapeGpgconf(strings.TrimSpace(stdout.String()))
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("gpgconf --list-dirs homedir: not an absolute path: %q", dir)
	}
	return dir, nil
}

// unescapeGpgconf は gpgconf の出力の %XX（":" や "%" など）を元の文字に戻す。
func unescapeGpgconf(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(v))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// exportPublicKeys はストアの鍵すべての公開鍵を armor で返す。
func exportPublicKeys(ctx context.Context, g *gpg, keys []storeKey) ([]byte, error) {
	if len(keys) == 0 {
		return nil, nil // 引数なしの --export はすべての鍵を書き出すので、呼ばない
	}
	args := []string{"--armor", "--export", "--"}
	for _, k := range keys {
		args = append(args, k.Fingerprint)
	}
	return g.run(ctx, args...)
}

// exportOwnertrust は --export-ownertrust から、ストアの鍵の指紋の行だけを返す。
// 行がなくても失敗にしない。
func exportOwnertrust(ctx context.Context, g *gpg, keys []storeKey) ([]byte, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	out, err := g.run(ctx, "--export-ownertrust")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	for _, line := range strings.Split(string(out), "\n") {
		fpr, _, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		if slices.ContainsFunc(keys, func(k storeKey) bool { return strings.EqualFold(k.Fingerprint, fpr) }) {
			buf.WriteString(line + "\n")
		}
	}
	return buf.Bytes(), nil
}
