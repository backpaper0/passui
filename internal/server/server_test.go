package server_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

func TestTopPage(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{})

	res := srv.Get("/")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	body := readBody(t, res)
	if !strings.Contains(body, "<title>passui</title>") {
		t.Errorf("body does not contain title: %s", body)
	}
	if !strings.Contains(body, `src="/static/htmx.min.js"`) {
		t.Errorf("body does not load htmx: %s", body)
	}
}

func TestEveryResponseIsNoStore(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{})

	for _, target := range []string{"/", "/static/htmx.min.js", "/no-such-page"} {
		res := srv.Get(target)
		if got := res.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("GET %s: Cache-Control = %q, want no-store", target, got)
		}
	}
}

func TestAllowedHosts(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{Port: 4000})

	for _, host := range []string{"passui.localhost:4000", "localhost:4000", "127.0.0.1:4000"} {
		req := srv.NewRequest(http.MethodGet, "/", "")
		req.Host = host
		if res := srv.Do(req); res.StatusCode != http.StatusOK {
			t.Errorf("Host %s: status = %d, want 200", host, res.StatusCode)
		}
	}
}

func TestRejectsUnknownHost(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{Port: 4000})

	for _, host := range []string{
		"evil.example:4000",
		"passui.localhost:3560", // 違うポート
		"passui.localhost",      // ポートなし
		"localhost",
		"",
		"[::1]:4000",
	} {
		req := srv.NewRequest(http.MethodGet, "/", "")
		req.Host = host
		res := srv.Do(req)
		if res.StatusCode != http.StatusMisdirectedRequest {
			t.Errorf("Host %q: status = %d, want 421", host, res.StatusCode)
		}
		if got := res.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("Host %q: Cache-Control = %q, want no-store", host, got)
		}
		if body := readBody(t, res); strings.Contains(body, "<title>passui</title>") {
			t.Errorf("Host %q: page was served", host)
		}
	}
}

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
