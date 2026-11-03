package cli_test

import (
	"strings"
	"testing"
)

// install-service の振る舞いは internal/service のフェイクで確かめる。ここでは本物の systemd を
// 操作しないよう、ヘルプに載っていることだけを見る。
func TestHelpListsServiceCommands(t *testing.T) {
	r := run(t, nil)
	for _, s := range []string{"install-service", "uninstall-service"} {
		if !strings.Contains(r.stdout, s) {
			t.Errorf("help lacks %s:\n%s", s, r.stdout)
		}
	}
}
