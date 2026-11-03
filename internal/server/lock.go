package server

import "net/http"

// badgeState はロック状態のバッジの状態。値は CSS のクラス（badge-<状態>）に使う。
// ゼロ値（空）はバッジを出さないことを表す。
type badgeState string

const (
	badgeUnlocked badgeState = "unlocked"
	badgeLocked   badgeState = "locked"
	// badgeUnknown はロック状態を取得できなかった。
	badgeUnknown badgeState = "unknown"
)

// badgeData は lock-badge（ヘッダーのロック状態のバッジ）に渡す。
type badgeData struct {
	State badgeState
	// OOB が true なら hx-swap-oob を付ける。右ペインの応答に添えて、
	// ヘッダーのバッジを一緒に差し替えるのに使う。
	OOB bool
}

// Text はバッジに出す文言。
func (d badgeData) Text() string {
	switch d.State {
	case badgeUnlocked:
		return "解錠中"
	case badgeLocked:
		return "ロック中"
	}
	return "状態不明"
}

// loadBadge はストアのロック状態を取得してバッジにする。取得に失敗したら、
// ログにエラー ID 付きで出して「状態不明」にする。
func (s *handler) loadBadge(r *http.Request) badgeData {
	locked, err := s.cfg.Store.Locked(r.Context())
	if err != nil {
		logFailure(r.Context(), s.logger, "lock status", "err", err)
		return badgeData{State: badgeUnknown}
	}
	if locked {
		return badgeData{State: badgeLocked}
	}
	return badgeData{State: badgeUnlocked}
}

// lockChangedEvent は、ロック状態が変わったときにバッジを読み直させる htmx のイベント
// （badge.html の hx-trigger）。
const lockChangedEvent = "lock-changed"

// lock は POST /lock（ヘッダーのロック ボタン）。gpg-agent のキャッシュを消す。
// ログインは続く。htmx からは右ペインを差し替える応答を返し、詳細や編集中の入力を
// 確認なしで破棄する（失敗してもエラーで差し替える）。バッジは成否によらず読み直させる。
// htmx 以外（JS なし）からは / に戻す。
func (s *handler) lock(w http.ResponseWriter, r *http.Request) {
	err := s.cfg.Store.Lock(r.Context())
	if !isHTMX(r) {
		if err != nil {
			logFailure(r.Context(), s.logger, "lock", "err", err)
		} else {
			s.logOperation(r, "lock", "")
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("HX-Trigger", lockChangedEvent)
	if err != nil {
		status, msg := s.storeFailure(r, err, "lock")
		s.render(w, r, status, "failure.html", msg)
		return
	}
	s.logOperation(r, "lock", "")
	s.render(w, r, http.StatusOK, "lock_done.html", nil)
}

// lockStatus は GET /lock-status。ヘッダーのバッジだけを返す（htmx で差し替える）。
// 応答に HX-Trigger: lock-changed を付けると、バッジがここから読み直される（lock）。
func (s *handler) lockStatus(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "lock-badge", s.loadBadge(r))
}
