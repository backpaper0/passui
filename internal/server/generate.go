package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/backpaper0/passui/internal/store"
)

// maxGeneratedLength は生成するパスワードの長さの上限（入力ミスで巨大な値を渡さないため）。
const maxGeneratedLength = 1024

// generateEntry は POST /new で「生成する」（mode=generate）を選んだとき。パスワード 1 行だけの
// エントリを pass generate で作る。長さが空なら pass の既定。失敗したら入力を残したフォームを返す。
// 生成したパスワードはここでは受け取らず、作ったエントリの詳細（showEntry）で出す。
func (s *handler) generateEntry(w http.ResponseWriter, r *http.Request) {
	data := newEntryData{
		Path:      r.PostForm.Get("path"),
		Selected:  r.PostForm.Get("selected"),
		Generate:  true,
		Length:    strings.TrimSpace(r.PostForm.Get("length")),
		NoSymbols: r.PostForm.Get("no-symbols") != "",
	}
	opts, ok := generateOptions(data.Length, data.NoSymbols)
	if !ok {
		data.Failure = "長さは 1 以上 " + strconv.Itoa(maxGeneratedLength) + " 以下の数で入力してください（空なら pass の既定）"
		s.render(w, r, http.StatusBadRequest, "new.html", data)
		return
	}

	if err := s.cfg.Store.Generate(r.Context(), data.Path, opts); err != nil {
		status, msg := s.destinationFailure(r, err, data.Path, "generate entry", "entry", data.Path)
		data.Failure = msg
		s.render(w, r, status, "new.html", data)
		return
	}
	s.logOperation(r, "generate", data.Path)
	triggerTreeReload(w)
	s.showEntry(w, r, data.Path, "パスワードを生成してエントリを作成しました。")
}

// regenerateURL は「パスワードを再生成」の POST 先（/regenerate/<name>）。
// テンプレートでは関数 regenerateURL として使える。
func regenerateURL(name string) string {
	return "/regenerate/" + escapePath(name)
}

// regenerateEntry は POST /regenerate/{name...}。エントリの 1 行目だけを pass の既定で
// 生成し直し、詳細を返す。ロック中なら閲覧と同じ解錠の案内（再試行は閲覧の URL。
// フォーカスで勝手に再生成しないため）。エントリが消えていたら「見つかりません」と
// ツリーの読み直し。
func (s *handler) regenerateEntry(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.cfg.Store.Regenerate(r.Context(), name, store.GenerateOptions{}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			triggerTreeReload(w)
		}
		s.entryFailure(w, r, name, entryURL(name), "", err, "regenerate entry")
		return
	}
	s.logOperation(r, "regenerate", name)
	triggerTreeReload(w)
	s.showEntry(w, r, name, "パスワードを再生成しました。")
}

// generateOptions はフォームの長さ（空なら pass の既定）と記号なしを GenerateOptions にする。
func generateOptions(length string, noSymbols bool) (store.GenerateOptions, bool) {
	opts := store.GenerateOptions{NoSymbols: noSymbols}
	if length == "" {
		return opts, true
	}
	n, err := strconv.Atoi(length)
	if err != nil || n < 1 || n > maxGeneratedLength {
		return opts, false
	}
	opts.Length = n
	return opts, true
}
