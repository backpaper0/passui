package server

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/backpaper0/passui/internal/export"
)

// defaultImportLimit は、インポートで受け付けるリクエストの大きさの既定の上限（バイト）。
// README の「インポート」に書いたものと揃える。
const defaultImportLimit = 64 << 20

// importData は import_page.html（インポートの結果ページ）に渡す。
type importData struct {
	// Done はインポートできたか。
	Done bool
	// Message は失敗したときのメッセージ。
	Message string
	// HasErrorID は、Message がエラー ID 付きの想定外の失敗か。途中まで戻したかもしれない。
	HasErrorID bool
	// Keygrips は、同じ keygrip の .key がゲストにすでにあって断ったときの、その keygrip。
	Keygrips []string
}

// importFile は POST /import（左ペインのインポートのフォーム）。送られたエクスポートファイルから、
// 空のゲストにストアとストアの鍵を戻す。ファイルを送るので htmx を使わない普通のフォームの
// POST で、結果は結果ページに出す。passui はパスフレーズを扱わないので、戻した鍵で復号
// できるかは確かめず、ターミナルで解錠して確かめるよう案内する。
//
// ストアが空でないときに送られても、Import が何も書かずに断る（入口を出すかどうかとは別に
// 確かめる）。ファイルはメモリに溜めず、Import に流す。
func (s *handler) importFile(w http.ResponseWriter, r *http.Request) {
	limit := s.cfg.ImportLimit
	if limit == 0 {
		limit = defaultImportLimit
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	noFile := func() {
		s.render(w, r, http.StatusBadRequest, "import_page.html", importData{Message: "エクスポートファイルを選んで送ってください。"})
	}
	mr, err := r.MultipartReader()
	if err != nil {
		noFile()
		return
	}
	for {
		part, err := mr.NextPart()
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status, data := s.importFailure(r, err)
			s.render(w, r, status, "import_page.html", data)
			return
		}
		if err != nil {
			// file のフィールドがないか、multipart として読めない。
			noFile()
			return
		}
		if part.FormName() != "file" {
			continue
		}
		sum, err := s.cfg.Import(r.Context(), part)
		if err != nil {
			status, data := s.importFailure(r, err)
			s.render(w, r, status, "import_page.html", data)
			return
		}
		s.logOperation(r, "import", "", "top", sum.Top, "store_keys", sum.Keys, "secret_keys", sum.SecretKeys)
		s.render(w, r, http.StatusOK, "import_page.html", importData{Done: true})
		return
	}
}

// importFailure はインポートの失敗を、応答のステータスと結果ページに渡すものに分類する。
// 利用者が直せる失敗は、画面に原因を出し、ログには warn で原因だけを出す。想定外の失敗
// （500）はエラー ID 付きでログに出す（エラー文はブラウザに出さない）。
func (s *handler) importFailure(r *http.Request, err error) (int, importData) {
	var tooLarge *http.MaxBytesError
	var bad *export.InvalidExportFileError
	var exists *export.SecretKeyExistsError
	var status int
	var msg string
	var grips []string
	switch {
	case errors.As(err, &tooLarge):
		status, msg = http.StatusRequestEntityTooLarge, "ファイルが大きすぎます（上限は "+formatBytes(tooLarge.Limit)+"）。"
	case errors.Is(err, export.ErrStoreNotEmpty):
		status, msg = http.StatusConflict, "ストアが空ではないので、インポートしませんでした。インポートは、ストアがないか空のゲストにだけできます。"
	case errors.Is(err, export.ErrSecretKeyExists):
		status, msg = http.StatusConflict, "同じ秘密鍵がゲストにすでにあるので、インポートしませんでした。インポートは、ストアの鍵の秘密鍵がまだないゲストにだけできます。"
		if errors.As(err, &exists) {
			// keygrip は画面にだけ出す（err の文には入っていないので、ログには出ない）。
			grips = exists.Keygrips
		}
	case errors.As(err, &bad):
		status, msg = http.StatusBadRequest, "エクスポートファイルとして受け付けられません: "+bad.Reason
	default:
		id := logFailure(r.Context(), s.logger, "import", "err", err)
		return http.StatusInternalServerError, importData{Message: failureMessage(id), HasErrorID: true}
	}
	s.logger.WarnContext(r.Context(), "import failed", "op", "import", "err", err)
	return status, importData{Message: msg, Keygrips: grips}
}

// formatBytes は n バイトを、MiB で割り切れれば MiB で、そうでなければバイトで表す。
func formatBytes(n int64) string {
	if n >= 1<<20 && n%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d バイト", n)
}
