package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/backpaper0/passui/internal/store"
)

// storeFailure はストア バックエンドのエラーを、応答のステータスとブラウザに出す
// メッセージに分類する。分類できないエラー（そのほかの失敗）は op と attrs と一緒に
// error でログに出し、「操作に失敗しました」とエラー ID を返す。エラー文（子プロセスの
// stderr などを含みうる）はブラウザに出さない。attrs にシークレットを入れないこと。
func (s *handler) storeFailure(r *http.Request, err error, op string, attrs ...any) (int, string) {
	switch {
	case errors.Is(err, store.ErrLocked):
		return http.StatusLocked, "ロック中です。ゲストのターミナルで解錠してください"
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound, "エントリが見つかりません"
	case errors.Is(err, store.ErrExists):
		return http.StatusConflict, "同じ名前のエントリが既にあります"
	case errors.Is(err, store.ErrIsDirectory):
		return http.StatusConflict, "同じ名前のフォルダがあります"
	case errors.Is(err, store.ErrConflict):
		return http.StatusConflict, "エントリがほかで変更されています"
	case errors.Is(err, store.ErrInvalidName):
		return http.StatusBadRequest, "パスが正しくありません（空の要素や . で始まる要素は使えません）"
	}
	id := logFailure(r.Context(), s.logger, op, append(attrs, "err", err)...)
	return http.StatusInternalServerError, failureMessage(id)
}

// destinationFailure は、新しいエントリの宛先 dest（作成・生成のパス、移動先）を伴う操作の
// 失敗を storeFailure で分類する。末尾が "/" のパスには、エントリの名前まで入れるよう
// 案内する（store は同名のフォルダがあるときと同じ ErrIsDirectory を返す）。
func (s *handler) destinationFailure(r *http.Request, err error, dest, op string, attrs ...any) (int, string) {
	status, msg := s.storeFailure(r, err, op, attrs...)
	if errors.Is(err, store.ErrIsDirectory) && strings.HasSuffix(dest, "/") {
		msg = "パスの末尾が / です。エントリの名前まで入力してください"
	}
	return status, msg
}

// newErrorID はブラウザとログを結び付けるエラー ID（16 桁の 16 進数）を作る。
func newErrorID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand.Read は失敗しない
	return hex.EncodeToString(b)
}

// failureMessage は失敗したときにブラウザに返す定型メッセージ。
func failureMessage(errorID string) string {
	return fmt.Sprintf("操作に失敗しました（エラー ID: %s）", errorID)
}

// logFailure はエラー ID を作り、msg と attrs を error でログに出して、エラー ID を返す。
// attrs にシークレット（エントリの中身、トークン、フォームの値）を入れてはいけない。
func logFailure(ctx context.Context, logger *slog.Logger, msg string, attrs ...any) string {
	id := newErrorID()
	logger.ErrorContext(ctx, msg, append([]any{"error_id", id}, attrs...)...)
	return id
}

// writeFailure は「操作に失敗しました」とエラー ID だけを 500 で返す。
// ハンドラで想定外の失敗が起きたときに使う。詳細はログにだけ出す。
func writeFailure(w http.ResponseWriter, r *http.Request, logger *slog.Logger, msg string, attrs ...any) {
	id := logFailure(r.Context(), logger, msg, attrs...)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, "<p>%s</p>\n", template.HTMLEscapeString(failureMessage(id)))
}
