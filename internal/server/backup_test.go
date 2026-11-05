package server_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/backpaper0/passui/internal/backup"
	"github.com/backpaper0/passui/internal/passuitest"
)

const backupPath = "/home/dev/.local/share/passui/backup/passui-backup-20261105-090807.tar.gz"

// fakeBackup は、呼ばれた回数を数えて sum と err を返す偽物の Backup。
type fakeBackup struct {
	calls int
	sum   backup.Summary
	err   error
}

func (f *fakeBackup) Backup(context.Context) (backup.Summary, error) {
	f.calls++
	return f.sum, f.err
}

// postBackup はバックアップ ボタンを押したときと同じリクエストを送る。htmx が true なら
// htmx から、false なら JS なしの普通のフォームの送信として送る。
func postBackup(t *testing.T, srv *passuitest.Server, htmx bool) (*http.Response, string) {
	t.Helper()
	req := srv.NewRequest(http.MethodPost, "/backup", "")
	req.Header.Set("Origin", "http://"+srv.Host)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	res := srv.Do(req)
	return res, readBody(t, res)
}

func TestTopHasBackupButtonDisabledWhileCreating(t *testing.T) {
	srv := passuitest.New(t).Serve(passuitest.Options{})

	body := readBody(t, srv.Get("/"))

	for _, s := range []string{`hx-post="/backup"`, `hx-target="#right-pane"`, "hx-disabled-elt", "作成中…", ">バックアップ<"} {
		if !strings.Contains(body, s) {
			t.Errorf("top lacks %q:\n%s", s, body)
		}
	}
}

func TestBackupShowsFullPathInRightPane(t *testing.T) {
	h := passuitest.New(t)
	entry := h.Canary("entry")
	store := passuitest.NewFakeStore()
	store.Put(entry, h.Canary("password"))
	fake := &fakeBackup{sum: backup.Summary{Path: backupPath}}
	srv := h.Serve(passuitest.Options{Store: store, Backup: fake.Backup})

	res, body := postBackup(t, srv, true)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, body)
	}
	if fake.calls != 1 {
		t.Errorf("Backup called %d times, want 1", fake.calls)
	}
	if !strings.Contains(body, "バックアップを置きました。") || !strings.Contains(body, backupPath) {
		t.Errorf("right pane lacks the notice and the path:\n%s", body)
	}
	if strings.Contains(body, "<html") {
		t.Errorf("htmx response is a full page:\n%s", body)
	}
	logs := h.Logs()
	if !strings.Contains(logs, "op=backup") || !strings.Contains(logs, backupPath) {
		t.Errorf("no operation log with the path:\n%s", logs)
	}
}

func TestBackupLogsHowManySpecialFilesWereSkipped(t *testing.T) {
	h := passuitest.New(t)
	fake := &fakeBackup{sum: backup.Summary{Path: backupPath, SkippedSpecialFiles: 3}}
	srv := h.Serve(passuitest.Options{Backup: fake.Backup})

	if res, body := postBackup(t, srv, true); res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, body)
	}
	if logs := h.Logs(); !strings.Contains(logs, "skipped_special_files=3") {
		t.Errorf("operation log lacks the number of skipped special files:\n%s", logs)
	}
}

func TestBackupWithoutHtmxShowsFullPageWithLinkToTop(t *testing.T) {
	h := passuitest.New(t)
	fake := &fakeBackup{sum: backup.Summary{Path: backupPath}}
	srv := h.Serve(passuitest.Options{Backup: fake.Backup})

	res, body := postBackup(t, srv, false)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, body)
	}
	for _, s := range []string{"<html", "バックアップを置きました。", backupPath, `href="/"`, "トップに戻る"} {
		if !strings.Contains(body, s) {
			t.Errorf("page lacks %q:\n%s", s, body)
		}
	}
}

func TestBackupFailureShowsOnlyErrorID(t *testing.T) {
	for _, htmx := range []bool{true, false} {
		h := passuitest.New(t)
		fake := &fakeBackup{err: errors.New("gpg: boom")}
		srv := h.Serve(passuitest.Options{Backup: fake.Backup})

		res, body := postBackup(t, srv, htmx)

		if res.StatusCode != http.StatusInternalServerError {
			t.Errorf("htmx=%v: status = %d, want 500:\n%s", htmx, res.StatusCode, body)
		}
		for _, s := range []string{"エラー ID", "ログでエラー ID を探してください。バックアップ先には何も置いていません。"} {
			if !strings.Contains(body, s) {
				t.Errorf("htmx=%v: body lacks %q:\n%s", htmx, s, body)
			}
		}
		if strings.Contains(body, "boom") {
			t.Errorf("htmx=%v: body leaks the error:\n%s", htmx, body)
		}
		if got := strings.Contains(body, "<html"); got != !htmx {
			t.Errorf("htmx=%v: full page = %v:\n%s", htmx, got, body)
		}
		if !htmx && !strings.Contains(body, "トップに戻る") {
			t.Errorf("page lacks a link to top:\n%s", body)
		}
		logs := h.Logs()
		if !strings.Contains(logs, "error_id=") || !strings.Contains(logs, "gpg: boom") {
			t.Errorf("htmx=%v: logs lack the error ID and the cause:\n%s", htmx, logs)
		}
	}
}

func TestBackupRequiresLoginAndSameOrigin(t *testing.T) {
	h := passuitest.New(t)
	fake := &fakeBackup{sum: backup.Summary{Path: backupPath}}

	anon := h.Serve(passuitest.Options{Anonymous: true, Backup: fake.Backup})
	if res, body := postBackup(t, anon, true); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("without login: status = %d, want 401:\n%s", res.StatusCode, body)
	}

	srv := h.Serve(passuitest.Options{Backup: fake.Backup})
	req := srv.NewRequest(http.MethodPost, "/backup", "")
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("HX-Request", "true")
	if res := srv.Do(req); res.StatusCode != http.StatusForbidden {
		t.Errorf("cross origin: status = %d, want 403", res.StatusCode)
	}

	if fake.calls != 0 {
		t.Errorf("Backup called %d times, want 0", fake.calls)
	}
}

func TestBackupWithUnresolvedKeysShowsKeyIDs(t *testing.T) {
	for _, htmx := range []bool{true, false} {
		h := passuitest.New(t)
		fake := &fakeBackup{err: &backup.UnresolvedKeysError{IDs: []string{"nobody@example.invalid", "DEADBEEF"}}}
		srv := h.Serve(passuitest.Options{Backup: fake.Backup})

		res, body := postBackup(t, srv, htmx)

		if res.StatusCode != http.StatusConflict {
			t.Errorf("htmx=%v: status = %d, want 409:\n%s", htmx, res.StatusCode, body)
		}
		for _, s := range []string{
			"ストアの鍵が見つかりません: <code>nobody@example.invalid</code>, <code>DEADBEEF</code>",
			"<code>.gpg-id</code> と <code>PASSWORD_STORE_KEY</code> を確かめてください。バックアップ先には何も置いていません。",
		} {
			if !strings.Contains(body, s) {
				t.Errorf("htmx=%v: body lacks %q:\n%s", htmx, s, body)
			}
		}
		if strings.Contains(body, "エラー ID") {
			t.Errorf("htmx=%v: body shows an error ID:\n%s", htmx, body)
		}
		if got := strings.Contains(body, "<html"); got != !htmx {
			t.Errorf("htmx=%v: full page = %v:\n%s", htmx, got, body)
		}
		if logs := h.Logs(); !strings.Contains(logs, "nobody@example.invalid") || !strings.Contains(logs, "DEADBEEF") {
			t.Errorf("htmx=%v: logs lack the unresolved key IDs:\n%s", htmx, logs)
		}
	}
}

func TestBackupLogsNumberOfStoreKeys(t *testing.T) {
	h := passuitest.New(t)
	fake := &fakeBackup{sum: backup.Summary{Path: backupPath, Keys: []string{"AAAA", "BBBB"}}}
	srv := h.Serve(passuitest.Options{Backup: fake.Backup})

	if res, body := postBackup(t, srv, true); res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d:\n%s", res.StatusCode, body)
	}

	if logs := h.Logs(); !strings.Contains(logs, "op=backup") || !strings.Contains(logs, "store_keys=2") {
		t.Errorf("logs lack the number of store keys:\n%s", logs)
	}
}
