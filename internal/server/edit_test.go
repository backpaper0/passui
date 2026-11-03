package server_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

// openEdit は htmx で編集フォームを開く（詳細の「編集」と同じリクエスト）。
func openEdit(t *testing.T, srv *passuitest.Server, name string) (*http.Response, string) {
	t.Helper()
	req := srv.NewRequest(http.MethodGet, "/edit/"+name, "")
	req.Header.Set("HX-Request", "true")
	res := srv.Do(req)
	return res, readBody(t, res)
}

// postEdit は編集フォームを htmx から送ったときのリクエストを通す。extra には
// 「それでも上書きする」のボタンの値などを入れる。
func postEdit(srv *passuitest.Server, name string, form url.Values) *http.Response {
	req := srv.NewRequest(http.MethodPost, "/edit/"+name, form.Encode())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	return srv.Do(req)
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestEntryDetailHasEditButton(t *testing.T) {
	fake := passuitest.NewFakeStore("dev/github token")
	srv := passuitest.New(t).Serve(passuitest.Options{Store: fake})

	_, body := openEntry(t, srv, "dev/github%20token")

	if !strings.Contains(body, `hx-get="/edit/dev/github%20token"`) {
		t.Errorf("detail has no edit button:\n%s", body)
	}
}

func TestOpenEditFormHasPathContentAndHash(t *testing.T) {
	h := passuitest.New(t)
	content := h.Canary("password") + "\nuser: " + h.Canary("user") + "\n"
	fake := passuitest.NewFakeStore()
	fake.Put("email/work", content)
	srv := h.Serve(passuitest.Options{Store: fake})

	res, body := openEdit(t, srv, "email/work")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, body)
	}
	for _, want := range []string{
		`hx-post="/edit/email/work"`,
		`name="path" value="email/work"`,
		`name="hash" value="` + sha(content) + `"`,
		">\n" + content + "</textarea>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("form does not contain %q:\n%s", want, body)
		}
	}
	if !strings.Contains(h.Logs(), "op=open-edit entry=email/work") {
		t.Errorf("no operation log:\n%s", h.Logs())
	}
}

func TestOpenEditWhileLockedShowsUnlockGuide(t *testing.T) {
	h := passuitest.New(t)
	password := h.Canary("password")
	fake := passuitest.NewFakeStore()
	fake.Put("bank", password)
	fake.SetLocked(true)
	srv := h.Serve(passuitest.Options{Store: fake})

	res, body := openEdit(t, srv, "bank")

	if res.StatusCode != http.StatusLocked {
		t.Errorf("status = %d, want 423", res.StatusCode)
	}
	// 再試行とフォーカスが戻ったときは、編集フォームを開き直す。
	for _, want := range []string{"pass show bank &gt;/dev/null", `hx-get="/edit/bank"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, password) {
		t.Errorf("locked response contains the entry:\n%s", body)
	}
}

func TestOpenEditOfMissingEntry(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{Store: passuitest.NewFakeStore()})

	res, body := openEdit(t, srv, "no/such")

	if res.StatusCode != http.StatusNotFound || !strings.Contains(body, "見つかりません") {
		t.Errorf("status = %d, want 404:\n%s", res.StatusCode, body)
	}
}

func TestSaveEditStoresContentAndShowsDetail(t *testing.T) {
	h := passuitest.New(t)
	fake := passuitest.NewFakeStore()
	fake.Put("email/work", "old\n")
	srv := h.Serve(passuitest.Options{Store: fake})
	content := h.Canary("password") + "\r\nuser: " + h.Canary("user")

	res := postEdit(srv, "email/work", url.Values{"path": {"email/work"}, "hash": {sha("old\n")}, "content": {content}})

	body := readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, body)
	}
	for _, want := range []string{"エントリを保存しました", `data-entry="email/work"`, `type="password" value="CANARYpassword`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q:\n%s", want, body)
		}
	}
	if got, _ := fake.Content("email/work"); got != strings.ReplaceAll(content, "\r\n", "\n")+"\n" {
		t.Errorf("stored content = %q", got)
	}
	if !strings.Contains(h.Logs(), "level=INFO msg=operation op=edit entry=email/work") {
		t.Errorf("no operation log:\n%s", h.Logs())
	}
	// 本文（フォームの値）のカナリアがログに出ていないことは Harness が確かめる。
}

func TestSaveEditAfterExternalChangeIsRejectedAndKeepsInput(t *testing.T) {
	h := passuitest.New(t)
	fake := passuitest.NewFakeStore()
	fake.Put("bank", "opened\n")
	srv := h.Serve(passuitest.Options{Store: fake})
	fake.Put("bank", "changed outside\n")
	input := h.Canary("input")

	res := postEdit(srv, "bank", url.Values{"path": {"bank"}, "hash": {sha("opened\n")}, "content": {input}})

	body := readBody(t, res)
	if res.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", res.StatusCode)
	}
	for _, want := range []string{
		"ほかで変更されました",
		input,
		`name="hash" value="` + sha("opened\n") + `"`,
		"最新を読み直す（入力は破棄）",
		`hx-get="/edit/bank"`,
		`name="overwrite" value="` + sha("changed outside\n") + `"`,
		"それでも上書きする",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q:\n%s", want, body)
		}
	}
	if got, _ := fake.Content("bank"); got != "changed outside\n" {
		t.Errorf("entry changed: %q", got)
	}

	// 「それでも上書きする」は、競合を見つけた時点の版に対して保存する。
	res = postEdit(srv, "bank", url.Values{"path": {"bank"}, "hash": {sha("opened\n")}, "content": {input},
		"overwrite": {sha("changed outside\n")}})

	if res.StatusCode != http.StatusOK {
		t.Errorf("overwrite status = %d, want 200:\n%s", res.StatusCode, readBody(t, res))
	}
	if got, _ := fake.Content("bank"); got != input+"\n" {
		t.Errorf("entry after overwrite = %q", got)
	}
}

func TestSaveEditOfRemovedEntryIsConflictAndDoesNotRecreate(t *testing.T) {
	h := passuitest.New(t)
	fake := passuitest.NewFakeStore("email/work")
	srv := h.Serve(passuitest.Options{Store: fake})
	fake.Remove("email/work")
	input := h.Canary("input")

	res := postEdit(srv, "email/work", url.Values{"path": {"email/work"}, "hash": {sha("ciphertext of email/work")}, "content": {input}})

	body := readBody(t, res)
	if res.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", res.StatusCode)
	}
	if got := res.Header.Get("HX-Trigger"); got != "tree-changed" {
		t.Errorf("HX-Trigger = %q, want tree-changed", got)
	}
	for _, want := range []string{"削除または移動されました", input} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "それでも上書きする") {
		t.Errorf("offers overwrite for a removed entry:\n%s", body)
	}
	if _, ok := fake.Content("email/work"); ok {
		t.Errorf("removed entry is recreated")
	}
}

func TestSaveEditRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		name, path, content, message string
	}{
		{"empty content", "bank", "", "本文を入力してください"},
		{"empty path", "", "x", "パスを入力してください"},
		{"invalid path", "../bank", "x", "パスが正しくありません"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := passuitest.NewFakeStore("bank")
			srv := passuitest.New(t).Serve(passuitest.Options{Store: fake})

			res := postEdit(srv, "bank", url.Values{"path": {tc.path}, "hash": {sha("ciphertext of bank")}, "content": {tc.content}})

			if res.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", res.StatusCode)
			}
			if body := readBody(t, res); !strings.Contains(body, tc.message) {
				t.Errorf("body does not contain %q:\n%s", tc.message, body)
			}
			if got, _ := fake.Content("bank"); got != "ciphertext of bank" {
				t.Errorf("entry changed: %q", got)
			}
		})
	}
}

func TestSaveEditWithNewPathMovesEntryAndReloadsTree(t *testing.T) {
	h := passuitest.New(t)
	fake := passuitest.NewFakeStore("email/other")
	fake.Put("email/work", "old\n")
	srv := h.Serve(passuitest.Options{Store: fake})
	input := h.Canary("input")

	res := postEdit(srv, "email/work", url.Values{"path": {"archive/work"}, "hash": {sha("old\n")}, "content": {input}})

	body := readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, body)
	}
	for _, want := range []string{"archive/work に移動しました", `data-entry="archive/work"`, `id="selected-entry" name="selected" value="archive/work"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q:\n%s", want, body)
		}
	}
	if res.Header.Get("HX-Trigger") != "tree-changed" {
		t.Errorf("tree is not reloaded")
	}
	if got, _ := fake.Content("archive/work"); got != input+"\n" {
		t.Errorf("moved content = %q", got)
	}
	if _, ok := fake.Content("email/work"); ok {
		t.Errorf("old entry still exists")
	}
	if !strings.Contains(h.Logs(), "op=move entry=email/work to=archive/work") {
		t.Errorf("no move log:\n%s", h.Logs())
	}
}

func TestSaveEditMoveToTakenDestinationIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name, path, message string
	}{
		{"existing entry", "email/other", "同じ名前のエントリが既にあります"},
		{"directory with the same name", "email", "同じ名前のフォルダがあります"},
		{"trailing slash", "archive/", "パスの末尾が / です"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := passuitest.NewFakeStore("email/other")
			fake.Put("bank", "old\n")
			srv := passuitest.New(t).Serve(passuitest.Options{Store: fake})

			res := postEdit(srv, "bank", url.Values{"path": {tc.path}, "hash": {sha("old\n")}, "content": {"new"}})

			body := readBody(t, res)
			if res.StatusCode != http.StatusConflict || !strings.Contains(body, tc.message) {
				t.Errorf("status = %d, want 409 with %q:\n%s", res.StatusCode, tc.message, body)
			}
			if got, _ := fake.Content("bank"); got != "old\n" {
				t.Errorf("entry changed: %q", got)
			}
			if got, _ := fake.Content("email/other"); got != "ciphertext of email/other" {
				t.Errorf("destination changed: %q", got)
			}
		})
	}
}

// mv だけが失敗した: 内容は元のパスに保存できたことと、移動に失敗したことを伝える。
// 原因（stderr）は出さず、エラー ID だけ。フォームのハッシュは保存した版にする。
func TestSaveEditMoveFailureSaysContentWasSavedButNotMoved(t *testing.T) {
	h := passuitest.New(t)
	fake := passuitest.NewFakeStore()
	fake.Put("bank", "old\n")
	fake.SetMoveErr(errors.New("pass mv: STDERR-DETAIL"))
	srv := h.Serve(passuitest.Options{Store: fake})
	input := h.Canary("input")

	res := postEdit(srv, "bank", url.Values{"path": {"money/bank"}, "hash": {sha("old\n")}, "content": {input}})

	body := readBody(t, res)
	for _, want := range []string{"内容は bank に保存しましたが、money/bank への移動に失敗しました（エラー ID: ", input, `name="hash" value="` + sha(input+"\n") + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "STDERR-DETAIL") {
		t.Errorf("body leaks the error:\n%s", body)
	}
	if res.Header.Get("HX-Trigger") != "tree-changed" {
		t.Errorf("tree is not reloaded")
	}
	if got, _ := fake.Content("bank"); got != input+"\n" {
		t.Errorf("content at the old path = %q", got)
	}
	if _, ok := fake.Content("money/bank"); ok {
		t.Errorf("entry was moved")
	}
}

func TestSaveEditFailureShowsOnlyErrorID(t *testing.T) {
	h := passuitest.New(t)
	fake := passuitest.NewFakeStore("bank")
	srv := h.Serve(passuitest.Options{Store: fake})
	fake.SetErr(errors.New("pass insert: STDERR-DETAIL"))
	input := h.Canary("input")

	res := postEdit(srv, "bank", url.Values{"path": {"bank"}, "hash": {sha("ciphertext of bank")}, "content": {input}})

	body := readBody(t, res)
	if res.StatusCode != http.StatusInternalServerError || !strings.Contains(body, "操作に失敗しました（エラー ID: ") {
		t.Errorf("status = %d, want 500 with error ID:\n%s", res.StatusCode, body)
	}
	if strings.Contains(body, "STDERR-DETAIL") || !strings.Contains(body, input) {
		t.Errorf("body leaks the error or loses the input:\n%s", body)
	}
}
