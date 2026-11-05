package server

import (
	"errors"
	"io/fs"
	"net/http"

	"github.com/backpaper0/passui/internal/backup"
)

// backupData は backup-result（バックアップの結果）に渡す。
type backupData struct {
	// Path は置いたバックアップの完全なパス。空なら失敗。
	Path string
	// Failure は失敗したときのメッセージ（エラー ID 付き）。
	Failure string
	// UnresolvedKeys は、公開鍵に解決できなかったストアの鍵の ID（秘密ではない）。
	UnresolvedKeys []string
	// Problem は、利用者が直せる失敗（バックアップ先の問題）のときの説明。
	Problem *backupProblem
}

// backupProblem は、何が悪いか（Message）、問題のパス（Path）、直し方（Hint）。
type backupProblem struct {
	Message string
	Path    string
	Hint    string
}

// destinationProblem は、err がバックアップ先の問題（backup.ErrNoDir・ErrNotWritable・
// ErrExists）なら、その説明を返す。パスは err の *fs.PathError から取り、なければ dir を使う。
func destinationProblem(err error, dir string) *backupProblem {
	path := dir
	var pe *fs.PathError
	if errors.As(err, &pe) {
		path = pe.Path
	}
	switch {
	case errors.Is(err, backup.ErrNoDir):
		return &backupProblem{"バックアップ先がありません。", path,
			"passui は明示したバックアップ先を作りません。パスの打ち間違いやマウントを確かめてください。"}
	case errors.Is(err, backup.ErrNotWritable):
		return &backupProblem{"バックアップ先に書けません。", path, "ディレクトリの権限を確かめてください。"}
	case errors.Is(err, backup.ErrExists):
		return &backupProblem{"同じ名前のファイルがすでにあります。", path,
			"上書きはしません。少し待ってから、もう一度押してください。"}
	}
	return nil
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
	if p := destinationProblem(err, s.cfg.BackupDir); p != nil {
		s.logger.WarnContext(r.Context(), "backup failed", "op", "backup", "path", p.Path, "err", err)
		return http.StatusConflict, backupData{Problem: p}
	}
	var unresolved *backup.UnresolvedKeysError
	if errors.As(err, &unresolved) {
		s.logger.WarnContext(r.Context(), "backup failed", "op", "backup", "unresolved_keys", unresolved.IDs)
		return http.StatusConflict, backupData{UnresolvedKeys: unresolved.IDs}
	}
	if err != nil {
		id := logFailure(r.Context(), s.logger, "backup", "dir", s.cfg.BackupDir, "err", err)
		return http.StatusInternalServerError, backupData{Failure: failureMessage(id)}
	}
	s.logOperation(r, "backup", "", "path", sum.Path, "store_keys", len(sum.Keys), "skipped_special_files", sum.SkippedSpecialFiles)
	s.logger.DebugContext(r.Context(), "backup store keys", "fingerprints", sum.Keys)
	return http.StatusOK, backupData{Path: sum.Path}
}
