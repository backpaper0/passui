package server_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

var errorIDPattern = regexp.MustCompile(`エラー ID: ([0-9a-f]{16})`)

func TestPanicReturnsErrorIDAndLogsIt(t *testing.T) {
	h := passuitest.New(t)
	srv := h.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	res := srv.Get("/")

	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", res.StatusCode)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	body := readBody(t, res)
	if !strings.Contains(body, "操作に失敗しました") {
		t.Errorf("body lacks the fixed message: %s", body)
	}
	if strings.Contains(body, "boom") {
		t.Errorf("body leaks the panic value: %s", body)
	}
	m := errorIDPattern.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("body lacks an error ID: %s", body)
	}
	logs := h.Logs()
	if !regexp.MustCompile(`level=ERROR .*error_id=` + m[1]).MatchString(logs) {
		t.Errorf("logs lack an ERROR line with error_id=%s:\n%s", m[1], logs)
	}
	if !strings.Contains(logs, "boom") || !strings.Contains(logs, "goroutine") {
		t.Errorf("logs lack the panic value or stack:\n%s", logs)
	}
}

func TestAccessLogOmitsQueryString(t *testing.T) {
	h := passuitest.New(t)
	srv := h.Serve(passuitest.Options{})
	secret := h.Canary("query")

	srv.Get("/?token=" + secret + "&q=" + secret)

	logs := h.Logs()
	if !regexp.MustCompile(`level=INFO msg="http request" method=GET path=/ status=200 duration=\S+`).MatchString(logs) {
		t.Errorf("access log line not found:\n%s", logs)
	}
	// カナリアが出ていないことは Harness が t.Cleanup で確かめる。
}

func TestAccessLogRecordsStatusOfRejectedAndPanickedRequests(t *testing.T) {
	h := passuitest.New(t)
	srv := h.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	srv.Get("/x")
	req := srv.NewRequest(http.MethodPost, "/y", "")
	req.Host = "evil.example"
	srv.Do(req)

	logs := h.Logs()
	for _, want := range []string{
		`msg="http request" method=GET path=/x status=500`,
		`msg="http request" method=POST path=/y status=421`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs lack %q:\n%s", want, logs)
		}
	}
}

func TestErrorIDsAreUnique(t *testing.T) {
	srv := passuitest.New(t).Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	a := errorIDPattern.FindString(readBody(t, srv.Get("/")))
	b := errorIDPattern.FindString(readBody(t, srv.Get("/")))
	if a == "" || a == b {
		t.Errorf("error IDs %q and %q, want distinct", a, b)
	}
}

func TestPanicAfterWriteHeaderDoesNotCrash(t *testing.T) {
	h := passuitest.New(t)
	srv := h.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic("late boom")
	}))

	srv.Get("/")

	if !strings.Contains(h.Logs(), "late boom") {
		t.Errorf("late panic was not logged:\n%s", h.Logs())
	}
}
