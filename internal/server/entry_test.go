package server_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

// openEntry は htmx でエントリの詳細を開く（ツリーのクリックと同じリクエスト）。
func openEntry(t *testing.T, srv *passuitest.Server, name string) (*http.Response, string) {
	t.Helper()
	req := srv.NewRequest(http.MethodGet, "/entry/"+name, "")
	req.Header.Set("HX-Request", "true")
	res := srv.Do(req)
	return res, readBody(t, res)
}

func TestOpenEntryShowsMaskedPasswordAndFields(t *testing.T) {
	h := passuitest.New(t)
	password := h.Canary("password")
	user := h.Canary("user")
	fake := passuitest.NewFakeStore()
	fake.Put("email/work", password+"\nuser: "+user+"\nurl: https://example.com/login\n")
	srv := h.Serve(passuitest.Options{Store: fake})

	res, body := openEntry(t, srv, "email/work")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, body)
	}
	// 1 行目は伏せ字の初期状態（type="password"）で DOM に入る。
	if !strings.Contains(body, `type="password"`) || !strings.Contains(body, `value="`+password+`"`) {
		t.Errorf("password is not a masked input:\n%s", body)
	}
	if !strings.Contains(body, "data-reveal") {
		t.Errorf("no reveal button:\n%s", body)
	}
	// 2 行目以降は伏せずに key と value で出す。
	for _, want := range []string{"<dt>user</dt>", "<dd>" + user, "<dt>url</dt>", "https://example.com/login"} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<html") {
		t.Errorf("detail is not a fragment:\n%s", body)
	}
}

func TestTreeEntryOpensDetailInRightPane(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{Store: passuitest.NewFakeStore("email/my work")})

	body := readBody(t, srv.Get("/tree"))

	if !strings.Contains(body, `hx-get="/entry/email/my%20work"`) || !strings.Contains(body, `hx-target="#right-pane"`) {
		t.Errorf("tree entry does not open the detail:\n%s", body)
	}
}

func TestOpenEntryWhileLockedShowsUnlockGuidance(t *testing.T) {
	h := passuitest.New(t)
	password := h.Canary("password")
	fake := passuitest.NewFakeStore()
	fake.Put("email/work", password+"\nuser: alice")
	fake.SetLocked(true)
	srv := h.Serve(passuitest.Options{Store: fake})

	res, body := openEntry(t, srv, "email/work")

	if res.StatusCode != http.StatusLocked {
		t.Errorf("status = %d, want 423", res.StatusCode)
	}
	for _, want := range []string{
		"ロック中です",
		"pass show email/work &gt;/dev/null",
		`data-copy="#unlock-command"`,
		"再試行",
		// 再試行とフォーカスが戻ったときの確認は、同じ詳細を開き直す。
		`hx-get="/entry/email/work"`,
		`hx-trigger="focus from:window"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, password) || strings.Contains(body, "alice") {
		t.Errorf("locked response contains the entry:\n%s", body)
	}
}

func TestUnlockCommandQuotesEntryName(t *testing.T) {
	fake := passuitest.NewFakeStore("it's mine")
	fake.SetLocked(true)
	srv := passuitest.New(t).Serve(passuitest.Options{Store: fake})

	_, body := openEntry(t, srv, "it%27s%20mine")

	// シングルクォートで囲み、中の ' は '\'' にする（HTML ではエスケープされる）。
	if !strings.Contains(body, `pass show &#39;it&#39;\&#39;&#39;s mine&#39; &gt;/dev/null`) {
		t.Errorf("unlock command is not quoted:\n%s", body)
	}
}

func TestOpenMissingEntryShowsNotFound(t *testing.T) {
	fake := passuitest.NewFakeStore("email/work")
	srv := passuitest.New(t).Serve(passuitest.Options{Store: fake})
	fake.Remove("email/work") // 外で消された

	res, body := openEntry(t, srv, "email/work")

	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.StatusCode)
	}
	if !strings.Contains(body, "見つかりません") {
		t.Errorf("body does not say not found:\n%s", body)
	}
}

func TestOpenEntryFailureShowsOnlyErrorID(t *testing.T) {
	h := passuitest.New(t)
	stderr := "gpg: decryption failed: some internal detail"
	fake := passuitest.NewFakeStore("email/work")
	fake.SetErr(errors.New(stderr))
	srv := h.Serve(passuitest.Options{Store: fake})

	res, body := openEntry(t, srv, "email/work")

	if res.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", res.StatusCode)
	}
	if !strings.Contains(body, "操作に失敗しました（エラー ID: ") {
		t.Errorf("body has no error ID:\n%s", body)
	}
	if strings.Contains(body, stderr) {
		t.Errorf("stderr leaked to browser:\n%s", body)
	}
}

func TestOpenEntryLogsOperationAndPath(t *testing.T) {
	h := passuitest.New(t)
	fake := passuitest.NewFakeStore()
	fake.Put("email/work", h.Canary("password")+"\nuser: "+h.Canary("user"))
	srv := h.Serve(passuitest.Options{Store: fake})

	openEntry(t, srv, "email/work")

	if !strings.Contains(h.Logs(), "level=INFO msg=operation op=show entry=email/work") {
		t.Errorf("no operation log:\n%s", h.Logs())
	}
}

func TestHeaderBadgeShowsLockState(t *testing.T) {
	fake := passuitest.NewFakeStore("a")
	srv := passuitest.New(t).Serve(passuitest.Options{Store: fake})

	if body := readBody(t, srv.Get("/")); !strings.Contains(body, `id="lock-badge"`) || !strings.Contains(body, "解錠中") {
		t.Errorf("top page has no unlocked badge:\n%s", body)
	}
	fake.SetLocked(true)
	body := readBody(t, srv.Get("/lock-status"))
	if !strings.Contains(body, `id="lock-badge"`) || !strings.Contains(body, "ロック中") {
		t.Errorf("lock status has no locked badge:\n%s", body)
	}
	if strings.Contains(body, "<html") {
		t.Errorf("lock status is not a fragment:\n%s", body)
	}
}

func TestEntryResponsesUpdateHeaderBadge(t *testing.T) {
	fake := passuitest.NewFakeStore("a")
	srv := passuitest.New(t).Serve(passuitest.Options{Store: fake})

	_, body := openEntry(t, srv, "a")
	if !strings.Contains(body, `hx-swap-oob="true"`) || !strings.Contains(body, "解錠中") {
		t.Errorf("detail does not update the badge:\n%s", body)
	}
	fake.SetLocked(true)
	_, body = openEntry(t, srv, "a")
	if !strings.Contains(body, `hx-swap-oob="true"`) || !strings.Contains(body, ">ロック中<") {
		t.Errorf("locked guidance does not update the badge:\n%s", body)
	}
}
