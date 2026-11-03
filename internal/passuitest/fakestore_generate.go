package passuitest

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"

	"github.com/backpaper0/passui/internal/store"
)

const (
	fakeAlnum   = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	fakeSymbols = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"
	// fakeDefaultLength は pass の既定の長さ（PASSWORD_STORE_GENERATED_LENGTH がないとき）。
	fakeDefaultLength = 25
)

// fakePassword は opts に従ってパスワードを作る（pass generate の代わり）。
func fakePassword(opts store.GenerateOptions) string {
	chars := fakeAlnum + fakeSymbols
	if opts.NoSymbols {
		chars = fakeAlnum
	}
	n := opts.Length
	if n == 0 {
		n = fakeDefaultLength
	}
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

// Generate は store.Pass.Generate と同じ確認をしてから、パスワード 1 行だけの
// エントリを置く（Content で読める）。
func (f *FakeStore) Generate(ctx context.Context, name string, opts store.GenerateOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if err := f.checkDestination(name); err != nil {
		return fmt.Errorf("generate %q: %w", name, err)
	}
	f.entries[name] = fakePassword(opts) + "\n"
	return nil
}

// Regenerate はエントリの 1 行目だけを新しいパスワードに替える。なければ
// store.ErrNotFound、ロック中（SetLocked(true)）なら store.ErrLocked で、変えない。
func (f *FakeStore) Regenerate(ctx context.Context, name string, opts store.GenerateOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	c, ok := f.entries[name]
	if !ok {
		return fmt.Errorf("regenerate %q: %w", name, store.ErrNotFound)
	}
	if f.locked {
		return fmt.Errorf("regenerate %q: %w", name, store.ErrLocked)
	}
	_, rest, _ := strings.Cut(c, "\n")
	f.entries[name] = fakePassword(opts) + "\n" + rest
	return nil
}
