package passuitest

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/backpaper0/passui/internal/store"
)

// RequireIntegrationEnv が "1" なら、pass や gpg がないときに統合テストを
// スキップせず失敗させる（CI で設定する）。
const RequireIntegrationEnv = "PASSUI_REQUIRE_INTEGRATION"

// integrationTools は統合テストに要るコマンド。
var integrationTools = []string{"pass", "gpg", "gpgconf", "gpg-connect-agent"}

// GPGStore は統合テストのための使い捨てのストアと GNUPGHOME。
// パスフレーズ付きの鍵で pass init してある。NewGPGStore の直後はロック状態。
type GPGStore struct {
	h *Harness
	// Dir はストアのディレクトリ（PASSWORD_STORE_DIR）。
	Dir string
	// GnupgHome は使い捨ての GNUPGHOME。
	GnupgHome string
	// KeyID は鍵のフィンガープリント。
	KeyID string

	passphrase string
	probe      string // Unlock で復号する暗号文のファイル
}

// NewGPGStore は使い捨ての GNUPGHOME とストアを作り、パスフレーズ付きの鍵を作って
// pass init する。pass や gpg がなければ t.Skip する（PASSUI_REQUIRE_INTEGRATION=1
// なら失敗にする）。後片付け（gpg-agent の停止とディレクトリの削除）は t.Cleanup で行う。
// パスフレーズはカナリアとして登録する。
func NewGPGStore(h *Harness) *GPGStore {
	t := h.t
	t.Helper()
	var missing []string
	for _, tool := range integrationTools {
		if _, err := exec.LookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}
	if len(missing) > 0 {
		if os.Getenv(RequireIntegrationEnv) == "1" {
			t.Fatalf("%s=1 ですが、統合テストに要るコマンドが見つかりません: %s", RequireIntegrationEnv, strings.Join(missing, ", "))
		}
		t.Skipf("統合テストに要るコマンドが見つからないのでスキップします: %s", strings.Join(missing, ", "))
	}

	// gpg-agent のソケットのパスが長すぎないように、短い一時ディレクトリを使う。
	root, err := os.MkdirTemp(ShortTempDir(), "passui-it-")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	g := &GPGStore{
		h:          h,
		Dir:        filepath.Join(root, "store"),
		GnupgHome:  filepath.Join(root, "gnupg"),
		passphrase: h.Canary("passphrase"),
		probe:      filepath.Join(root, "probe.gpg"),
	}
	t.Cleanup(func() {
		_ = g.command("gpgconf", "--kill", "gpg-agent").Run()
		_ = os.RemoveAll(root)
	})
	if err := os.Mkdir(g.GnupgHome, 0o700); err != nil {
		t.Fatalf("create GNUPGHOME: %v", err)
	}

	g.must(g.passphrase+"\n", "gpg", "--batch", "--pinentry-mode", "loopback", "--passphrase-fd", "0",
		"--quick-gen-key", "passui test <passui-test@example.invalid>", "future-default", "default", "never")
	g.KeyID = g.fingerprint("--list-secret-keys")
	g.must("", "pass", "init", g.KeyID)
	g.must("probe", "gpg", "--batch", "--trust-model", "always", "--encrypt", "--recipient", g.KeyID, "--output", g.probe)
	g.Lock()
	return g
}

// Env は、このストアに対して pass や gpg を呼ぶための環境を返す。プロセスの環境から
// PASSWORD_STORE_* と GNUPGHOME を除き、このストアの PASSWORD_STORE_DIR と
// GNUPGHOME を足したもの。
func (g *GPGStore) Env() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "PASSWORD_STORE_") || k == "GNUPGHOME" {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "PASSWORD_STORE_DIR="+g.Dir, "GNUPGHOME="+g.GnupgHome)
}

// Backend は、このストアに対する本物のストア バックエンドを返す。
// ログは Harness の Logger に出す（カナリア検査の対象になる）。
func (g *GPGStore) Backend() *store.Pass {
	passPath, err := exec.LookPath("pass")
	if err != nil {
		g.h.t.Fatalf("look up pass: %v", err)
	}
	return store.NewPass(store.PassConfig{
		Pass:      passPath,
		Dir:       g.Dir,
		GnupgHome: g.GnupgHome,
		Env:       g.Env(),
	}, g.h.Logger)
}

// Insert は外から `pass insert -m -f` でエントリを置く（あれば上書きする）。
// 暗号化だけなので、ロック状態は変わらない。content はカナリアにしておくとよい。
func (g *GPGStore) Insert(name, content string) {
	g.h.t.Helper()
	g.must(content, "pass", "insert", "-m", "-f", name)
}

// Show は `pass show` でエントリを復号して返す（互換の確認に使う）。
// パスフレーズを loopback で渡すので、呼んだ後は解錠状態になる。
func (g *GPGStore) Show(name string) string {
	g.h.t.Helper()
	cmd := g.command("pass", "show", name)
	cmd.Env = append(cmd.Env, "PASSWORD_STORE_GPG_OPTS=--batch --pinentry-mode loopback --passphrase-fd 0")
	cmd.Stdin = strings.NewReader(g.passphrase + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		g.h.t.Fatalf("pass show %s: %v: %s", name, err, stderr.String())
	}
	return string(out)
}

// Passphrase は鍵（KeyID）のパスフレーズを返す（カナリア）。バックアップから別の
// GNUPGHOME に戻した鍵を、元のパスフレーズで使えるか確かめるために使う。
func (g *GPGStore) Passphrase() string {
	return g.passphrase
}

// Lock は `gpgconf --reload gpg-agent` で gpg-agent のキャッシュを消し、ロック状態にする。
func (g *GPGStore) Lock() {
	g.h.t.Helper()
	g.must("", "gpgconf", "--reload", "gpg-agent")
}

// Unlock は --pinentry-mode loopback でパスフレーズを渡して 1 回だけ復号し、
// gpg-agent にキャッシュさせて解錠状態にする。
func (g *GPGStore) Unlock() {
	g.h.t.Helper()
	g.must(g.passphrase+"\n", "gpg", "--batch", "--pinentry-mode", "loopback", "--passphrase-fd", "0",
		"--decrypt", "--output", os.DevNull, g.probe)
}

// AddKey は GNUPGHOME に、パスフレーズ付きの鍵（秘密鍵あり）を 1 つ足し、指紋を返す。
// label は uid とパスフレーズのカナリアに使う。ownertrust は gpg の既定どおり ultimate になる。
func (g *GPGStore) AddKey(label string) string {
	g.h.t.Helper()
	uid := "passui " + label + " <" + label + "@example.invalid>"
	g.must(g.h.Canary(label)+"\n", "gpg", "--batch", "--pinentry-mode", "loopback", "--passphrase-fd", "0",
		"--quick-gen-key", uid, "future-default", "default", "never")
	return g.fingerprint("--list-secret-keys", "--", uid)
}

// AddPublicKey は、別の使い捨ての GNUPGHOME で作った鍵の公開鍵だけを GNUPGHOME に
// 取り込み、指紋を返す（ゲストに秘密鍵のないストアの鍵）。ownertrust は設定しない。
func (g *GPGStore) AddPublicKey(label string) string {
	g.h.t.Helper()
	t := g.h.t
	home, err := os.MkdirTemp(ShortTempDir(), "passui-pub-")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	other := &GPGStore{h: g.h, Dir: g.Dir, GnupgHome: home}
	t.Cleanup(func() {
		_ = other.command("gpgconf", "--kill", "gpg-agent").Run()
		_ = os.RemoveAll(home)
	})
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	uid := "passui " + label + " <" + label + "@example.invalid>"
	other.must("", "gpg", "--batch", "--pinentry-mode", "loopback", "--passphrase", "",
		"--quick-gen-key", uid, "future-default", "default", "never")
	pub := filepath.Join(home, "public.asc")
	other.must("", "gpg", "--batch", "--armor", "--output", pub, "--export", "--", uid)
	g.must("", "gpg", "--batch", "--import", pub)
	return g.fingerprint("--list-keys", "--", uid)
}

// Run は、このストアの環境で name を stdin 付きで実行し、stdout を返す。失敗したら
// テストを止める。stdin はシークレットでもよい（失敗のメッセージには入れない）。
func (g *GPGStore) Run(stdin, name string, args ...string) string {
	g.h.t.Helper()
	cmd := g.command(name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		g.h.t.Fatalf("%s %s: %v: %s", name, strings.Join(args, " "), err, stderr.String())
	}
	return string(out)
}

// fingerprint は gpg --with-colons <args> の最初の鍵の主鍵の指紋を返す。
func (g *GPGStore) fingerprint(args ...string) string {
	g.h.t.Helper()
	cmd := g.command("gpg", append([]string{"--batch", "--with-colons"}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		g.h.t.Fatalf("list keys: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Split(line, ":"); len(f) > 9 && f[0] == "fpr" {
			return f[9]
		}
	}
	g.h.t.Fatalf("no fingerprint in gpg output")
	return ""
}

func (g *GPGStore) command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Env = append(g.Env(), "LC_ALL=C")
	return cmd
}

// must はコマンドを stdin 付きで実行し、失敗したらテストを止める。
// stdin はシークレットなので、失敗のメッセージには入れない。
func (g *GPGStore) must(stdin string, name string, args ...string) {
	g.h.t.Helper()
	cmd := g.command(name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		g.h.t.Fatalf("%s %s: %v: %s", name, strings.Join(args, " "), err, out)
	}
}

// ShortTempDir は、GNUPGHOME にする一時ディレクトリの置き場所を返す。TMPDIR が長いと
// gpg-agent のソケットのパスが UNIX ソケットの上限を超えるので、そのときは /tmp を使う。
func ShortTempDir() string {
	dir := os.TempDir()
	if len(dir) > 40 {
		if fi, err := os.Stat("/tmp"); err == nil && fi.IsDir() {
			return "/tmp"
		}
	}
	return dir
}
