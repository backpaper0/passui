package store

import (
	"context"
	"fmt"
	"os"
)

// Delete はエントリ name を pass rm -f で消す。復号しないのでロック中でも消せる。
// エントリがなければ（外で消されていれば）ErrNotFound を返し、pass を呼ばない。
func (p *Pass) Delete(ctx context.Context, name string) error {
	return p.write(func() error {
		file, err := p.entryFile(name)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(file); err != nil {
			if absent(err) {
				return fmt.Errorf("delete %q: %w", name, ErrNotFound)
			}
			return fmt.Errorf("delete %q: %w", name, err)
		}
		_, err = p.run(ctx, nil, "rm", "-f", "--", name)
		return err
	})
}
