package backup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// run は gpg --batch <args> を呼び、stdout を返す。終了コードが 0 でなければ、
// stderr を含むエラーを返す（呼び出し側がログに出す）。
func (g *gpg) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, g.prog, append([]string{"--batch"}, args...)...)
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
