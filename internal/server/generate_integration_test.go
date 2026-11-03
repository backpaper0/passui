package server_test

import (
	"maps"
	"net/http"
	"strings"
	"testing"
	"unicode"

	"github.com/backpaper0/passui/internal/passuitest"
)

func TestIntegrationGeneratedEntryHasOnlyPasswordOfGivenLength(t *testing.T) {
	for _, tc := range []struct {
		name      string
		noSymbols bool
	}{
		{"with symbols", false},
		{"without symbols", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := passuitest.New(t)
			g := passuitest.NewGPGStore(h)
			g.Lock() // 生成は暗号化だけなので、ロック中でもできる
			srv := h.Serve(passuitest.Options{Store: g.Backend()})

			res := postGenerate(srv, "email/work", "40", tc.noSymbols)

			if res.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, readBody(t, res))
			}
			got := g.Show("email/work")
			password, ok := strings.CutSuffix(got, "\n")
			if !ok || strings.Contains(password, "\n") {
				t.Fatalf("entry is not a single line: %d bytes", len(got))
			}
			h.AddCanary(password)
			if len(password) != 40 {
				t.Errorf("password length = %d, want 40", len(password))
			}
			if tc.noSymbols && strings.IndexFunc(password, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) >= 0 {
				t.Errorf("password contains a symbol")
			}
		})
	}
}

func TestIntegrationGenerateUsesPassDefaultLength(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	srv := h.Serve(passuitest.Options{Store: g.Backend()})

	res := postGenerate(srv, "bank", "", false)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, readBody(t, res))
	}
	password := strings.TrimSuffix(g.Show("bank"), "\n")
	h.AddCanary(password)
	if len(password) != 25 { // pass の既定（PASSWORD_STORE_GENERATED_LENGTH がないとき）
		t.Errorf("password length = %d, want 25", len(password))
	}
}

func TestIntegrationGenerateRejectsTakenDestinationsAndKeepsStore(t *testing.T) {
	for _, tc := range []struct {
		name, path, message string
	}{
		{"existing entry", "email/work", "同じ名前のエントリが既にあります"},
		{"directory with the same name", "email", "同じ名前のフォルダがあります"},
		{"trailing slash", "email/", "パスの末尾が / です"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := passuitest.New(t)
			g := passuitest.NewGPGStore(h)
			g.Insert("email/work", h.Canary("original"))
			srv := h.Serve(passuitest.Options{Store: g.Backend()})
			before := snapshot(t, g.Dir)

			res := postGenerate(srv, tc.path, "", false)

			if res.StatusCode != http.StatusConflict {
				t.Errorf("status = %d, want 409", res.StatusCode)
			}
			if body := readBody(t, res); !strings.Contains(body, tc.message) {
				t.Errorf("body does not contain %q:\n%s", tc.message, body)
			}
			if after := snapshot(t, g.Dir); !maps.Equal(before, after) {
				t.Errorf("store changed:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

func TestIntegrationRegenerateReplacesOnlyFirstLine(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	old := h.Canary("old")
	rest := "user: " + h.Canary("user") + "\nurl: https://example.com\n"
	g.Insert("email/work", old+"\n"+rest)
	g.Unlock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})

	res := postRegenerate(srv, "email/work")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, readBody(t, res))
	}
	first, gotRest, _ := strings.Cut(g.Show("email/work"), "\n")
	h.AddCanary(first) // 生成したパスワードもログに出ていないこと
	if first == old || len(first) != 25 {
		t.Errorf("first line was not regenerated with the default length (len %d)", len(first))
	}
	if gotRest != rest {
		t.Errorf("rest = %q, want %q", gotRest, rest)
	}
}

func TestIntegrationRegenerateWhileLockedShowsGuidanceAndKeepsEntry(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("email/work", h.Canary("old")+"\nuser: alice\n")
	g.Lock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})
	before := snapshot(t, g.Dir)

	res := postRegenerate(srv, "email/work")

	if res.StatusCode != http.StatusLocked {
		t.Errorf("status = %d, want 423", res.StatusCode)
	}
	body := readBody(t, res)
	for _, want := range []string{"ロック中です", "pass show email/work &gt;/dev/null"} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q:\n%s", want, body)
		}
	}
	if after := snapshot(t, g.Dir); !maps.Equal(before, after) {
		t.Errorf("store changed:\nbefore %v\nafter  %v", before, after)
	}
}

func TestIntegrationRegenerateMissingEntryShowsNotFoundAndReloadsTree(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Unlock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})

	res := postRegenerate(srv, "email/gone")

	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.StatusCode)
	}
	if body := readBody(t, res); !strings.Contains(body, "見つかりません") {
		t.Errorf("body does not contain 見つかりません:\n%s", body)
	}
	if got := res.Header.Get("HX-Trigger"); got != "tree-changed" {
		t.Errorf("HX-Trigger = %q, want tree-changed", got)
	}
}
