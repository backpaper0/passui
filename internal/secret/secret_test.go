package secret_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/secret"
)

func TestValueIsRedactedInLogsAndFormatting(t *testing.T) {
	v := secret.Of("CANARYsecret")
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "token", v)
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "token", v)
	fmt.Fprintf(&buf, "%v %s %+v %#v %q", v, v, v, v, v)
	fmt.Fprint(&buf, v, []secret.Value{v}, struct{ T secret.Value }{v})

	out := buf.String()
	if strings.Contains(out, "CANARYsecret") {
		t.Errorf("secret leaked: %s", out)
	}
	if !strings.Contains(out, "token=[REDACTED]") {
		t.Errorf("log does not show [REDACTED]: %s", out)
	}
}

func TestReveal(t *testing.T) {
	if got := secret.Of("abc").Reveal(); got != "abc" {
		t.Errorf("Reveal = %q, want abc", got)
	}
}

func TestNewIsRandomAndLongEnough(t *testing.T) {
	a, b := secret.New(), secret.New()
	if len(a.Reveal()) < 32 {
		t.Errorf("len = %d, want >= 32", len(a.Reveal()))
	}
	if a.Reveal() == b.Reveal() {
		t.Error("New returned the same value twice")
	}
}

func TestEqual(t *testing.T) {
	v := secret.Of("abc")
	for _, tc := range []struct {
		in   string
		want bool
	}{{"abc", true}, {"abd", false}, {"ab", false}, {"abcd", false}, {"", false}} {
		if got := v.Equal(tc.in); got != tc.want {
			t.Errorf("Equal(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	if secret.Of("").Equal("") {
		t.Error("an empty secret must not match anything")
	}
}
