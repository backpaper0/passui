package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/backpaper0/passui/internal/secret"
)

// write は書き込みの mutex を取って fn を呼ぶ。書き込み系の操作（作成、生成、編集、
// 移動、削除）は、存在とハッシュの確認から pass の呼び出しまでを fn の中で行うこと。
// 読み取り（Entries、Exists、Hash、閲覧）はロックしない。
func (p *Pass) write(fn func() error) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return fn()
}

// checkDestination は新しいエントリ name を置けるかを確かめる（write の中で呼ぶ）。
// pass は TTY がないと上書きを確かめないので、呼ぶ前に passui が確かめる。
// 名前の形が悪ければ ErrInvalidName か ErrIsDirectory、同名のディレクトリがあれば
// ErrIsDirectory、同じ名前のエントリがあれば ErrExists。
func (p *Pass) checkDestination(name string) error {
	if err := CheckNewName(name); err != nil {
		return fmt.Errorf("destination %q: %w", name, err)
	}
	file := filepath.Join(p.cfg.Dir, filepath.FromSlash(name))
	if fi, err := os.Stat(file); err == nil && fi.IsDir() {
		return fmt.Errorf("destination %q: %w", name, ErrIsDirectory)
	} else if err != nil && !absent(err) {
		return fmt.Errorf("destination %q: %w", name, err)
	}
	if _, err := os.Lstat(file + ".gpg"); err == nil {
		return fmt.Errorf("destination %q: %w", name, ErrExists)
	} else if !absent(err) {
		return fmt.Errorf("destination %q: %w", name, err)
	}
	return nil
}

// absent は stat のエラーが「ない」を表すかを返す。途中の要素がファイルのとき
// （ENOTDIR）も「ない」と扱う（その場合は pass の呼び出しが失敗する）。
func absent(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// Create は新しいエントリを pass insert -m -f で作る。本文は stdin で渡す。
func (p *Pass) Create(ctx context.Context, name string, content secret.Value) error {
	return p.write(func() error {
		if err := p.checkDestination(name); err != nil {
			return err
		}
		_, err := p.run(ctx, []byte(WithTrailingNewline(content.Reveal())), "insert", "-m", "-f", "--", name)
		return err
	})
}
