package passuitest_test

import (
	"fmt"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

// recordingTB は Errorf と Cleanup を記録する testing.TB。
type recordingTB struct {
	testing.TB
	cleanups []func()
	errors   []string
}

func (r *recordingTB) Helper()          {}
func (r *recordingTB) Cleanup(f func()) { r.cleanups = append(r.cleanups, f) }
func (r *recordingTB) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}
func (r *recordingTB) runCleanups() {
	for i := len(r.cleanups) - 1; i >= 0; i-- {
		r.cleanups[i]()
	}
}

func TestCanaryInLogsFailsTest(t *testing.T) {
	tb := &recordingTB{TB: t}
	h := passuitest.New(tb)
	secret := h.Canary("secret")

	h.Logger.Info("oops", "value", secret)
	tb.runCleanups()

	if len(tb.errors) != 1 {
		t.Fatalf("errors = %v, want exactly one", tb.errors)
	}
}

func TestCanaryNotInLogsPasses(t *testing.T) {
	tb := &recordingTB{TB: t}
	h := passuitest.New(tb)
	h.Canary("secret")

	h.Logger.Info("harmless")
	tb.runCleanups()

	if len(tb.errors) != 0 {
		t.Fatalf("errors = %v, want none", tb.errors)
	}
}

func TestDebugLogsAreCollected(t *testing.T) {
	tb := &recordingTB{TB: t}
	h := passuitest.New(tb)
	secret := h.Canary("debug")

	h.Logger.Debug("debug line", "value", secret)
	tb.runCleanups()

	if len(tb.errors) != 1 {
		t.Fatalf("errors = %v, want a leak reported at debug level", tb.errors)
	}
}
