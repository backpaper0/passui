package server

import (
	"net/http"
	"strings"

	"github.com/backpaper0/passui/internal/store"
)

// treeData は tree.html（左ペインのツリー）に渡す。
type treeData struct {
	Root *store.Folder
	// Failure が空でなければ、ツリーの代わりにこれを出す。
	Failure string
	// Importable は、ストアのディレクトリがないか空で、インポートの入口を出すか。
	Importable bool
}

// Empty はストアにエントリが 1 つもないかを返す。
func (d treeData) Empty() bool {
	return d.Root == nil || (len(d.Root.Folders) == 0 && len(d.Root.Entries) == 0)
}

// loadTree はストアのツリーを読む。失敗したら、ブラウザに出すメッセージを
// Failure に入れ、応答のステータスと一緒に返す。
func (s *handler) loadTree(r *http.Request) (treeData, int) {
	entries, err := s.cfg.Store.Entries(r.Context())
	if err != nil {
		status, msg := s.storeFailure(r, err, "list entries")
		return treeData{Failure: msg}, status
	}
	data := treeData{Root: store.BuildTree(entries)}
	if len(entries) == 0 {
		data.Importable = s.storeEmpty(r)
	}
	return data, http.StatusOK
}

// storeEmpty は、ストアのディレクトリがないか空かを返す。確かめられなければ、ログに出して
// false（インポートの入口を出さない）とする。
func (s *handler) storeEmpty(r *http.Request) bool {
	empty, err := s.cfg.StoreEmpty(r.Context())
	if err != nil {
		s.logger.WarnContext(r.Context(), "cannot check whether the store is empty", "err", err)
		return false
	}
	return empty
}

// tree は GET /tree。左ペインのツリーだけを返す（htmx で差し替える）。
// 検索語 q が空でなければ、ツリーの代わりに検索結果を返す（search.go）。
func (s *handler) tree(w http.ResponseWriter, r *http.Request) {
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		s.search(w, r, q)
		return
	}
	data, status := s.loadTree(r)
	s.render(w, r, status, "tree.html", data)
}
