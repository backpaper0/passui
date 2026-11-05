package backup

import (
	"os"
	"strings"
	"testing"
)

const (
	stepsBegin = "<!-- backup-steps:begin -->\n"
	stepsEnd   = "<!-- backup-steps:end -->"
)

// README.md の復元手順は、README.txt と同じテンプレートを置き場所の印で描画したものと
// 一字一句同じ。
func TestReadmeMarkdownHasRestoreStepsRenderedWithPlaceholders(t *testing.T) {
	md, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(md), stepsBegin)
	if !ok {
		t.Fatalf("README.md lacks %q", stepsBegin)
	}
	section, _, ok := strings.Cut(rest, stepsEnd)
	if !ok {
		t.Fatalf("README.md lacks %q", stepsEnd)
	}

	want, err := renderRestoreSteps(placeholderSteps)
	if err != nil {
		t.Fatal(err)
	}
	if section != want {
		t.Errorf("README.md backup-steps differ from the template.\n--- README.md\n%s\n--- template\n%s", section, want)
	}
}

// 秘密鍵が 1 つもないバックアップでは、.key を写す手順は飛ばすと書き、
// 秘密鍵を確かめるコマンドも書かない。
func TestRestoreStepsSkipSecretKeysWhenBackupHasNone(t *testing.T) {
	got, err := renderRestoreSteps(restoreSteps{
		Name: "passui-backup-20261105-090807",
		Keys: []storeKey{{Fingerprint: "0123456789ABCDEF0123456789ABCDEF01234567"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "このバックアップには秘密鍵が入っていないので、飛ばします。") {
		t.Errorf("steps do not say the secret keys are skipped:\n%s", got)
	}
	for _, s := range []string{"install -m 600", "--list-secret-keys", "gpg --batch -e"} {
		if strings.Contains(got, s) {
			t.Errorf("steps have %q though there is no secret key:\n%s", s, got)
		}
	}
	if !strings.Contains(got, "tar -xzf passui-backup-20261105-090807.tar.gz\n") {
		t.Errorf("steps lack the archive name:\n%s", got)
	}
}

// ストアの鍵が 1 つもないバックアップ（.gpg-id のないストア）では、空の public-keys.asc を
// 取り込もうとして gpg が失敗しないよう、鍵を取り込む手順はどれも飛ばすと書く。
func TestRestoreStepsSkipKeysWhenBackupHasNone(t *testing.T) {
	got, err := renderRestoreSteps(restoreSteps{Name: "passui-backup-20261105-090807"})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, "このバックアップにはストアの鍵が入っていないので、飛ばします。"); n != 2 {
		t.Errorf("steps say the store keys are skipped %d times, want 2 (public keys and ownertrust):\n%s", n, got)
	}
	for _, s := range []string{"--import", "pass show", "gpgconf"} {
		if strings.Contains(got, s) {
			t.Errorf("steps have %q though there is no store key:\n%s", s, got)
		}
	}
	if !strings.Contains(got, "cp -a store ") {
		t.Errorf("steps lack putting the store:\n%s", got)
	}
}
