package server

import "net/http"

// ロック状態のバッジの状態。
const (
	badgeUnlocked = "unlocked"
	badgeLocked   = "locked"
	// badgeUnknown はロック状態を取得できなかった。
	badgeUnknown = "unknown"
)

// badgeData は lock-badge（ヘッダーのロック状態のバッジ）に渡す。
type badgeData struct {
	State string
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

// lockStatus は GET /lock-status。ヘッダーのバッジだけを返す（htmx で差し替える）。
// ロックの操作（#31）の後は、応答に HX-Trigger: lock-changed を付ければバッジが読み直される。
func (s *handler) lockStatus(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "lock-badge", s.loadBadge(r))
}
