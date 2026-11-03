package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/backpaper0/passui/internal/secret"
)

// Edit は編集フォームの保存（Backend.Update）に渡すもの。
type Edit struct {
	// Name は編集しているエントリ（元のパス）。
	Name string
	// NewName はパス欄の値（保存先のパス）。空か Name と同じなら内容だけの編集、
	// 違えば Name に保存してから NewName へ移動する（移動・リネーム）。
	NewName string
	// Hash は編集を開いた時点の暗号文のハッシュ（Backend.Hash の値）。今のハッシュと
	// 違えば ErrConflict で保存しない。
	Hash string
	// Content は新しい本文。末尾に改行がなければ足す（Create と同じ）。
	Content secret.Value
}

// Moves はパスが変わる（移動・リネームになる）かを返す。
func (e Edit) Moves() bool {
	return e.NewName != "" && e.NewName != e.Name
}

// Update は編集した本文で既存のエントリ e.Name を書き換え（pass insert -m -f）、
// パスが変わるなら e.NewName へ移動する（pass mv -f。.gpg-id の解決と再暗号化は pass に
// 任せる）。書き込みの mutex の中で、存在とハッシュと移動先を確かめてから pass を呼ぶ。
// mv だけが失敗したら ErrMoveFailed を返す（本文は元のパスに保存されている）。
func (p *Pass) Update(ctx context.Context, e Edit) error {
	return p.write(func() error {
		if err := checkUnchanged(ctx, p, e); err != nil {
			return err
		}
		if e.Moves() {
			if err := p.checkDestination(e.NewName); err != nil {
				return err
			}
		}
		if _, err := p.run(ctx, []byte(WithTrailingNewline(e.Content.Reveal())), "insert", "-m", "-f", "--", e.Name); err != nil {
			return err
		}
		if !e.Moves() {
			return nil
		}
		if _, err := p.run(ctx, nil, "mv", "-f", "--", e.Name, e.NewName); err != nil {
			return fmt.Errorf("move %q to %q: %w: %w", e.Name, e.NewName, ErrMoveFailed, err)
		}
		return nil
	})
}

// checkUnchanged は、編集を開いた後にエントリが外で変えられていないかを確かめる。
// 消えていれば ErrConflict と ErrNotFound の両方（errors.Is で比べられる）、ハッシュが
// 違えば ErrConflict を返す。
func checkUnchanged(ctx context.Context, p *Pass, e Edit) error {
	current, err := p.Hash(ctx, e.Name)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("update %q: %w: %w", e.Name, ErrConflict, ErrNotFound)
	}
	if err != nil {
		return err
	}
	if e.Hash == "" || current != e.Hash {
		return fmt.Errorf("update %q: %w", e.Name, ErrConflict)
	}
	return nil
}
