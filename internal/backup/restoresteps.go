package backup

import (
	"bytes"
	_ "embed"
	"slices"
)

//go:embed restore-steps.tmpl
var restoreStepsText string

// restoreSteps は復元の手順のテンプレートに渡す。README.txt ではそのバックアップの値で、
// README.md では置き場所の印（placeholderSteps）で描画する。
type restoreSteps struct {
	// Name はバックアップの名前（.tar.gz を除いたファイル名。展開したディレクトリの名前）。
	Name string
	// Keys はストアの鍵。
	Keys []storeKey
}

// HasSecrets は、秘密鍵（.key）のあるストアの鍵があるか。
func (s restoreSteps) HasSecrets() bool {
	return slices.ContainsFunc(s.Keys, func(k storeKey) bool { return len(k.Secrets) > 0 })
}

// placeholderSteps は、README.md に載せる手順のための置き場所の印。
var placeholderSteps = restoreSteps{
	Name: "passui-backup-<日時>",
	Keys: []storeKey{{Fingerprint: "<指紋>", Secrets: []secretKey{{Keygrip: "<keygrip>"}}}},
}

func renderRestoreSteps(s restoreSteps) (string, error) {
	var buf bytes.Buffer
	if err := readmeTemplate.ExecuteTemplate(&buf, "restore-steps", s); err != nil {
		return "", err
	}
	return buf.String(), nil
}
