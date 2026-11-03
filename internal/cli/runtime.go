package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/backpaper0/passui/internal/secret"
)

// $XDG_RUNTIME_DIR/passui/ に置くファイル。serve が起動ごとに書き、正常に止まるときに消す。
// login はサービスと通信せず、この 2 つを読んでログイン URL を組み立てる。
const (
	tokenFileName = "token" // ログイン トークン
	urlFileName   = "url"   // serve 自身の URL（"http://passui.localhost:<port>/"）
)

// runtimeDir は $XDG_RUNTIME_DIR/passui を返す。
func runtimeDir(env environ) (string, error) {
	base, _ := env.lookup("XDG_RUNTIME_DIR")
	if base == "" {
		return "", errors.New("XDG_RUNTIME_DIR が設定されていません")
	}
	return filepath.Join(base, "passui"), nil
}

// writeRuntimeFiles はディレクトリを 0700 で用意し、トークンと URL を 0600 で書く。
// install-service が URL ファイルの更新を待つので、URL は最後に書く。
func writeRuntimeFiles(dir string, token secret.Value, url string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// 既にあったディレクトリのパーミッションも直す。
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(dir, tokenFileName, token.Reveal()+"\n"); err != nil {
		return err
	}
	return writeFileAtomic(dir, urlFileName, url+"\n")
}

// writeFileAtomic は一時ファイル（0600）に書いてから rename する。
// login が書きかけのファイルを読まないようにするため。
func writeFileAtomic(dir, name, content string) error {
	f, err := os.CreateTemp(dir, "."+name+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // rename に成功したら何もしない
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := io.WriteString(f, content); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}

// removeRuntimeFiles はトークンと URL を消す。
func removeRuntimeFiles(dir string) error {
	var errs []error
	for _, name := range []string{tokenFileName, urlFileName} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// errNotRunning は serve が動いていない（ファイルがない）ことを表す。
var errNotRunning = errors.New("passui は起動していません")

// readLoginURL はファイルからログイン URL を組み立てる。
func readLoginURL(dir string) (secret.Value, error) {
	read := func(name string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			return "", errNotRunning
		}
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	url, err := read(urlFileName)
	if err != nil {
		return secret.Value{}, err
	}
	token, err := read(tokenFileName)
	if err != nil {
		return secret.Value{}, err
	}
	if url == "" || token == "" {
		return secret.Value{}, errNotRunning
	}
	return secret.Of(strings.TrimSuffix(url, "/") + "/login?token=" + token), nil
}

// login はログイン URL を標準出力に出す。サービスとは通信しない。
func login(env environ, stdout, stderr io.Writer) int {
	dir, err := runtimeDir(env)
	if err != nil {
		fmt.Fprintf(stderr, "passui: %v\n", err)
		return 1
	}
	u, err := readLoginURL(dir)
	if errors.Is(err, errNotRunning) {
		fmt.Fprintln(stderr, "passui: passui は起動していないようです。サービスの状態を確かめてください: systemctl --user status passui")
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "passui: ログイン URL を読めません: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, u.Reveal())
	return 0
}
