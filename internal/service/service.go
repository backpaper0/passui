// Package service は passui を systemd ユーザーサービスとして登録・解除する
// （install-service と uninstall-service）。
//
// systemctl --user、loginctl、unit の置き場所、pass の探索は System で隠す。
// sudo が要る変更はせず、必要な操作を表示するだけにする。env ファイルには触らない。
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/backpaper0/passui/internal/secret"
)

// System は install-service が使う OS の機能。
type System interface {
	// CheckUserManager は systemd のユーザーマネージャに繋がるかを確かめる。
	CheckUserManager(ctx context.Context) error
	// IsWSL は WSL2 の中で動いているかを返す（エラーの案内に使う）。
	IsWSL() bool
	// Linger は loginctl の linger が有効かを返す。
	Linger(ctx context.Context) (bool, error)
	// UnitEnabled は systemctl --user is-enabled が enabled かを返す。
	UnitEnabled(ctx context.Context, name string) (bool, error)
	// Systemctl は systemctl --user に args を渡して実行する。
	Systemctl(ctx context.Context, args ...string) error
	// UnitDir はユーザー unit の置き場所（~/.config/systemd/user）を返す。
	UnitDir() (string, error)
	// LookPass は pass の絶対パスを返す。見つからなければエラー。
	LookPass() (string, error)
	// Executable は動いている passui の実体の絶対パス（シンボリックリンクを解決したもの）を返す。
	Executable() (string, error)
	// Exists はファイルがあるかを返す。
	Exists(path string) bool
}

// Options は Install と Uninstall の入力。
type Options struct {
	System System
	// Env はシェルの環境変数。
	Env    map[string]string
	Stdout io.Writer
	Stderr io.Writer
	// LoginURL は動いている passui のログイン URL を読む（passui login と同じ）。
	LoginURL func() (secret.Value, error)
	// WaitTimeout は起動後にログイン URL が現れるのを待つ時間。0 なら DefaultWaitTimeout。
	WaitTimeout time.Duration
}

// DefaultWaitTimeout は起動後にログイン URL が現れるのを待つ既定の時間。
const DefaultWaitTimeout = 10 * time.Second

// Install は unit を書き、daemon-reload、enable、restart を行い、ログイン URL を出す。
// 終了コードを返す。
func Install(ctx context.Context, o Options) int {
	sys := o.System
	if code, ok := checkUserManager(ctx, o); !ok {
		return code
	}
	shimsDir := miseShimsDir(o.Env)

	exec := filepath.Join(shimsDir, "passui")
	if !sys.Exists(exec) {
		p, err := sys.Executable()
		if err != nil {
			return fail(o, "実行ファイルの場所が分かりません: %v", err)
		}
		exec = p
		warn(o, "mise の shim（%s）がないので、実行ファイルのパス %s を unit に書きます。"+
			"passui を更新してパスが変わったら、もう一度 passui install-service を実行してください。", filepath.Join(shimsDir, "passui"), exec)
	}

	pass, err := sys.LookPass()
	if err != nil {
		return fail(o, "pass が見つかりません。先に pass を入れてください: %v", err)
	}

	warnPreconditions(ctx, o)

	unit := Unit(UnitParams{
		Exec: exec,
		Env:  copiedEnv(o.Env),
		Path: buildPath(sys, o.Env, filepath.Dir(pass)),
	})

	dir, err := sys.UnitDir()
	if err != nil {
		return fail(o, "unit の置き場所が分かりません: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail(o, "%v", err)
	}
	path := filepath.Join(dir, UnitName)
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return fail(o, "%v", err)
	}
	fmt.Fprintf(o.Stdout, "unit を書きました: %s\n", path)

	// 起動前のログイン URL を覚えておき、新しい serve が書き直すのを待つ。
	// トークンは起動ごとに変わるので、違う URL が読めたら新しい serve が書いたものである。
	before, _ := o.LoginURL()

	// restart は止まっていれば起動し、動いていれば再起動する。
	for _, args := range [][]string{{"daemon-reload"}, {"enable", UnitName}, {"restart", UnitName}} {
		if err := sys.Systemctl(ctx, args...); err != nil {
			return fail(o, "systemctl --user %s に失敗しました: %v", strings.Join(args, " "), err)
		}
	}
	fmt.Fprintln(o.Stdout, "passui を起動しました。")

	u, ok := waitLoginURL(ctx, o, before)
	if !ok {
		return fail(o, "passui のログイン URL が現れません。ログを確かめてください: journalctl --user -u passui")
	}
	fmt.Fprintln(o.Stdout, "ホストのブラウザで次の URL を開いてください:")
	fmt.Fprintln(o.Stdout, u.Reveal())
	return 0
}

// Uninstall は disable --now、unit の削除、daemon-reload を行う。終了コードを返す。
// linger と gpg-agent.socket、env ファイルには触らない。
func Uninstall(ctx context.Context, o Options) int {
	sys := o.System
	if code, ok := checkUserManager(ctx, o); !ok {
		return code
	}
	dir, err := sys.UnitDir()
	if err != nil {
		return fail(o, "unit の置き場所が分かりません: %v", err)
	}
	path := filepath.Join(dir, UnitName)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(o.Stdout, "passui はサービスとして登録されていません（%s がありません）。\n", path)
		return 0
	}
	if err := sys.Systemctl(ctx, "disable", "--now", UnitName); err != nil {
		return fail(o, "systemctl --user disable --now %s に失敗しました: %v", UnitName, err)
	}
	if err := os.Remove(path); err != nil {
		return fail(o, "%v", err)
	}
	fmt.Fprintf(o.Stdout, "unit を削除しました: %s\n", path)
	if err := sys.Systemctl(ctx, "daemon-reload"); err != nil {
		return fail(o, "systemctl --user daemon-reload に失敗しました: %v", err)
	}
	fmt.Fprintln(o.Stdout, "passui のサービスを取り除きました。linger、gpg-agent.socket、~/.config/passui/env はそのままです。")
	return 0
}

// waitLoginURL は before と違うログイン URL が読めるまで待つ。
func waitLoginURL(ctx context.Context, o Options, before secret.Value) (secret.Value, bool) {
	timeout := o.WaitTimeout
	if timeout <= 0 {
		timeout = DefaultWaitTimeout
	}
	deadline := time.Now().Add(timeout)
	for {
		if u, err := o.LoginURL(); err == nil && !u.IsZero() && !before.Equal(u.Reveal()) {
			return u, true
		}
		if time.Now().After(deadline) {
			return secret.Value{}, false
		}
		select {
		case <-ctx.Done():
			return secret.Value{}, false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// checkUserManager は systemd のユーザーマネージャに繋がるかを確かめる。
// 繋がらなければ案内を出し、終了コードと false を返す。
func checkUserManager(ctx context.Context, o Options) (int, bool) {
	err := o.System.CheckUserManager(ctx)
	if err == nil {
		return 0, true
	}
	fmt.Fprintf(o.Stderr, "passui: systemd のユーザーセッションに繋がりません（systemctl --user）: %v\n", err)
	if o.System.IsWSL() {
		fmt.Fprint(o.Stderr, `WSL2 では systemd を有効にする必要があります。ゲストで /etc/wsl.conf に次を書き（sudo が要ります）:

  [boot]
  systemd=true

Windows 側で wsl --shutdown を実行してからゲストを開き直し、もう一度 passui install-service を実行してください。
`)
	}
	return 1, false
}

// warnPreconditions は、なくても登録はできるが困ることがある前提条件を警告する。
func warnPreconditions(ctx context.Context, o Options) {
	sys := o.System
	if on, err := sys.Linger(ctx); err != nil {
		warn(o, "linger が有効か確かめられませんでした: %v", err)
	} else if !on {
		warn(o, "linger が無効なので、ゲストの起動時に passui が立ち上がりません。有効にするには: loginctl enable-linger")
	}
	if on, err := sys.UnitEnabled(ctx, "gpg-agent.socket"); err != nil {
		warn(o, "gpg-agent.socket が有効か確かめられませんでした: %v", err)
	} else if !on {
		warn(o, "gpg-agent.socket が enabled ではありません。passui の再起動で gpg-agent が止まり、"+
			"ターミナルの pass までロックされることがあります。有効にするには: systemctl --user enable gpg-agent.socket")
	}
	if h := o.Env["GNUPGHOME"]; h != "" && filepath.Clean(h) != filepath.Join(o.Env["HOME"], ".gnupg") {
		warn(o, "GNUPGHOME が既定以外（%s）です。gpg-agent.socket などのソケットユニットは既定の GNUPGHOME の"+
			"ソケットしか扱わないので、passui とターミナルの pass が同じ gpg-agent を使うか確かめてください。", h)
	}
}

// copiedEnv はシェルから unit に書き写す変数（GNUPGHOME と PASSWORD_STORE_*）を名前順で返す。
func copiedEnv(env map[string]string) []EnvVar {
	var vars []EnvVar
	for k, v := range env {
		if k == "GNUPGHOME" || strings.HasPrefix(k, "PASSWORD_STORE_") {
			vars = append(vars, EnvVar{k, v})
		}
	}
	slices.SortFunc(vars, func(a, b EnvVar) int { return strings.Compare(a.Name, b.Name) })
	return vars
}

// miseShimsDir は mise の shims のディレクトリを返す（mise の既定の場所の決め方に合わせる）。
func miseShimsDir(env map[string]string) string {
	return filepath.Join(miseDataDir(env), "shims")
}

func miseDataDir(env map[string]string) string {
	if d := env["MISE_DATA_DIR"]; d != "" {
		return d
	}
	if d := env["XDG_DATA_HOME"]; d != "" {
		return filepath.Join(d, "mise")
	}
	return filepath.Join(env["HOME"], ".local", "share", "mise")
}

// buildPath は unit の PATH を組み立てる。mise の shims（あれば）、システムのパスの順に並べ、
// pass がそのどちらにもなく mise のインストール先でもない場所（~/.nix-profile/bin など）にあれば、
// そのディレクトリを最後に足す。シェルの PATH は使わない（mise activate の一時的なパスを入れないため）。
func buildPath(sys System, env map[string]string, passDir string) string {
	var dirs []string
	shimsDir := miseShimsDir(env)
	if sys.Exists(shimsDir) {
		dirs = append(dirs, shimsDir)
	}
	dirs = append(dirs, filepath.SplitList(systemPath)...)
	installs := filepath.Join(miseDataDir(env), "installs") + string(filepath.Separator)
	if !slices.Contains(dirs, passDir) && !strings.HasPrefix(passDir, installs) {
		dirs = append(dirs, passDir)
	}
	return strings.Join(dirs, ":")
}

func warn(o Options, format string, args ...any) {
	fmt.Fprintf(o.Stderr, "警告: "+format+"\n", args...)
}

func fail(o Options, format string, args ...any) int {
	fmt.Fprintf(o.Stderr, "passui: "+format+"\n", args...)
	return 1
}
