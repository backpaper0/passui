package store

import (
	"context"
	"fmt"
	"path"
	"strconv"
)

// GenerateOptions はパスワード生成の指定。ゼロ値は pass の既定（長さは
// PASSWORD_STORE_GENERATED_LENGTH か 25、文字は PASSWORD_STORE_CHARACTER_SET）。
type GenerateOptions struct {
	// Length は長さ。0 なら pass の既定。
	Length int
	// NoSymbols は記号を使わない（pass generate -n。文字は
	// PASSWORD_STORE_CHARACTER_SET_NO_SYMBOLS）。
	NoSymbols bool
}

// args は pass generate のフラグの後ろに付ける引数（-n、--、name、長さ）を返す。
func (o GenerateOptions) args(name string) []string {
	var args []string
	if o.NoSymbols {
		args = append(args, "-n")
	}
	args = append(args, "--", name)
	if o.Length > 0 {
		args = append(args, strconv.Itoa(o.Length))
	}
	return args
}

// Generate は pass generate -f で新しいエントリを作る。pass は生成したパスワードを
// stdout に出すが、run は stdout をログに出さず、ここでも捨てる。
func (p *Pass) Generate(ctx context.Context, name string, opts GenerateOptions) error {
	return p.write(func() error {
		if err := p.checkDestination(name); err != nil {
			return err
		}
		_, err := p.run(ctx, nil, append([]string{"generate", "-f"}, opts.args(name)...)...)
		return err
	})
}

// Regenerate は pass generate -i でエントリの 1 行目だけを替える。pass 1.7.4 は
// -i と -f を同時に受け付けない（-i は上書きの確認をしない）ので、-f は付けない。
// 復号に失敗したら pass は一時ファイルを消してエントリを変えない（pipefail）。
// 失敗の後に、エントリが消えていれば ErrNotFound、ロック中なら ErrLocked に分類する。
func (p *Pass) Regenerate(ctx context.Context, name string, opts GenerateOptions) error {
	return p.write(func() error {
		if ok, err := p.Exists(ctx, name); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("regenerate %s: %w", name, ErrNotFound)
		}
		_, err := p.run(ctx, nil, append([]string{"generate", "-i"}, opts.args(name)...)...)
		if err == nil {
			return nil
		}
		if ok, _ := p.Exists(ctx, name); !ok {
			return fmt.Errorf("regenerate %s: %w", name, ErrNotFound)
		}
		if locked, lerr := p.locked(ctx, path.Dir(name)); lerr == nil && locked {
			return fmt.Errorf("regenerate %s: %w", name, ErrLocked)
		} else if lerr != nil {
			p.logger.WarnContext(ctx, "cannot get lock status", "err", lerr)
		}
		return err
	})
}
