package cli

import (
	"context"
	"io"

	"github.com/backpaper0/passui/internal/secret"
	"github.com/backpaper0/passui/internal/service"
)

// serviceOptions は install-service と uninstall-service に渡す入力を組み立てる。
func serviceOptions(env environ, stdout, stderr io.Writer) service.Options {
	return service.Options{
		System: service.NewSystem(env),
		Env:    env,
		Stdout: stdout,
		Stderr: stderr,
		LoginURL: func() (secret.Value, error) {
			dir, err := runtimeDir(env)
			if err != nil {
				return secret.Value{}, err
			}
			return readLoginURL(dir)
		},
	}
}

func installService(ctx context.Context, env environ, stdout, stderr io.Writer) int {
	return service.Install(ctx, serviceOptions(env, stdout, stderr))
}

func uninstallService(ctx context.Context, env environ, stdout, stderr io.Writer) int {
	return service.Uninstall(ctx, serviceOptions(env, stdout, stderr))
}
