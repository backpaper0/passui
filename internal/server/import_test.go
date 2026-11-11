package server_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/export"
	"github.com/backpaper0/passui/internal/passuitest"
	"github.com/backpaper0/passui/internal/server"
)

const importForm = `<form class="import-form" method="post" action="/import" enctype="multipart/form-data">`

// fakeImport は、呼ばれた回数と読んだものを覚え、sum と err を返す偽物の Import。
type fakeImport struct {
	calls int
	read  string
	sum   export.ImportSummary
	err   error
}

func (f *fakeImport) Import(_ context.Context, r io.Reader) (export.ImportSummary, error) {
	f.calls++
	b, err := io.ReadAll(r)
	if err != nil {
		return export.ImportSummary{}, err
	}
	f.read = string(b)
	return f.sum, f.err
}

func storeEmpty(empty bool) server.StoreEmptyFunc {
	return func(context.Context) (bool, error) { return empty, nil }
}

// postImport はインポートのフォームと同じリクエスト（multipart/form-data の普通のフォームの送信）を
// 送る。field はファイルのフィールドの名前。
func postImport(t *testing.T, srv *passuitest.Server, field, content string) (*http.Response, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile(field, "passui-export-20261105-090807.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(fw, content); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := srv.NewRequest(http.MethodPost, "/import", body.String())
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", "http://"+srv.Host)
	res := srv.Do(req)
	return res, readBody(t, res)
}

// ストアのディレクトリがないか空のときだけ、左ペインの「エントリがありません」の中に
// インポートのフォーム（htmx を使わない普通のフォーム）が出る。
func TestTreeShowsImportFormOnlyWhenStoreIsEmpty(t *testing.T) {
	h := passuitest.New(t)

	empty := h.Serve(passuitest.Options{StoreEmpty: storeEmpty(true)})
	for _, target := range []string{"/", "/tree"} {
		body := readBody(t, empty.Get(target))
		_, rest, ok := strings.Cut(body, "エントリがありません。")
		if !ok || !strings.Contains(rest, importForm) {
			t.Fatalf("%s lacks the import form after the empty message:\n%s", target, body)
		}
		form, _, _ := strings.Cut(strings.SplitN(rest, importForm, 2)[1], "</form>")
		for _, s := range []string{`type="file"`, `name="file"`, ">インポート<"} {
			if !strings.Contains(form, s) {
				t.Errorf("%s: import form lacks %q:\n%s", target, s, form)
			}
		}
		if strings.Contains(form, "hx-") {
			t.Errorf("%s: import form uses htmx:\n%s", target, form)
		}
	}

	// .gpg-id だけのストアなど、エントリはないが空でないときは出さない。
	notEmpty := h.Serve(passuitest.Options{StoreEmpty: storeEmpty(false)})
	if body := readBody(t, notEmpty.Get("/")); strings.Contains(body, importForm) {
		t.Errorf("import form is shown for a non-empty store:\n%s", body)
	}

	withEntries := h.Serve(passuitest.Options{Store: passuitest.NewFakeStore("bank"), StoreEmpty: storeEmpty(true)})
	if body := readBody(t, withEntries.Get("/")); strings.Contains(body, importForm) {
		t.Errorf("import form is shown with entries:\n%s", body)
	}
}

func TestImportShowsGuidanceAfterSuccess(t *testing.T) {
	h := passuitest.New(t)
	fake := &fakeImport{sum: export.ImportSummary{Top: "passui-export-20261105-090807", Keys: true, SecretKeys: 2}}
	srv := h.Serve(passuitest.Options{Import: fake.Import})

	res, body := postImport(t, srv, "file", "tar.gz bytes")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, body)
	}
	if fake.calls != 1 || fake.read != "tar.gz bytes" {
		t.Errorf("Import called %d times with %q, want once with the uploaded file", fake.calls, fake.read)
	}
	assertImportPage(t, res, body)
	if !strings.Contains(body, "インポートしました。ターミナルで解錠し、エントリを開いて確かめてください") {
		t.Errorf("body lacks the guidance:\n%s", body)
	}
	logs := h.Logs()
	for _, s := range []string{"op=import", "secret_keys=2"} {
		if !strings.Contains(logs, s) {
			t.Errorf("operation log lacks %q:\n%s", s, logs)
		}
	}
}

func TestImportFailuresAreClassified(t *testing.T) {
	tests := map[string]struct {
		err      error
		status   int
		messages []string
		errorID  bool
	}{
		"store not empty": {
			err: export.ErrStoreNotEmpty, status: http.StatusConflict,
			messages: []string{"ストアが空ではないので、インポートしませんでした。"},
		},
		"secret key exists": {
			err: export.ErrSecretKeyExists, status: http.StatusConflict,
			messages: []string{"同じ秘密鍵がゲストにすでにあるので、インポートしませんでした。"},
		},
		"invalid archive": {
			err: &export.InvalidExportFileError{Reason: "store がありません"}, status: http.StatusBadRequest,
			messages: []string{"エクスポートファイルとして受け付けられません: store がありません"},
		},
		"unexpected": {
			err: errors.New("gpg: boom"), status: http.StatusInternalServerError,
			messages: []string{"エラー ID", "ログでエラー ID を探してください。"}, errorID: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := passuitest.New(t)
			fake := &fakeImport{err: tt.err}
			srv := h.Serve(passuitest.Options{Import: fake.Import})

			res, body := postImport(t, srv, "file", "tar.gz bytes")

			if res.StatusCode != tt.status {
				t.Errorf("status = %d, want %d:\n%s", res.StatusCode, tt.status, body)
			}
			assertImportPage(t, res, body)
			for _, s := range tt.messages {
				if !strings.Contains(body, s) {
					t.Errorf("body lacks %q:\n%s", s, body)
				}
			}
			if strings.Contains(body, "インポートしました") || strings.Contains(body, "boom") {
				t.Errorf("body has the success guidance or the cause:\n%s", body)
			}
			if got := strings.Contains(body, "エラー ID"); got != tt.errorID {
				t.Errorf("error ID shown = %v, want %v:\n%s", got, tt.errorID, body)
			}
			if tt.errorID && !strings.Contains(h.Logs(), "gpg: boom") {
				t.Errorf("logs lack the cause:\n%s", h.Logs())
			}
		})
	}
}

// ファイルのフィールドがない送信は、Import を呼ばずに 400 で断る。
func TestImportWithoutFileIsBadRequest(t *testing.T) {
	fake := &fakeImport{}
	srv := passuitest.New(t).Serve(passuitest.Options{Import: fake.Import})

	res, body := postImport(t, srv, "other", "tar.gz bytes")

	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400:\n%s", res.StatusCode, body)
	}
	assertImportPage(t, res, body)
	if fake.calls != 0 {
		t.Errorf("Import called %d times, want 0", fake.calls)
	}
	// 画面にエラー ID を出さないので、ログにもエラー ID を付けない。
	if strings.Contains(body, "エラー ID") || strings.Contains(srv.Logs(), "error_id=") {
		t.Errorf("an error ID is shown or logged:\n%s\n%s", body, srv.Logs())
	}
}

// 上限より大きいファイルは 413 で断る。
func TestImportTooLargeIsRejected(t *testing.T) {
	fake := &fakeImport{}
	srv := passuitest.New(t).Serve(passuitest.Options{Import: fake.Import, ImportLimit: 1024})

	res, body := postImport(t, srv, "file", strings.Repeat("x", 4096))

	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413:\n%s", res.StatusCode, body)
	}
	assertImportPage(t, res, body)
	if !strings.Contains(body, "ファイルが大きすぎます") {
		t.Errorf("body lacks the message:\n%s", body)
	}
}

func TestImportRequiresLoginAndSameOrigin(t *testing.T) {
	h := passuitest.New(t)
	fake := &fakeImport{}

	anon := h.Serve(passuitest.Options{Anonymous: true, Import: fake.Import})
	if res, body := postImport(t, anon, "file", "x"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("without login: status = %d, want 401:\n%s", res.StatusCode, body)
	}

	srv := h.Serve(passuitest.Options{Import: fake.Import})
	req := srv.NewRequest(http.MethodPost, "/import", "x")
	req.Header.Set("Origin", "http://evil.example")
	if res := srv.Do(req); res.StatusCode != http.StatusForbidden {
		t.Errorf("cross origin: status = %d, want 403", res.StatusCode)
	}

	if fake.calls != 0 {
		t.Errorf("Import called %d times, want 0", fake.calls)
	}
}

// assertImportPage は、応答がトップに戻れるインポートの結果ページであることを確かめる。
func assertImportPage(t *testing.T, res *http.Response, body string) {
	t.Helper()
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	for _, s := range []string{"<html", "<h2>インポート</h2>", `href="/"`, "トップに戻る"} {
		if !strings.Contains(body, s) {
			t.Errorf("page lacks %q:\n%s", s, body)
		}
	}
}

// 本物の Import でも、上限を超えて読んだら（エクスポートファイルとして読めないとは言わずに）413 で断る。
func TestImportTooLargeIsRejectedByRealImport(t *testing.T) {
	noise := make([]byte, 64<<10)
	if _, err := rand.Read(noise); err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "top/", Mode: 0o700}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "top/README.txt", Mode: 0o600, Size: int64(len(noise))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(noise); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	storeDir := filepath.Join(t.TempDir(), "store")
	srv := passuitest.New(t).Serve(passuitest.Options{
		ImportLimit: 4096,
		Import: func(ctx context.Context, r io.Reader) (export.ImportSummary, error) {
			return export.Import(ctx, r, export.ImportOptions{StoreDir: storeDir})
		},
	})

	res, body := postImport(t, srv, "file", gz.String())

	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413:\n%s", res.StatusCode, body)
	}
}
