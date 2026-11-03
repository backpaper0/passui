package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReleaseBuildEmbedsVersion は、.goreleaser.yaml の ldflags でビルドした
// バイナリの `passui version` が、リリースのバージョンを出すことを確かめる。
// ldflags の -X の変数名がずれると、リリースでも "dev" と出てしまう。
func TestReleaseBuildEmbedsVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("バイナリをビルドするので -short では飛ばす")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go が PATH にない")
	}

	const version = "9.8.7"
	flags := releaseLdflags(t, filepath.Join("..", "..", ".goreleaser.yaml"))
	for i, f := range flags {
		f = strings.ReplaceAll(f, "{{ .Version }}", version)
		flags[i] = strings.ReplaceAll(f, "{{.Version}}", version)
	}

	bin := filepath.Join(t.TempDir(), "passui")
	build := exec.Command(goBin, "build", "-ldflags", strings.Join(flags, " "), "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		t.Fatalf("passui version: %v", err)
	}
	if got, want := string(out), "passui 9.8.7\n"; got != want {
		t.Errorf("passui version = %q, want %q", got, want)
	}
}

// releaseLdflags は .goreleaser.yaml の `ldflags:` の下に並んだ項目を返す。
// 標準ライブラリだけで読むため、ブロック形式の一覧だけを扱う。
func releaseLdflags(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var flags []string
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "ldflags:":
			in = true
		case in && strings.HasPrefix(line, "- "):
			flags = append(flags, strings.Trim(strings.TrimPrefix(line, "- "), `"'`))
		case in && line != "" && !strings.HasPrefix(line, "#"):
			in = false
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(flags) == 0 {
		t.Fatalf("%s に ldflags がない", path)
	}
	return flags
}
