package server

import (
	"bytes"
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/backpaper0/passui/internal/export"
)

// exportData は export_page.html（エクスポートに失敗したときの結果ページ）に渡す。
type exportData struct {
	// Failure は想定外の失敗のときのメッセージ（エラー ID 付き）。
	Failure string
	// UnresolvedKeys は、公開鍵に解決できなかったストアの鍵の ID（秘密ではない）。
	UnresolvedKeys []string
}

// exportFile は POST /export（ヘッダーのエクスポート ボタン）。確認なしですぐエクスポート
// ファイルを作り、ブラウザにダウンロードさせる。ロック中かどうかで流れは変わらない。
// htmx を使わない普通のフォームの POST なので、成功したときは画面が変わらず、ダウンロード
// だけが始まる。失敗したときは、ダウンロードの代わりに結果ページで原因を出す。
//
// 失敗をダウンロードの前に知るため、エクスポートファイルは全体をメモリに作ってから送る
// （ゲストには何も残さない）。保存できたかどうかは passui からは知れない。
func (s *handler) exportFile(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	sum, err := s.cfg.Export(r.Context(), &buf)
	if err != nil {
		status, data := s.exportFailure(r, err)
		s.render(w, r, status, "export_page.html", data)
		return
	}
	s.logOperation(r, "export", "", "name", sum.Name, "bytes", buf.Len(), "store_keys", len(sum.Keys), "secret_keys", sum.SecretKeys, "skipped_special_files", sum.SkippedSpecialFiles)
	s.logger.DebugContext(r.Context(), "export store keys", "fingerprints", sum.Keys)

	h := w.Header()
	h.Set("Content-Type", "application/gzip")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": sum.Name}))
	h.Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(buf.Bytes()); err != nil {
		// 応答は差し替えられないので、ログにだけ出す。
		s.logger.WarnContext(r.Context(), "export download interrupted", "op", "export", "name", sum.Name, "err", err)
	}
}

// exportFailure はエクスポートの失敗を、応答のステータスと結果ページに渡すものに分類する。
// 想定外の失敗（500）はエラー ID 付きでログに出す（エラー文はブラウザに出さない）。
//
// 利用者が直せる失敗（409: 解決できない鍵 ID）は、画面に原因をそのまま出してエラー ID は
// 出さないので、ログにもエラー ID を付けず、warn で原因だけを出す。エラー ID は、画面から
// ログを辿るためのものなので、画面に出すときだけ作る。
func (s *handler) exportFailure(r *http.Request, err error) (int, exportData) {
	var unresolved *export.UnresolvedKeysError
	if errors.As(err, &unresolved) {
		s.logger.WarnContext(r.Context(), "export failed", "op", "export", "unresolved_keys", unresolved.IDs)
		return http.StatusConflict, exportData{UnresolvedKeys: unresolved.IDs}
	}
	id := logFailure(r.Context(), s.logger, "export", "err", err)
	return http.StatusInternalServerError, exportData{Failure: failureMessage(id)}
}
