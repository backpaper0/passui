package server_test

import (
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

func TestIntegrationCreatedEntryIsReadableWithPass(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Unlock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})
	content := h.Canary("password") + "\nuser: alice\nurl: https://example.com"

	res := postCreate(srv, "email/work", content)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, readBody(t, res))
	}
	// 端末で pass insert -m したときと同じく、末尾に改行が付く。
	if got := g.Show("email/work"); got != content+"\n" {
		t.Errorf("pass show = %q, want %q", got, content+"\n")
	}
}

func TestIntegrationCreateWorksWhileLocked(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Lock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})
	content := h.Canary("password")

	res := postCreate(srv, "bank", content)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, readBody(t, res))
	}
	if got := g.Show("bank"); got != content+"\n" {
		t.Errorf("pass show = %q, want %q", got, content+"\n")
	}
}

func TestIntegrationCreateRejectsTakenDestinationsAndKeepsStore(t *testing.T) {
	for _, tc := range []struct {
		name, path, message string
	}{
		{"existing entry", "email/work", "同じ名前のエントリが既にあります"},
		{"directory with the same name", "email", "同じ名前のフォルダがあります"},
		{"trailing slash", "email/", "パスの末尾が / です"},
		{"trailing slash of new folder", "new/", "パスの末尾が / です"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := passuitest.New(t)
			g := passuitest.NewGPGStore(h)
			original := h.Canary("original")
			g.Insert("email/work", original)
			srv := h.Serve(passuitest.Options{Store: g.Backend()})
			before := snapshot(t, g.Dir)

			res := postCreate(srv, tc.path, h.Canary("password"))

			if res.StatusCode != http.StatusConflict {
				t.Errorf("status = %d, want 409", res.StatusCode)
			}
			if body := readBody(t, res); !strings.Contains(body, tc.message) {
				t.Errorf("body does not contain %q:\n%s", tc.message, body)
			}
			if after := snapshot(t, g.Dir); !maps.Equal(before, after) {
				t.Errorf("store changed:\nbefore %v\nafter  %v", before, after)
			}
			if got := g.Show("email/work"); got != original {
				t.Errorf("existing entry changed: %q", got)
			}
		})
	}
}

// snapshot はストアのディレクトリの中身（ディレクトリと、ファイルの相対パスと中身）を返す。
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			files[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		files[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
