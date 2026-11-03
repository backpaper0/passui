package passuitest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	"github.com/backpaper0/passui/internal/store"
)

// FakeStore は HTTP 層のテストに使うストア バックエンド。エントリの名前と
// 「暗号文」（ただの文字列）をメモリに持つ。並行に使ってよい。
type FakeStore struct {
	mu      sync.Mutex
	entries map[string]string
	err     error
}

var _ store.Backend = (*FakeStore)(nil)

// NewFakeStore は names のエントリを持つ FakeStore を作る。暗号文は名前から作る。
func NewFakeStore(names ...string) *FakeStore {
	f := &FakeStore{entries: map[string]string{}}
	for _, n := range names {
		f.entries[n] = "ciphertext of " + n
	}
	return f
}

// Put はエントリを置く（あれば上書きする）。外から pass で書き換えた状況に使う。
func (f *FakeStore) Put(name, ciphertext string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[name] = ciphertext
}

// Remove はエントリを消す。外から pass rm した状況に使う。
func (f *FakeStore) Remove(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.entries, name)
}

// SetErr を nil 以外にすると、以降のすべての操作がそのエラーを返す。
// 分類したエラー（store.ErrLocked など）や、そのほかの失敗を起こすのに使う。
func (f *FakeStore) SetErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *FakeStore) Entries(ctx context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	names := make([]string, 0, len(f.entries))
	for n := range f.entries {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

func (f *FakeStore) Exists(ctx context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, f.err
	}
	_, ok := f.entries[name]
	return ok, nil
}

func (f *FakeStore) Hash(ctx context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	c, ok := f.entries[name]
	if !ok {
		return "", fmt.Errorf("hash %s: %w", name, store.ErrNotFound)
	}
	sum := sha256.Sum256([]byte(c))
	return hex.EncodeToString(sum[:]), nil
}
