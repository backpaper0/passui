package passuitest

import (
	"context"
	"fmt"

	"github.com/backpaper0/passui/internal/store"
)

// Delete はエントリを消す。なければ store.ErrNotFound。ロック中でも消せる。
func (f *FakeStore) Delete(ctx context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if _, ok := f.entries[name]; !ok {
		return fmt.Errorf("delete %q: %w", name, store.ErrNotFound)
	}
	delete(f.entries, name)
	return nil
}
