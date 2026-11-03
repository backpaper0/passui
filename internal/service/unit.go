package service

import (
	"strings"
)

// UnitName は passui の systemd ユーザー unit の名前。
const UnitName = "passui.service"

// systemPath は unit の PATH に入れるシステムのパス。
const systemPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// EnvVar は unit の Environment= に書く 1 つの変数。
type EnvVar struct {
	Name, Value string
}

// UnitParams は unit の生成に使う値。
type UnitParams struct {
	// Exec は ExecStart に書く実行ファイルの絶対パス（mise の shim か実体）。
	Exec string
	// Env はシェルから書き写す変数（GNUPGHOME、PASSWORD_STORE_*）。この順で書く。
	Env []EnvVar
	// Path は組み立てた PATH。
	Path string
}

// Unit は passui.service の中身を返す純関数。
func Unit(p UnitParams) string {
	var b strings.Builder
	b.WriteString(`# passui install-service が書き出した unit。次の install-service で上書きされるので、手で編集しない。
# 設定（PASSUI_PORT など）は ~/.config/passui/env に書く。install-service はこのファイルに触らない。
[Unit]
Description=passui - password-store をブラウザから管理する Web ツール
Documentation=https://github.com/backpaper0/passui

[Service]
`)
	// ExecStart では $ も展開されるので $$ にする。
	b.WriteString("ExecStart=" + quote(strings.ReplaceAll(p.Exec, "$", "$$")) + " serve\n")
	b.WriteString("WorkingDirectory=%h\n")
	// EnvironmentFile の値は Environment= より優先される（systemd.exec(5)）。
	b.WriteString("EnvironmentFile=-%E/passui/env\n")
	for _, v := range p.Env {
		b.WriteString("Environment=" + quote(v.Name+"="+v.Value) + "\n")
	}
	b.WriteString("Environment=" + quote("PATH="+p.Path) + "\n")
	b.WriteString(`Restart=on-failure
NoNewPrivileges=yes
PrivateTmp=yes

[Install]
WantedBy=default.target
`)
	return b.String()
}

// quote は systemd の unit の値を二重引用符で囲む。% は指定子にならないよう %% にし、
// \ と " と改行はエスケープする。
func quote(s string) string {
	r := strings.NewReplacer(`%`, `%%`, `\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}
