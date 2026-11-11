package export

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
// うち、公開鍵に解決できなかったものがあることを表す。
type UnresolvedKeysError struct {
	// IDs は解決できなかった鍵 ID（書かれたまま）。秘密ではないので画面に出してよい。
	IDs []string
}

func (e *UnresolvedKeysError) Error() string {
	return "store keys not found: " + strings.Join(e.IDs, ", ")
}

// storeKey はエクスポートに入れるストアの鍵 1 つ。
type storeKey struct {
	// Fingerprint は主鍵の指紋。
	Fingerprint string
	// Secrets は、ゲストに秘密部分がある主鍵・副鍵の鍵ファイル。空ならゲストに秘密鍵がない。
	Secrets []secretKey
}

// gpg はエクスポートのために gpg と gpgconf を呼ぶ。ストアにも gpg-agent にも書き込む
// 呼び出しはしない。
//
// ストアの Pass（store パッケージ）は子プロセスの stderr を warn でログに出してエラーには
// 入れないが、ここでは stderr をエラーに入れて返す。export はロガーを持たず、失敗のログは
// 呼び出し側（server）がエラー ID と一緒に 1 行で出すため（失敗時のログにエラー ID と
// gpg の stderr を出す、という要件）。ここで呼ぶ gpg の stdin にも stdout にも
// シークレットは流れない。
type gpg struct {
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
	return g
}

// lookPath は env の PATH から実行可能な name を探す。見つからなければ name を
// そのまま返す（プロセスの PATH で探され、見つからなければ実行のときに失敗する）。
// cli にも env の PATH から探す lookPath があるが、あちらは起動時の設定の検査で、
// 見つからなければ日本語のエラーで止める。ここではエクスポートの失敗として扱えば
// よいので、パッケージをまたいで共有はしない。
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
// 書き換えないため（エクスポートは取るだけで、gpg の状態を変えない）。
func (g *gpg) run(ctx context.Context, args ...string) ([]byte, error) {
	return g.exec(ctx, "gpg", append([]string{"--batch", "--no-auto-check-trustdb"}, args...)...)
}

// exec は name（env の PATH から探す）を args で呼び、stdout を返す。終了コードが 0 で
// なければ、stderr を含むエラーを返す。ctx が終わっていれば ctx のエラーを返す。
func (g *gpg) exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, lookPath(g.env, name), args...)
	cmd.Env = g.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// keyIDs はストアの鍵の ID（opts.StoreDir のすべての .gpg-id と、opts.Env に
// PASSWORD_STORE_KEY があればその鍵）を、重複なしで返す。
func keyIDs(ctx context.Context, opts Options) ([]string, error) {
	ids, err := store.KeyIDs(ctx, opts.StoreDir)
	if err != nil {
		return nil, err
	}
	// pass と同じく、PASSWORD_STORE_KEY は空白区切りの鍵の一覧。
	for _, id := range strings.Fields(lookupEnv(opts.Env, "PASSWORD_STORE_KEY")) {
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
		listed := parseKeyList(out)
		if len(listed) == 0 {
			unresolved = append(unresolved, id)
		}
		for _, l := range listed {
			if !hasFingerprint(keys, l.Fingerprint) {
				keys = append(keys, storeKey{Fingerprint: l.Fingerprint})
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

// listedKey は gpg --with-colons --list-keys の一覧の鍵 1 つ。
type listedKey struct {
	// Fingerprint は主鍵の指紋。
	Fingerprint string
	// Keygrips は主鍵と副鍵の keygrip（出てきた順）。--with-keygrip のときだけ入る。
	Keygrips []string
}

// parseKeyList は gpg --with-colons --list-keys（--with-keygrip 付きでもよい）の出力を、
// 主鍵の指紋がわかった鍵ごとに返す。ここでは公開鍵の一覧（pub / sub）だけを読む
// （秘密鍵の一覧の sec / ssb は使わない。collectSecretKeys を参照）。
func parseKeyList(out []byte) []listedKey {
	var keys []listedKey
	cur := -1        // いまの鍵の keys での位置（pub の次の fpr 行で決まる）
	wantFpr := false // 次の fpr 行が主鍵の指紋
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, ":")
		switch f[0] {
		case "pub":
			cur, wantFpr = -1, true
		case "sub":
			wantFpr = false
		case "fpr":
			if wantFpr && len(f) > 9 && f[9] != "" {
				keys = append(keys, listedKey{Fingerprint: f[9]})
				cur, wantFpr = len(keys)-1, false
			}
		case "grp":
			if cur >= 0 && len(f) > 9 && f[9] != "" && !slices.Contains(keys[cur].Keygrips, f[9]) {
				keys[cur].Keygrips = append(keys[cur].Keygrips, f[9])
			}
		}
	}
	return keys
}

// sameFingerprint は 2 つの指紋が同じ鍵を指すか（16 進なので大文字・小文字を区別しない）。
func sameFingerprint(a, b string) bool {
	return strings.EqualFold(a, b)
}

// hasFingerprint は keys に指紋 fpr の鍵があるか。
func hasFingerprint(keys []storeKey, fpr string) bool {
	return slices.ContainsFunc(keys, func(k storeKey) bool { return sameFingerprint(k.Fingerprint, fpr) })
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
	listed := parseKeyList(out)
	home, err := g.homedir(ctx)
	if err != nil {
		return err
	}
	for i := range keys {
		var grips []string
		for _, l := range listed {
			if sameFingerprint(l.Fingerprint, keys[i].Fingerprint) {
				grips = l.Keygrips
			}
		}
		for _, grip := range grips {
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

// homedir は gpgconf --list-dirs homedir で gpg の homedir を求める（GNUPGHOME を反映する）。
func (g *gpg) homedir(ctx context.Context) (string, error) {
	out, err := g.exec(ctx, "gpgconf", "--list-dirs", "homedir")
	if err != nil {
		return "", err
	}
	dir := unescapeGpgconf(strings.TrimSpace(string(out)))
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
		if hasFingerprint(keys, fpr) {
			buf.WriteString(line + "\n")
		}
	}
	return buf.Bytes(), nil
}
