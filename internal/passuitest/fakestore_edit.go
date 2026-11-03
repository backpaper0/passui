package passuitest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/backpaper0/passui/internal/store"
)

// Update は store.Pass.Update と同じ確認をしてからエントリを書き換える。「暗号文」は
// 書かれた本文（末尾に改行が付いたもの）そのものにする。
func (f *FakeStore) Update(ctx context.Context, e store.Edit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	c, ok := f.entries[e.Name]
	if !ok {
		return fmt.Errorf("update %q: %w: %w", e.Name, store.ErrConflict, store.ErrNotFound)
	}
	if sum := sha256.Sum256([]byte(c)); e.Hash == "" || hex.EncodeToString(sum[:]) != e.Hash {
		return fmt.Errorf("update %q: %w", e.Name, store.ErrConflict)
	}
	if e.Moves() {
		if err := f.checkDestination(e.NewName); err != nil {
			return fmt.Errorf("move %q: %w", e.Name, err)
		}
	}
	f.entries[e.Name] = store.WithTrailingNewline(e.Content.Reveal())
	if !e.Moves() {
		return nil
	}
	if f.moveErr != nil {
		return fmt.Errorf("move %q to %q: %w: %w", e.Name, e.NewName, store.ErrMoveFailed, f.moveErr)
	}
	f.entries[e.NewName] = f.entries[e.Name]
	delete(f.entries, e.Name)
	return nil
}

// SetMoveErr を nil 以外にすると、以降の Update の移動（pass mv）だけが失敗する
// （本文は元のパスに保存され、store.ErrMoveFailed と err を包んだエラーを返す）。
func (f *FakeStore) SetMoveErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.moveErr = err
}
