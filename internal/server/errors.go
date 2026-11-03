package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
)

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
