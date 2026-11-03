package server

import (
	"errors"
	"net/http"

	"github.com/backpaper0/passui/internal/store"
)

// deleteURL はエントリの削除（確認と実行）の URL（/delete/<name>）を作る。
func deleteURL(name string) string {
	return "/delete/" + escapePath(name)
}

// DeleteURL は詳細の見出しの「削除」が開く確認の URL。
func (d entryData) DeleteURL() string { return deleteURL(d.Name) }

// DeleteURL はロック中の案内の「削除」が開く確認の URL（削除は復号しないのでロック中でもできる）。
func (d lockedData) DeleteURL() string { return deleteURL(d.Name) }

// deleteData は delete.html（削除の確認）と deleted.html（削除の結果）に渡す。
type deleteData struct {
	Name string
	// Failure が空でなければ、確認の上に出す。
	Failure string
}

// DeleteURL は確認の「削除する」が POST する URL。
func (d deleteData) DeleteURL() string { return deleteURL(d.Name) }

// EntryURL は確認の「やめる」で戻る詳細の URL。
func (d deleteData) EntryURL() string { return entryURL(d.Name) }

// confirmDelete は GET /delete/{name...}。右ペインに削除の確認を返す。ストアは変えない。
func (s *handler) confirmDelete(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "delete.html", deleteData{Name: r.PathValue("name")})
}

// deleteEntry は POST /delete/{name...}。確認を経た（フォームに confirm=yes がある）ときだけ
// エントリを消す。消したらツリーを読み直させる。外で消されていたら「見つかりません」を出し、
// やはりツリーを読み直させる。
func (s *handler) deleteEntry(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := r.ParseForm(); err != nil {
		writeFailure(w, r, s.logger, "parse form", "err", err)
		return
	}
	if r.PostForm.Get("confirm") != "yes" {
		s.render(w, r, http.StatusBadRequest, "delete.html", deleteData{Name: name, Failure: "削除するには確認してください"})
		return
	}
	if err := s.cfg.Store.Delete(r.Context(), name); err != nil {
		status, msg := s.storeFailure(r, err, "delete entry", "entry", name)
		if errors.Is(err, store.ErrNotFound) {
			triggerTreeReload(w)
			s.render(w, r, status, "failure.html", msg)
			return
		}
		s.render(w, r, status, "delete.html", deleteData{Name: name, Failure: msg})
		return
	}
	s.logOperation(r, "delete", name)
	triggerTreeReload(w)
	s.render(w, r, http.StatusOK, "deleted.html", deleteData{Name: name})
}
