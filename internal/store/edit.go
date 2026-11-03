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
	// Hash は編集を開いた時点の暗号文のハッシュ（Backend.Hash の値）。今のハッシュと
	// 違えば ErrConflict で保存しない。
	Hash string
	// Content は新しい本文。末尾に改行がなければ足す（Create と同じ）。
	Content secret.Value
	// TODO(#35): 新しいパス（NewName）を足し、違えば insert の後に mv する。
}

// Update は編集した本文で既存のエントリ e.Name を書き換える（pass insert -m -f）。
// 書き込みの mutex の中で、存在とハッシュを確かめてから pass を呼ぶ。
func (p *Pass) Update(ctx context.Context, e Edit) error {
	return p.write(func() error {
		if err := checkUnchanged(ctx, p, e); err != nil {
			return err
		}
		_, err := p.run(ctx, []byte(WithTrailingNewline(e.Content.Reveal())), "insert", "-m", "-f", "--", e.Name)
		return err
	})
}

// checkUnchanged は、編集を開いた後にエントリが外で変えられていないかを確かめる。
// 消えていれば ErrConflict と ErrNotFound の両方（errors.Is で比べられる）、ハッシュが
// 違えば ErrConflict を返す。
func checkUnchanged(ctx context.Context, p *Pass, e Edit) error {
	current, err := p.Hash(ctx, e.Name)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("update %s: %w: %w", e.Name, ErrConflict, ErrNotFound)
	}
	if err != nil {
		return err
	}
	if e.Hash == "" || current != e.Hash {
		return fmt.Errorf("update %s: %w", e.Name, ErrConflict)
	}
	return nil
}
