// Package cli は passui のサブコマンドを実装する。
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/backpaper0/passui/internal/secret"
	"github.com/backpaper0/passui/internal/server"
)

// Version は passui のバージョン。リリース時に -ldflags "-X" で埋め込む。
var Version = "dev"

const help = `passui - password-store をブラウザから管理する Web ツール

使い方:
  passui <サブコマンド>

サブコマンド:
  serve     サーバーを起動する（127.0.0.1 で待ち受ける）
  login     動いている passui のログイン URL を表示する
  install-service
            systemd ユーザーサービスとして登録して起動し、ログイン URL を表示する
            （何度実行してもよい。passui を更新した後にも実行する）
  uninstall-service
            サービスを止めて登録を取り除く
  version   バージョンを表示する

設定（環境変数）:
  PASSUI_PORT       待ち受けるポート（既定: 3560）
  PASSUI_LOG_LEVEL  info または debug（既定: info）
  サービスでは ~/.config/passui/env に KEY=VALUE で書く。
`

// Run は passui を実行して終了コードを返す。env は "KEY=VALUE" の一覧で、
// os.Environ() の代わりにこれだけを見る。ctx が終わると serve は止まる。
func Run(ctx context.Context, args []string, env []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, help)
		return 0
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, help)
		return 0
	case "version":
		fmt.Fprintf(stdout, "passui %s\n", Version)
		return 0
	case "serve":
		return serve(ctx, parseEnviron(env), stderr)
	case "login":
		return login(parseEnviron(env), stdout, stderr)
	case "install-service":
		return installService(ctx, parseEnviron(env), stdout, stderr)
	case "uninstall-service":
		return uninstallService(ctx, parseEnviron(env), stdout, stderr)
	default:
		fmt.Fprintf(stderr, "passui: 不明なサブコマンドです: %s\n\n%s", args[0], help)
		return 2
	}
}

// newLogger は stderr に時刻なしの slog の text 形式で出すロガーを作る。
// journald が時刻を付けるので、passui 自身は時刻を出さない。
func newLogger(w io.Writer, level slog.Leveler) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))
}

func serve(ctx context.Context, env environ, stderr io.Writer) int {
	level := new(slog.LevelVar)
	logger := newLogger(stderr, level)

	cfg, unknown, err := loadConfig(env)
	for _, k := range unknown {
		logger.Warn("unknown environment variable", "name", k)
	}
	if err != nil {
		logger.Error("invalid configuration", "err", err)
		return 1
	}
	level.Set(cfg.LogLevel)

	passPath, err := lookPath(env, "pass")
	if err != nil {
		logger.Error("pass not found", "err", err)
		return 1
	}
	logger.Debug("found pass", "path", passPath)

	dir, err := runtimeDir(env)
	if err != nil {
		logger.Error("no runtime directory", "err", err)
		return 1
	}

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Error("cannot listen", "addr", addr, "err", err)
		return 1
	}

	// トークンは待ち受けに成功してから書く。ポートが使用中で失敗したときに、
	// 動いているほかの passui のトークンを上書きしないため。
	token := secret.New()
	url := fmt.Sprintf("http://passui.localhost:%d/", cfg.Port)
	if err := writeRuntimeFiles(dir, token, url); err != nil {
		ln.Close()
		logger.Error("cannot write login token", "dir", dir, "err", err)
		return 1
	}
	// 正常に止まるときにトークンを消す。
	defer func() {
		if err := removeRuntimeFiles(dir); err != nil {
			logger.Warn("cannot remove login token", "dir", dir, "err", err)
		}
	}()

	srv := &http.Server{
		Handler:           server.New(server.Config{Port: cfg.Port, Token: token}, logger),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	logger.Info("listening", "addr", addr, "url", url)

	select {
	case err := <-errc:
		logger.Error("server stopped", "err", err)
		return 1
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", "err", err)
		return 1
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "err", err)
		return 1
	}
	logger.Info("stopped")
	return 0
}
