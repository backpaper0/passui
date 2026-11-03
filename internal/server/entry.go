package server

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/backpaper0/passui/internal/store"
)

// entryURL はエントリの詳細の URL（/entry/<name>）を作る。パスの各要素をエスケープする。
// テンプレートでは関数 entryURL として使える。
func entryURL(name string) string {
	return "/entry/" + escapePath(name)
}

// escapePath はエントリ名の "/" を残して各要素をパス用にエスケープする。
// 編集（/edit/...）など、エントリ名を URL のパスに入れるルートで使い回せる。
func escapePath(name string) string {
	parts := strings.Split(name, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// entryContent はエントリの平文を分けたもの。平文はシークレットなので、ログに出さない。
type entryContent struct {
	// Password は 1 行目。
	Password string
	// Fields は 2 行目以降。空行は除く。
	Fields []entryField
}

// entryField は 2 行目以降の 1 行。"key: value" の形なら Key と Value に分け、
// そうでなければ Key は空で、行全体を Value に入れる。
type entryField struct {
	Key   string
	Value string
}

// parseEntry は pass show の出力を 1 行目と 2 行目以降に分ける。
func parseEntry(plain []byte) entryContent {
	text := strings.ReplaceAll(string(plain), "\r\n", "\n")
	first, rest, _ := strings.Cut(text, "\n")
	c := entryContent{Password: first}
	for _, line := range strings.Split(rest, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok && k != "" && !strings.ContainsAny(k, " \t") {
			c.Fields = append(c.Fields, entryField{Key: k, Value: strings.TrimSpace(v)})
			continue
		}
		c.Fields = append(c.Fields, entryField{Value: line})
	}
	return c
}

// entryData は entry.html（右ペインの詳細）に渡す。
type entryData struct {
	Name string
	// Notice が空でなければ、詳細の上に出す（「エントリを作成しました」など）。
	Notice string
	entryContent
	// Badge は、ヘッダーのバッジを差し替える（hx-swap-oob）ためのロック状態。
	Badge badgeData
}

// entry は GET /entry/{name...}。エントリを 1 回だけ復号し、右ペインの詳細を返す。
// ロック中なら中身を返さず、解錠の案内を 423 で返す。
func (s *handler) entry(w http.ResponseWriter, r *http.Request) {
	s.showEntry(w, r, r.PathValue("name"), "")
}

// showEntry はエントリを 1 回だけ復号して詳細を右ペインに返す（閲覧の操作ログも出す）。
// 書き込みの後に結果として詳細を出すときは notice に結果の文言を渡す。そのときは
// 書き込み自体は成功しているので、ロック中でも案内を 200 で返す。
func (s *handler) showEntry(w http.ResponseWriter, r *http.Request, name, notice string) {
	plain, err := s.cfg.Store.Show(r.Context(), name)
	if err != nil {
		s.entryFailure(w, r, name, entryURL(name), notice, err, "show entry")
		return
	}
	s.logOperation(r, "show", name)
	s.render(w, r, http.StatusOK, "entry.html", entryData{
		Name:         name,
		Notice:       notice,
		entryContent: parseEntry(plain),
		Badge:        badgeData{State: badgeUnlocked, OOB: true},
	})
}

// entryFailure は、エントリを復号する操作（閲覧、編集を開く、再生成）の失敗を右ペインに返す。
// ロック中なら解錠の案内（lockedData）を 423 で返す（notice があれば 200）。retryURL は
// 案内の再試行と、フォーカスが戻ったときの確認で GET する URL（閲覧なら entryURL(name)）。
// それ以外は storeFailure で分類したメッセージだけを返す（stderr は出さない）。
func (s *handler) entryFailure(w http.ResponseWriter, r *http.Request, name, retryURL, notice string, err error, op string) {
	if errors.Is(err, store.ErrLocked) {
		status, msg := s.storeFailure(r, err, op)
		if notice != "" {
			status = http.StatusOK
		}
		s.render(w, r, status, "locked.html", lockedData{
			Name:     name,
			Notice:   notice,
			RetryURL: retryURL,
			Message:  msg,
			Badge:    badgeData{State: badgeLocked, OOB: true},
		})
		return
	}
	status, msg := s.storeFailure(r, err, op, "entry", name)
	s.render(w, r, status, "failure.html", msg)
}

// lockedData は locked.html（ロック中の案内）に渡す。
type lockedData struct {
	Name   string
	Notice string
	// RetryURL は再試行とフォーカスが戻ったときに GET する URL。
	RetryURL string
	Message  string
	Badge    badgeData
}

// UnlockCommand は解錠のためのワンライナー。
func (d lockedData) UnlockCommand() string {
	return "pass show " + shellQuote(d.Name) + " >/dev/null"
}

// shellQuote は s をシェルの引数として安全な形にする。英数字と一部の記号だけなら
// そのまま、それ以外はシングルクォートで囲む。
func shellQuote(s string) string {
	safe := s != ""
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_./@+,:=%", c)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
