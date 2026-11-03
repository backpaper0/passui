// Package secret は、ログイン トークンやセッション ID のように、ログに出してはいけない値を持つ型を提供する。
package secret

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
)

const redacted = "[REDACTED]"

// Value はシークレットの文字列。slog でも fmt でも [REDACTED] と出る。
// 中身が要るところ（Cookie やファイルへの書き出し）でだけ Reveal を呼ぶ。
type Value struct{ v string }

// Of は s をシークレットとして包む。
func Of(s string) Value { return Value{s} }

// New は 256 ビットの乱数から作ったシークレット（16 進数 64 文字）を返す。
func New() Value {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read は失敗しない
	return Value{hex.EncodeToString(b)}
}

// Reveal は中身を返す。
func (s Value) Reveal() string { return s.v }

// IsZero は中身が空かどうかを返す。
func (s Value) IsZero() bool { return s.v == "" }

// Equal は other と定数時間で比べる。空のシークレットは何とも一致しない。
func (s Value) Equal(other string) bool {
	if s.v == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(s.v), []byte(other)) == 1
}

// LogValue は slog で [REDACTED] と出す。
func (s Value) LogValue() slog.Value { return slog.StringValue(redacted) }

// String は fmt で [REDACTED] と出す。
func (s Value) String() string { return redacted }

// GoString は %#v で [REDACTED] と出す。
func (s Value) GoString() string { return redacted }

// Format は、どの動詞でも [REDACTED] と出す。
func (s Value) Format(f fmt.State, verb rune) { _, _ = fmt.Fprint(f, redacted) }
