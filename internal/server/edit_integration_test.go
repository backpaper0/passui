package server_test

import (
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/backpaper0/passui/internal/passuitest"
)

var hashField = regexp.MustCompile(`name="hash" value="([0-9a-f]{64})"`)

// openEditHash は編集フォームを開き、hidden 欄の暗号文のハッシュを返す。
func openEditHash(t *testing.T, srv *passuitest.Server, name string) string {
	t.Helper()
	res, body := openEdit(t, srv, name)
	m := hashField.FindStringSubmatch(body)
	if res.StatusCode != http.StatusOK || m == nil {
		t.Fatalf("open edit %s: status = %d, no hash:\n%s", name, res.StatusCode, body)
	}
	return m[1]
}

// passRm は外から（ターミナルの pass で）エントリを消す。
func passRm(t *testing.T, g *passuitest.GPGStore, name string) {
	t.Helper()
	cmd := exec.Command("pass", "rm", "-f", name)
	cmd.Env = g.Env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pass rm %s: %v: %s", name, err, out)
	}
}

func TestIntegrationEditedEntryIsReadableWithPass(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("email/work", h.Canary("old"))
	g.Unlock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})
	hash := openEditHash(t, srv, "email/work")
	content := h.Canary("password") + "\nuser: " + h.Canary("user")

	res := postEdit(srv, "email/work", url.Values{"path": {"email/work"}, "hash": {hash}, "content": {content}})

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", res.StatusCode, readBody(t, res))
	}
	if got := g.Show("email/work"); got != content+"\n" {
		t.Errorf("pass show = %q, want %q", got, content+"\n")
	}
}

// 競合 1: 開いた後に外から pass insert -f で書き換えられた。
func TestIntegrationEditConflictsWithExternalInsertThenOverwrite(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("bank", h.Canary("old"))
	g.Unlock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})
	hash := openEditHash(t, srv, "bank")
	outside := h.Canary("outside")
	g.Insert("bank", outside)
	input := h.Canary("input")

	res := postEdit(srv, "bank", url.Values{"path": {"bank"}, "hash": {hash}, "content": {input}})

	body := readBody(t, res)
	if res.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409:\n%s", res.StatusCode, body)
	}
	if !strings.Contains(body, input) || !strings.Contains(body, "それでも上書きする") {
		t.Errorf("conflict does not keep the input or offer overwrite:\n%s", body)
	}
	if got := g.Show("bank"); got != outside {
		t.Errorf("external change is lost: %q", got)
	}

	m := regexp.MustCompile(`name="overwrite" value="([0-9a-f]{64})"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no overwrite button:\n%s", body)
	}
	res = postEdit(srv, "bank", url.Values{"path": {"bank"}, "hash": {hash}, "content": {input}, "overwrite": {m[1]}})

	if res.StatusCode != http.StatusOK {
		t.Fatalf("overwrite status = %d, want 200:\n%s", res.StatusCode, readBody(t, res))
	}
	if got := g.Show("bank"); got != input+"\n" {
		t.Errorf("pass show after overwrite = %q, want %q", got, input+"\n")
	}
}

// 競合 2: 開いた後に外から pass rm された。
func TestIntegrationEditOfRemovedEntryIsConflictAndNotRecreated(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("email/work", h.Canary("old"))
	g.Insert("email/personal", h.Canary("other"))
	g.Unlock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})
	hash := openEditHash(t, srv, "email/work")
	passRm(t, g, "email/work")
	before := snapshot(t, g.Dir)
	input := h.Canary("input")

	res := postEdit(srv, "email/work", url.Values{"path": {"email/work"}, "hash": {hash}, "content": {input}})

	body := readBody(t, res)
	if res.StatusCode != http.StatusConflict || !strings.Contains(body, "削除または移動されました") || !strings.Contains(body, input) {
		t.Errorf("status = %d, want 409 with the input kept:\n%s", res.StatusCode, body)
	}
	if res.Header.Get("HX-Trigger") != "tree-changed" {
		t.Errorf("tree is not reloaded")
	}
	if after := snapshot(t, g.Dir); !maps.Equal(before, after) {
		t.Errorf("store changed (entry recreated?):\nbefore %v\nafter  %v", before, after)
	}
}

// 並行保存: 同じエントリへ、同じ版から保存を並行して投げる。確認から書き込みまでが
// mutex の中なので、通るのは 1 つだけで、ストアの中身はその 1 つと一致する。
func TestIntegrationConcurrentEditsDoNotCorruptStore(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("bank", h.Canary("old"))
	g.Unlock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})
	hash := openEditHash(t, srv, "bank")

	const n = 6
	contents := make([]string, n)
	statuses := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		contents[i] = h.Canary(fmt.Sprintf("content%d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := postEdit(srv, "bank", url.Values{"path": {"bank"}, "hash": {hash}, "content": {contents[i]}})
			res.Body.Close()
			statuses[i] = res.StatusCode
		}()
	}
	wg.Wait()

	saved := -1
	for i, st := range statuses {
		switch st {
		case http.StatusOK:
			if saved >= 0 {
				t.Errorf("both save %d and %d succeeded from the same version", saved, i)
			}
			saved = i
		case http.StatusConflict:
		default:
			t.Errorf("save %d: status = %d", i, st)
		}
	}
	if saved < 0 {
		t.Fatalf("no save succeeded: %v", statuses)
	}
	if got := g.Show("bank"); got != contents[saved]+"\n" {
		t.Errorf("pass show = %q, want the successful save %q", got, contents[saved]+"\n")
	}
}

func TestIntegrationOpenEditWhileLockedShowsUnlockGuide(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	password := h.Canary("password")
	g.Insert("dev/github token", password)
	g.Lock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})

	res, body := openEdit(t, srv, "dev/github%20token")

	if res.StatusCode != http.StatusLocked {
		t.Errorf("status = %d, want 423:\n%s", res.StatusCode, body)
	}
	for _, want := range []string{"pass show &#39;dev/github token&#39; &gt;/dev/null", `hx-get="/edit/dev/github%20token"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, password) || strings.Contains(body, "<textarea") {
		t.Errorf("locked response contains the entry or the form:\n%s", body)
	}
}

// 競合 4: パスを変えて保存すると移動・リネームになる。古いパスは消え、新しいパスに
// 中身がある（宛先のフォルダは pass が作る）。
func TestIntegrationEditWithNewPathMovesEntry(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("email/work", h.Canary("old"))
	g.Unlock()
	srv := h.Serve(passuitest.Options{Store: g.Backend()})
	hash := openEditHash(t, srv, "email/work")
	content := h.Canary("password")

	res := postEdit(srv, "email/work", url.Values{"path": {"archive/email/work"}, "hash": {hash}, "content": {content}})

	body := readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `data-entry="archive/email/work"`) {
		t.Fatalf("status = %d, want 200 with the moved entry:\n%s", res.StatusCode, body)
	}
	if res.Header.Get("HX-Trigger") != "tree-changed" {
		t.Errorf("tree is not reloaded")
	}
	if got := g.Show("archive/email/work"); got != content+"\n" {
		t.Errorf("pass show new path = %q, want %q", got, content+"\n")
	}
	if _, ok := snapshot(t, g.Dir)["email/work.gpg"]; ok {
		t.Errorf("old entry still exists")
	}
}

// 競合 3: 移動の宛先にエントリか同名のディレクトリがある（末尾が / のときも）と
// エラーになり、どちらのエントリも変わらない（上書きする手段は持たない）。
func TestIntegrationEditMoveToTakenDestinationIsRejectedAndKeepsStore(t *testing.T) {
	for _, tc := range []struct {
		name, path, message string
	}{
		{"existing entry", "email/personal", "同じ名前のエントリが既にあります"},
		{"directory with the same name", "email", "同じ名前のフォルダがあります"},
		{"trailing slash", "archive/", "パスの末尾が / です"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := passuitest.New(t)
			g := passuitest.NewGPGStore(h)
			work, personal := h.Canary("work"), h.Canary("personal")
			g.Insert("email/work", work)
			g.Insert("email/personal", personal)
			g.Unlock()
			srv := h.Serve(passuitest.Options{Store: g.Backend()})
			hash := openEditHash(t, srv, "email/work")
			before := snapshot(t, g.Dir)
			input := h.Canary("input")

			res := postEdit(srv, "email/work", url.Values{"path": {tc.path}, "hash": {hash}, "content": {input}})

			body := readBody(t, res)
			if res.StatusCode != http.StatusConflict || !strings.Contains(body, tc.message) || !strings.Contains(body, input) {
				t.Errorf("status = %d, want 409 with %q and the input kept:\n%s", res.StatusCode, tc.message, body)
			}
			if after := snapshot(t, g.Dir); !maps.Equal(before, after) {
				t.Errorf("store changed:\nbefore %v\nafter  %v", before, after)
			}
			if got := g.Show("email/work"); got != work {
				t.Errorf("edited entry changed: %q", got)
			}
			if got := g.Show("email/personal"); got != personal {
				t.Errorf("destination entry changed: %q", got)
			}
		})
	}
}

// mv だけが失敗する（宛先の途中の要素がフォルダでなくファイル）と、内容は元のパスに
// 保存され、そのことが伝わる。
func TestIntegrationEditMoveFailureKeepsSavedContentAtOldPath(t *testing.T) {
	h := passuitest.New(t)
	g := passuitest.NewGPGStore(h)
	g.Insert("bank", h.Canary("old"))
	g.Unlock()
	if err := os.WriteFile(filepath.Join(g.Dir, "blocker"), []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := h.Serve(passuitest.Options{Store: g.Backend()})
	hash := openEditHash(t, srv, "bank")
	input := h.Canary("input")

	res := postEdit(srv, "bank", url.Values{"path": {"blocker/bank"}, "hash": {hash}, "content": {input}})

	body := readBody(t, res)
	if !strings.Contains(body, "移動に失敗しました") || !strings.Contains(body, input) {
		t.Errorf("status = %d, want the move failure with the input kept:\n%s", res.StatusCode, body)
	}
	if got := g.Show("bank"); got != input+"\n" {
		t.Errorf("content is not saved at the old path: %q", got)
	}
	if m := hashField.FindStringSubmatch(body); m == nil || m[1] == hash {
		t.Errorf("hash in the form is not the saved version:\n%s", body)
	}
}
