package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/backpaper0/passui/internal/secret"
	"github.com/backpaper0/passui/internal/store"
)

// editURL は編集フォームの URL（/edit/<name>）を作る。GET で開き、POST で保存する。
// テンプレートでは関数 editURL として使える。
func editURL(name string) string {
	return "/edit/" + escapePath(name)
}

// editData は edit.html（編集フォーム）に渡す。
type editData struct {
	// Name は元のパス（URL のパス）。保存はこのエントリに対して行う。
	Name string
	// Path はパス欄の値。Name と違えば、保存で移動・リネームになる。
	Path string
	// Content は本文の textarea の値（平文なのでログに出さない）。
	Content string
	// Hash は開いた時点の暗号文のハッシュ。保存のときに比べる。
	Hash string
	// Failure が空でなければ、フォームの上に出す。
	Failure string
	// Conflict なら「最新を読み直す（入力は破棄）」を出す。
	Conflict bool
	// OverwriteHash が空でなければ「それでも上書きする」を出す。競合を見つけた時点の
	// 暗号文のハッシュで、押すとこのハッシュと比べて保存する（その後にまた変わって
	// いれば、もう一度競合になる）。エントリが消えていたときは空（作り直さない）。
	OverwriteHash string
	// Badge は、ヘッダーのバッジを差し替える（hx-swap-oob）ためのロック状態。空なら出さない。
	Badge badgeData
}

// editForm は GET /edit/{name...}。エントリを復号し、本文を平文で入れた編集フォームを
// 返す。ロック中なら閲覧と同じ解錠の案内を返す（再試行はこのフォームを開き直す）。
func (s *handler) editForm(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	// ハッシュは復号の前に取る。間に外で変わっても、保存のときに競合として見つかる
	// （古い平文に新しいハッシュを組み合わせて、外の変更を黙って潰すことはない）。
	hash, err := s.cfg.Store.Hash(r.Context(), name)
	if err != nil {
		s.entryFailure(w, r, name, editURL(name), "", err, "open edit form")
		return
	}
	plain, err := s.cfg.Store.Show(r.Context(), name)
	if err != nil {
		s.entryFailure(w, r, name, editURL(name), "", err, "open edit form")
		return
	}
	s.logOperation(r, "open-edit", name)
	s.render(w, r, http.StatusOK, "edit.html", editData{
		Name:    name,
		Path:    name,
		Content: string(plain),
		Hash:    hash,
		Badge:   badgeData{State: badgeUnlocked, OOB: true},
	})
}

// saveEdit は POST /edit/{name...}。開いた時点のハッシュ（hash）と比べてから、本文を
// 保存する。競合したら入力を残したフォームを 409 で返す。
func (s *handler) saveEdit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeFailure(w, r, s.logger, "parse form", "err", err)
		return
	}
	data := editData{
		Name: r.PathValue("name"),
		Path: r.PostForm.Get("path"),
		// textarea は改行を CRLF で送ってくる。ストアは端末の pass と同じ LF にする。
		Content: strings.ReplaceAll(r.PostForm.Get("content"), "\r\n", "\n"),
		Hash:    r.PostForm.Get("hash"),
	}
	expected := data.Hash
	if overwrite := r.PostForm.Get("overwrite"); overwrite != "" {
		// 「それでも上書きする」: 競合を見つけた時点の版に対して保存する。
		expected = overwrite
	}
	switch {
	case data.Path == "":
		data.Failure = "パスを入力してください"
		s.render(w, r, http.StatusBadRequest, "edit.html", data)
		return
	case data.Content == "":
		data.Failure = "本文を入力してください"
		s.render(w, r, http.StatusBadRequest, "edit.html", data)
		return
	}

	// パス欄を変えていれば移動・リネーム（元のパスに保存してから mv する）。
	e := store.Edit{
		Name:    data.Name,
		NewName: data.Path,
		Hash:    expected,
		Content: secret.Of(data.Content),
	}
	if err := s.cfg.Store.Update(r.Context(), e); err != nil {
		s.editFailure(w, r, data, err)
		return
	}
	s.logOperation(r, "edit", data.Name)
	if !e.Moves() {
		s.showEntry(w, r, data.Name, "エントリを保存しました。")
		return
	}
	s.logger.InfoContext(r.Context(), "operation", "op", "move", "entry", data.Name, "to", data.Path)
	triggerTreeReload(w)
	s.showEntry(w, r, data.Path, "エントリを保存し、"+data.Path+" に移動しました。")
}

// moveFailure は、本文は元のパスに保存できたが移動だけが失敗したことを、入力を残した
// フォームで伝える。保存で暗号文が変わったので、ハッシュを今のものにしておく（もう一度
// 「保存」を押すと移動をやり直せる）。ツリーは読み直させる。
func (s *handler) moveFailure(w http.ResponseWriter, r *http.Request, data editData, err error) {
	id := logFailure(r.Context(), s.logger, "move entry", "entry", data.Name, "to", data.Path, "err", err)
	data.Failure = "内容は " + data.Name + " に保存しましたが、" + data.Path +
		" への移動に失敗しました（エラー ID: " + id + "）。エントリは元のパスのままです"
	data.Hash = ""
	if current, herr := s.cfg.Store.Hash(r.Context(), data.Name); herr == nil {
		data.Hash = current
	}
	triggerTreeReload(w)
	s.render(w, r, http.StatusInternalServerError, "edit.html", data)
}

// editFailure は保存の失敗を、入力を残したフォームで返す。
func (s *handler) editFailure(w http.ResponseWriter, r *http.Request, data editData, err error) {
	if errors.Is(err, store.ErrMoveFailed) {
		s.moveFailure(w, r, data, err)
		return
	}
	if !errors.Is(err, store.ErrConflict) {
		status, msg := s.storeFailure(r, err, "save entry", "entry", data.Name)
		if errors.Is(err, store.ErrIsDirectory) && strings.HasSuffix(data.Path, "/") {
			msg = "パスの末尾が / です。エントリの名前まで入力してください"
		}
		data.Failure = msg
		s.render(w, r, status, "edit.html", data)
		return
	}
	data.Conflict = true
	s.logger.InfoContext(r.Context(), "edit conflict", "entry", data.Name)
	if errors.Is(err, store.ErrNotFound) {
		// 外で削除・移動された。作り直さないので上書きは出さず、ツリーを読み直させる。
		data.Failure = "編集中にエントリがほかで削除または移動されました。保存していません"
		triggerTreeReload(w)
		s.render(w, r, http.StatusConflict, "edit.html", data)
		return
	}
	data.Failure = "編集中にエントリがほかで変更されました。保存していません"
	if current, herr := s.cfg.Store.Hash(r.Context(), data.Name); herr == nil {
		data.OverwriteHash = current
	} else if errors.Is(herr, store.ErrNotFound) {
		data.Failure = "編集中にエントリがほかで削除または移動されました。保存していません"
		triggerTreeReload(w)
	} else {
		status, msg := s.storeFailure(r, herr, "hash entry", "entry", data.Name)
		data.Failure = msg
		s.render(w, r, status, "edit.html", data)
		return
	}
	s.render(w, r, http.StatusConflict, "edit.html", data)
}
