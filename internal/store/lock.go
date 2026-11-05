package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

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
	out, err := p.agent(ctx, "KEYINFO", grips)
	if err != nil {
		return false, err
	}
	// "S KEYINFO <keygrip> <type> <serialno> <idstr> <cached> ..."。cached は "1" か "-"。
	for _, line := range out {
		f := strings.Fields(line)
		if len(f) > 6 && f[0] == "S" && f[1] == "KEYINFO" && f[6] == "1" {
			return false, nil
		}
	}
	return true, nil
}

// Lock は、ストアの鍵（ルートとサブフォルダのすべての .gpg-id に書かれた受信者の鍵）の
// うち、暗号化に使える秘密鍵（主鍵と副鍵）の keygrip について、gpg-agent に
// CLEAR_PASSPHRASE を送り、解錠のキャッシュを消す。ストアと関係のない鍵には触れない。
// .gpg-id が 1 つも読めなければ、locked と同じくすべての秘密鍵を対象にする。
// キャッシュされていない鍵に送っても失敗しない。gpg-agent のキャッシュはターミナルと
// 共有なので、ターミナルの pass もロックされる。
func (p *Pass) Lock(ctx context.Context) error {
	ids, err := KeyIDs(ctx, p.cfg.Dir)
	if err != nil {
		return err
	}
	grips, err := p.keygrips(ctx, ids)
	if err != nil {
		return err
	}
	if len(grips) == 0 {
		return errors.New("no secret key to lock")
	}
	out, err := p.agent(ctx, "CLEAR_PASSPHRASE --mode=normal", grips)
	if err != nil {
		return err
	}
	// gpg-connect-agent は、コマンドが ERR を返しても終了コード 0 で終わる。
	for _, line := range out {
		if strings.HasPrefix(line, "ERR ") {
			p.logger.WarnContext(ctx, "gpg-connect-agent failed", "command", "CLEAR_PASSPHRASE", "response", line)
			return fmt.Errorf("clear passphrase: %s", line)
		}
	}
	return nil
}

// agent は keygrip ごとに "<command> <keygrip>" を gpg-connect-agent で gpg-agent に送り、
// 応答を行に分けて返す。
func (p *Pass) agent(ctx context.Context, command string, grips []string) ([]string, error) {
	args := make([]string, 0, len(grips)+1)
	for _, g := range grips {
		args = append(args, command+" "+g)
	}
	out, err := p.exec(ctx, p.tool("gpg-connect-agent"), nil, append(args, "/bye")...)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(out), "\n"), nil
}

// recipients は dir に効く .gpg-id（dir から上にたどって最初に見つかるもの）の鍵を返す。
// 見つからなければ nil。
func (p *Pass) recipients(dir string) []string {
	for {
		if ids, err := readGPGID(filepath.Join(p.cfg.Dir, filepath.FromSlash(dir))); err == nil {
			return ids
		}
		if dir == "." || dir == "/" || dir == "" {
			return nil
		}
		dir = path.Dir(dir)
	}
}

// KeyIDs は、ストアのディレクトリ storeDir の中のすべての .gpg-id（ルートとサブフォルダ）に
// 書かれたストアの鍵の ID を、書かれた順に重複を除いて返す。読めない .gpg-id は飛ばし、
// 1 つも読めなければ nil。ID は .gpg-id に書かれたまま（指紋とは限らない）。
// ロックとバックアップが共有する。
func KeyIDs(ctx context.Context, storeDir string) ([]string, error) {
	var ids []string
	err := walkStore(ctx, storeDir, func(dir, rel string, files []fs.DirEntry) {
		found, err := readGPGID(dir)
		if err != nil {
			return
		}
		for _, id := range found {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	})
	return ids, err
}

// readGPGID はディレクトリ dir の .gpg-id を読む。pass と同じく、"#" より後ろと
// 空行は無視する。
func readGPGID(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, ".gpg-id"))
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, line := range strings.Split(string(b), "\n") {
		line, _, _ = strings.Cut(line, "#")
		if line = strings.TrimSpace(line); line != "" {
			ids = append(ids, line)
		}
	}
	return ids, nil
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
