package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Lock は、暗号化に使える秘密鍵（主鍵と副鍵）すべての keygrip について、
// gpg-agent に CLEAR_PASSPHRASE を送り、解錠のキャッシュを消す。ストアのルートの
// .gpg-id の鍵だけでなく、サブフォルダの .gpg-id の鍵もロックするため、すべての
// 秘密鍵を対象にする。キャッシュされていない鍵に送っても失敗しない。
// gpg-agent のキャッシュはターミナルと共有なので、ターミナルの pass もロックされる。
func (p *Pass) Lock(ctx context.Context) error {
	grips, err := p.keygrips(ctx, nil)
	if err != nil {
		return err
	}
	if len(grips) == 0 {
		return errors.New("no secret key to lock")
	}
	args := make([]string, 0, len(grips)+1)
	for _, g := range grips {
		args = append(args, "CLEAR_PASSPHRASE --mode=normal "+g)
	}
	out, err := p.exec(ctx, p.tool("gpg-connect-agent"), nil, append(args, "/bye")...)
	if err != nil {
		return err
	}
	// gpg-connect-agent は、コマンドが ERR を返しても終了コード 0 で終わる。
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "ERR ") {
			p.logger.WarnContext(ctx, "gpg-connect-agent failed", "args", args, "response", line)
			return fmt.Errorf("clear passphrase: %s", line)
		}
	}
	return nil
}
