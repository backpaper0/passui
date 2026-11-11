package server_test

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/export"
	"github.com/backpaper0/passui/internal/passuitest"
)

const exportName = "passui-export-20261105-090807.tar.gz"

// fakeExport は、呼ばれた回数を数え、body を w に書いて sum と err を返す偽物の Export。
// 失敗するときも、途中まで書いたものとして body を書く。
type fakeExport struct {
	calls int
	body  string
	sum   export.Summary
	err   error
}

func (f *fakeExport) Export(_ context.Context, w io.Writer) (export.Summary, error) {
	f.calls++
	if _, err := io.WriteString(w, f.body); err != nil {
		return export.Summary{}, err
	}
	return f.sum, f.err
}

// postExport はエクスポート ボタンを押したときと同じリクエスト（htmx を使わない普通の
// フォームの送信）を送る。
func postExport(t *testing.T, srv *passuitest.Server) (*http.Response, string) {
	t.Helper()
	req := srv.NewRequest(http.MethodPost, "/export", "")
	req.Header.Set("Origin", "http://"+srv.Host)
	res := srv.Do(req)
	return res, readBody(t, res)
}

// エクスポート ボタンは htmx を使わない普通のフォームで、押すとダウンロードだけが始まる。
func TestTopHasExportButtonAsPlainForm(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{})

	body := readBody(t, srv.Get("/"))

	_, form, ok := strings.Cut(body, `<form class="export-form" method="post" action="/export">`)
	if !ok {
		t.Fatalf("top lacks the export form:\n%s", body)
	}
	form, _, _ = strings.Cut(form, "</form>")
	if !strings.Contains(form, ">エクスポート<") {
		t.Errorf("export form lacks the button:\n%s", form)
	}
	if strings.Contains(form, "hx-") {
		t.Errorf("export form uses htmx:\n%s", form)
	}
	for _, s := range []string{"/backup", "バックアップ"} {
		if strings.Contains(body, s) {
			t.Errorf("top still has %q:\n%s", s, body)
		}
	}
}

func TestExportDownloadsExportFile(t *testing.T) {
	h := passuitest.New(t)
	fake := &fakeExport{body: "tar.gz bytes", sum: export.Summary{Name: exportName}}
	srv := h.Serve(passuitest.Options{Export: fake.Export})

	res, body := postExport(t, srv)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, body)
	}
	if fake.calls != 1 {
		t.Errorf("Export called %d times, want 1", fake.calls)
	}
	if body != "tar.gz bytes" {
		t.Errorf("body = %q, want what Export wrote", body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type = %q, want application/gzip", ct)
	}
	disp, params, err := mime.ParseMediaType(res.Header.Get("Content-Disposition"))
	if err != nil || disp != "attachment" || params["filename"] != exportName {
		t.Errorf("Content-Disposition = %q, want an attachment named %s", res.Header.Get("Content-Disposition"), exportName)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	logs := h.Logs()
	if !strings.Contains(logs, "op=export") || !strings.Contains(logs, exportName) {
		t.Errorf("no operation log with the file name:\n%s", logs)
	}
}

func TestExportLogsWhatItContains(t *testing.T) {
	h := passuitest.New(t)
	fake := &fakeExport{sum: export.Summary{Name: exportName, Keys: []string{"AAAA", "BBBB"}, SecretKeys: 1, SkippedSpecialFiles: 3}}
	srv := h.Serve(passuitest.Options{Export: fake.Export})

	if res, body := postExport(t, srv); res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, body)
	}

	logs := h.Logs()
	for _, s := range []string{"op=export", "store_keys=2", "secret_keys=1", "skipped_special_files=3"} {
		if !strings.Contains(logs, s) {
			t.Errorf("operation log lacks %q:\n%s", s, logs)
		}
	}
}

// 想定外の失敗では、ダウンロードの代わりにエラー ID 付きの結果ページを返す。
// Export が途中まで書いたものは送らない。
func TestExportFailureShowsResultPageWithErrorID(t *testing.T) {
	h := passuitest.New(t)
	fake := &fakeExport{body: "partial tar.gz", err: errors.New("gpg: boom")}
	srv := h.Serve(passuitest.Options{Export: fake.Export})

	res, body := postExport(t, srv)

	if res.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500:\n%s", res.StatusCode, body)
	}
	assertResultPage(t, res, body)
	for _, s := range []string{"エラー ID", "ログでエラー ID を探してください。"} {
		if !strings.Contains(body, s) {
			t.Errorf("body lacks %q:\n%s", s, body)
		}
	}
	for _, s := range []string{"boom", "partial tar.gz"} {
		if strings.Contains(body, s) {
			t.Errorf("body contains %q:\n%s", s, body)
		}
	}
	logs := h.Logs()
	if !strings.Contains(logs, "error_id=") || !strings.Contains(logs, "gpg: boom") {
		t.Errorf("logs lack the error ID and the cause:\n%s", logs)
	}
}

func TestExportWithUnresolvedKeysShowsKeyIDs(t *testing.T) {
	h := passuitest.New(t)
	fake := &fakeExport{err: &export.UnresolvedKeysError{IDs: []string{"nobody@example.invalid", "DEADBEEF"}}}
	srv := h.Serve(passuitest.Options{Export: fake.Export})

	res, body := postExport(t, srv)

	if res.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409:\n%s", res.StatusCode, body)
	}
	assertResultPage(t, res, body)
	for _, s := range []string{
		"ストアの鍵が見つかりません: <code>nobody@example.invalid</code>, <code>DEADBEEF</code>",
		"<code>.gpg-id</code> と <code>PASSWORD_STORE_KEY</code> を確かめてください。",
	} {
		if !strings.Contains(body, s) {
			t.Errorf("body lacks %q:\n%s", s, body)
		}
	}
	if strings.Contains(body, "エラー ID") {
		t.Errorf("body shows an error ID:\n%s", body)
	}
	if logs := h.Logs(); !strings.Contains(logs, "nobody@example.invalid") || !strings.Contains(logs, "DEADBEEF") {
		t.Errorf("logs lack the unresolved key IDs:\n%s", logs)
	}
}

// assertResultPage は、応答がダウンロードではなく、トップに戻れる結果ページであることを確かめる。
func assertResultPage(t *testing.T, res *http.Response, body string) {
	t.Helper()
	if d := res.Header.Get("Content-Disposition"); d != "" {
		t.Errorf("Content-Disposition = %q, want none", d)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	for _, s := range []string{"<html", "エクスポート", `href="/"`, "トップに戻る"} {
		if !strings.Contains(body, s) {
			t.Errorf("page lacks %q:\n%s", s, body)
		}
	}
}

func TestExportRequiresLoginAndSameOrigin(t *testing.T) {
	h := passuitest.New(t)
	fake := &fakeExport{sum: export.Summary{Name: exportName}}

	anon := h.Serve(passuitest.Options{Anonymous: true, Export: fake.Export})
	if res, body := postExport(t, anon); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("without login: status = %d, want 401:\n%s", res.StatusCode, body)
	}

	srv := h.Serve(passuitest.Options{Export: fake.Export})
	req := srv.NewRequest(http.MethodPost, "/export", "")
	req.Header.Set("Origin", "http://evil.example")
	if res := srv.Do(req); res.StatusCode != http.StatusForbidden {
		t.Errorf("cross origin: status = %d, want 403", res.StatusCode)
	}

	if fake.calls != 0 {
		t.Errorf("Export called %d times, want 0", fake.calls)
	}
}

func TestBackupRouteIsGone(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{})
	req := srv.NewRequest(http.MethodPost, "/backup", "")
	req.Header.Set("Origin", "http://"+srv.Host)

	if res := srv.Do(req); res.StatusCode != http.StatusNotFound && res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /backup: status = %d, want 404 or 405", res.StatusCode)
	}
}
