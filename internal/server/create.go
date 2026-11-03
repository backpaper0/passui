package server

import (
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/backpaper0/passui/internal/secret"
	"github.com/backpaper0/passui/internal/store"
)

// treeChangedEvent は、書き込みの後にツリーを読み直させる htmx のイベント。
// 書き込み系のハンドラは、成功したら応答に `HX-Trigger: tree-changed` を付ける
// （index.html の #tree がこのイベントで /tree を読み直す）。
const treeChangedEvent = "tree-changed"

// triggerTreeReload は応答にツリーの読み直しのイベントを付ける。
func triggerTreeReload(w http.ResponseWriter) {
	w.Header().Set("HX-Trigger", treeChangedEvent)
}

// logOperation は閲覧と書き込みの操作種別とエントリのパスを info でログに出す。
// エントリの中身やフォームの値は渡さないこと。
func (s *handler) logOperation(r *http.Request, op, entry string) {
	s.logger.InfoContext(r.Context(), "operation", "op", op, "entry", entry)
}

// newEntryData は new.html（新規作成フォーム）に渡す。
type newEntryData struct {
	// Path はパス欄の値。最初は既定のフォルダ（"email/" など。ルートなら空）。
	Path string
	// Content は本文の textarea の値（入力のやり直しのときだけ。平文なのでログに出さない）。
	Content string
	// Selected は選んでいたエントリ。もう一度「＋ 新規エントリ」を押したときにも使う。
	Selected string
	// Failure が空でなければ、フォームの上に出す。
	Failure string
}

// createdData は created.html（作ったエントリ）に渡す。
type createdData struct {
	Path string
}

// newEntryForm は GET /new。新規作成フォームを右ペインに返す。クエリの selected は
// 選んでいるエントリ（#selected-entry の値）で、既定のフォルダはそのフォルダにする。
func (s *handler) newEntryForm(w http.ResponseWriter, r *http.Request) {
	selected := r.URL.Query().Get("selected")
	if store.CheckNewName(selected) != nil {
		selected = ""
	}
	s.render(w, r, http.StatusOK, "new.html", newEntryData{Path: defaultFolder(selected), Selected: selected})
}

// defaultFolder は選んでいるエントリのフォルダ（末尾 "/" 付き）を返す。
// 選んでいない、またはルートのエントリなら空。
func defaultFolder(selected string) string {
	if dir := path.Dir(selected); selected != "" && dir != "." {
		return dir + "/"
	}
	return ""
}

// createEntry は POST /new。手で入力した本文で新しいエントリを作る。成功したらツリーを
// 読み直させ、作ったエントリを右ペインに出す。失敗したら入力を残したフォームを返す。
func (s *handler) createEntry(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeFailure(w, r, s.logger, "parse form", "err", err)
		return
	}
	data := newEntryData{
		Path: r.PostForm.Get("path"),
		// textarea は改行を CRLF で送ってくる。ストアは端末の pass と同じ LF にする。
		Content:  strings.ReplaceAll(r.PostForm.Get("content"), "\r\n", "\n"),
		Selected: r.PostForm.Get("selected"),
	}
	if data.Content == "" {
		data.Failure = "本文を入力してください"
		s.render(w, r, http.StatusBadRequest, "new.html", data)
		return
	}

	err := s.cfg.Store.Create(r.Context(), data.Path, secret.Of(data.Content))
	if err != nil {
		status, msg := s.storeFailure(r, err, "create entry", "entry", data.Path)
		if errors.Is(err, store.ErrIsDirectory) && strings.HasSuffix(data.Path, "/") {
			msg = "パスの末尾が / です。エントリの名前まで入力してください"
		}
		data.Failure = msg
		s.render(w, r, status, "new.html", data)
		return
	}
	s.logOperation(r, "create", data.Path)
	triggerTreeReload(w)
	// TODO(#29): 詳細表示ができたら、作ったエントリの詳細に差し替える。
	s.render(w, r, http.StatusOK, "created.html", createdData{Path: data.Path})
}
