package server

import (
	"errors"
	"net/http"
)

// backupData は backup-result（バックアップの結果）に渡す。
type backupData struct {
	// Path は置いたバックアップの完全なパス。空なら失敗。
	Path string
	// Failure は失敗したときのメッセージ（エラー ID 付き）。
	Failure string
}

// backup は POST /backup（ヘッダーのバックアップ ボタン）。確認なしですぐバックアップを
// 作り、バックアップ先に置く。ロック中かどうかで流れは変わらない。htmx からは右ペインを
// 結果で差し替え、詳細や編集中の入力を確認なしで破棄する。htmx 以外（JS なし）からは、
// 置いたパスを見失わないよう、/ に戻さずに同じ結果を最小限のページで返す。
func (s *handler) backup(w http.ResponseWriter, r *http.Request) {
	status, data := s.runBackup(r)
	name := "backup-result"
	if !isHTMX(r) {
		name = "backup_page.html"
	}
	s.render(w, r, status, name, data)
}

// runBackup はバックアップを作り、応答のステータスと結果を返す。失敗はエラー ID 付きで
// ログに出す（エラー文はブラウザに出さない）。
func (s *handler) runBackup(r *http.Request) (int, backupData) {
	if s.cfg.Backup == nil {
		id := logFailure(r.Context(), s.logger, "backup", "err", errors.New("backup is not configured"))
		return http.StatusInternalServerError, backupData{Failure: failureMessage(id)}
	}
	sum, err := s.cfg.Backup(r.Context())
	if err != nil {
		id := logFailure(r.Context(), s.logger, "backup", "dir", s.cfg.BackupDir, "err", err)
		return http.StatusInternalServerError, backupData{Failure: failureMessage(id)}
	}
	s.logOperation(r, "backup", "", "path", sum.Path)
	return http.StatusOK, backupData{Path: sum.Path}
}
