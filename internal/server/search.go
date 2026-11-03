package server

import (
	"net/http"
	"unicode"
)

// searchData は search.html（左ペインの検索結果）に渡す。
type searchData struct {
	Results []searchResult
	// Failure が空でなければ、結果の代わりにこれを出す。
	Failure string
}

// searchResult は検索に一致したエントリ 1 つ。Parts は一致した部分（Match）とそれ以外に分けたパス。
type searchResult struct {
	Path  string
	Parts []searchPart
}

type searchPart struct {
	Text  string
	Match bool
}

// search は GET /tree?q=... で検索語が空でないとき。ストアのファイル名（Entries）だけを使うので、
// ロック中でも動く。エントリの中身は見ない。検索語はログに出さない（アクセスログもクエリを落とす）。
func (s *handler) search(w http.ResponseWriter, r *http.Request, q string) {
	entries, err := s.cfg.Store.Entries(r.Context())
	if err != nil {
		status, msg := s.storeFailure(r, err, "search entries")
		s.render(w, r, status, "search.html", searchData{Failure: msg})
		return
	}
	var data searchData
	for _, e := range entries {
		if parts, ok := matchParts(e, q); ok {
			data.Results = append(data.Results, searchResult{Path: e, Parts: parts})
		}
	}
	s.render(w, r, http.StatusOK, "search.html", data)
}

// matchParts は path に q が（大文字小文字を区別せず）含まれるかを返す。含まれるなら、
// 重ならない出現をすべて Match にして path を分ける。
func matchParts(path, q string) ([]searchPart, bool) {
	p, k := []rune(path), []rune(q)
	if len(k) == 0 {
		return nil, false
	}
	var parts []searchPart
	start, matched := 0, false
	for i := 0; i+len(k) <= len(p); {
		if !runesEqualFold(p[i:i+len(k)], k) {
			i++
			continue
		}
		if start < i {
			parts = append(parts, searchPart{Text: string(p[start:i])})
		}
		parts = append(parts, searchPart{Text: string(p[i : i+len(k)]), Match: true})
		matched = true
		i += len(k)
		start = i
	}
	if !matched {
		return nil, false
	}
	if start < len(p) {
		parts = append(parts, searchPart{Text: string(p[start:])})
	}
	return parts, true
}

func runesEqualFold(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] && unicode.ToLower(a[i]) != unicode.ToLower(b[i]) {
			return false
		}
	}
	return true
}
