// Command passui は、ゲスト上のストアをホストのブラウザから管理する Web ツールである。
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/backpaper0/passui/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
