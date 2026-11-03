package passuitest

import (
	"context"
	"fmt"
	"strings"

	"github.com/backpaper0/passui/internal/secret"
	"github.com/backpaper0/passui/internal/store"
)

// Create は store.Pass.Create と同じ確認をしてからエントリを置く。「暗号文」は
// ストアに書かれる本文（末尾に改行が付いたもの）そのものにする（Content で読める）。
func (f *FakeStore) Create(ctx context.Context, name string, content secret.Value) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if err := f.checkDestination(name); err != nil {
		return fmt.Errorf("create %q: %w", name, err)
	}
	f.entries[name] = store.WithTrailingNewline(content.Reveal())
	return nil
}

// checkDestination は store.Pass と同じ規則で、新しいエントリ name を置けるかを確かめる。
// f.mu を持って呼ぶこと。
func (f *FakeStore) checkDestination(name string) error {
	if err := store.CheckNewName(name); err != nil {
		return err
	}
	for n := range f.entries {
		if strings.HasPrefix(n, name+"/") {
			return store.ErrIsDirectory
		}
	}
	if _, ok := f.entries[name]; ok {
		return store.ErrExists
	}
	return nil
}
