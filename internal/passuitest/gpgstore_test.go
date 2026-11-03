package passuitest_test

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

// pinentry を使わずに復号できるか（解錠中か）を、passui と同じ GPG のオプションで確かめる。
func decryptsWithoutPinentry(t *testing.T, g *passuitest.GPGStore, name string) bool {
	t.Helper()
	cmd := exec.Command("pass", "show", name)
	cmd.Env = append(g.Env(), "PASSWORD_STORE_GPG_OPTS=--batch --pinentry-mode error")
	return cmd.Run() == nil
}

func TestGPGStoreLockAndUnlock(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("a", h.Canary("password"))

	g.Lock()
	if decryptsWithoutPinentry(t, g, "a") {
		t.Fatal("decrypted after Lock, want locked")
	}

	g.Unlock()
	if !decryptsWithoutPinentry(t, g, "a") {
		t.Fatal("cannot decrypt after Unlock, want unlocked")
	}

	g.Lock()
	if decryptsWithoutPinentry(t, g, "a") {
		t.Fatal("decrypted after second Lock, want locked")
	}
}

func TestGPGStoreInsertIsReadableByPass(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	content := h.Canary("password") + "\nuser: alice"

	g.Insert("dir/entry", content)

	if got := g.Show("dir/entry"); got != content {
		t.Errorf("Show = %q, want %q", got, content)
	}
}

// skipTB は Skipf と Fatalf を記録して、そのゴルーチンを止める testing.TB。
type skipTB struct {
	testing.TB
	mu      sync.Mutex
	skipped []string
	fatals  []string
}

func (s *skipTB) Helper()        {}
func (s *skipTB) Cleanup(func()) {}
func (s *skipTB) Skipf(format string, args ...any) {
	s.mu.Lock()
	s.skipped = append(s.skipped, fmt.Sprintf(format, args...))
	s.mu.Unlock()
	runtime.Goexit()
}
func (s *skipTB) Fatalf(format string, args ...any) {
	s.mu.Lock()
	s.fatals = append(s.fatals, fmt.Sprintf(format, args...))
	s.mu.Unlock()
	runtime.Goexit()
}

// runHelper は f を別のゴルーチンで動かし、Skipf や Fatalf で止まるのを待つ。
func runHelper(f func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	<-done
}

func TestGPGStoreSkipsWithoutTools(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PASSUI_REQUIRE_INTEGRATION", "")
	tb := &skipTB{TB: t}

	runHelper(func() { passuitest.NewGPGStore(passuitest.New(tb)) })

	if len(tb.skipped) != 1 || len(tb.fatals) != 0 {
		t.Errorf("skipped = %v, fatals = %v, want one skip", tb.skipped, tb.fatals)
	}
}

func TestGPGStoreFailsWithoutToolsWhenIntegrationIsRequired(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PASSUI_REQUIRE_INTEGRATION", "1")
	tb := &skipTB{TB: t}

	runHelper(func() { passuitest.NewGPGStore(passuitest.New(tb)) })

	if len(tb.fatals) != 1 || len(tb.skipped) != 0 {
		t.Errorf("skipped = %v, fatals = %v, want one fatal", tb.skipped, tb.fatals)
	}
	if len(tb.fatals) == 1 && !strings.Contains(tb.fatals[0], "PASSUI_REQUIRE_INTEGRATION") {
		t.Errorf("fatal message does not mention PASSUI_REQUIRE_INTEGRATION: %q", tb.fatals[0])
	}
}
