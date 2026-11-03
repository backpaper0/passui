// Package store はストア バックエンドである。HTTP シームの裏側で、ストアの読み書きを
// 1 つのインターフェース（Backend）にまとめる。本物の実装は Pass で、テストでは
// passuitest.FakeStore を使う。
package store

import (
	"context"
	"errors"
)

// Backend はストア バックエンドのインターフェース。エントリはストアからの相対パスで
// 指定する（"email/work" のように、拡張子 .gpg を付けず、区切りは "/"）。
//
// 返すエラーは分類してある。errors.Is で ErrLocked、ErrNotFound、ErrExists、
// ErrIsDirectory、ErrConflict のどれかと比べ、どれでもなければ「そのほかの失敗」とする。
// そのほかの失敗のエラー文には内部の情報が入りうるので、ブラウザには出さない。
//
// 以降のチケットで、閲覧、追加、生成、削除、移動、ロック状態の取得、明示ロックを足す。
type Backend interface {
	// Entries はストアのエントリを名前順で返す。ストアのディレクトリを走査し、
	// *.gpg のファイル名だけを使う（復号しない。ロック中でも使える）。
	Entries(ctx context.Context) ([]string, error)
	// Exists はエントリ（name.gpg のファイル）があるかを返す。
	Exists(ctx context.Context, name string) (bool, error)
	// Hash はエントリの暗号文の SHA-256 を 16 進で返す。なければ ErrNotFound。
	Hash(ctx context.Context, name string) (string, error)
}

// 分類したエラー。Backend の実装はこれらを（必要なら %w で包んで）返す。
var (
	// ErrLocked はロック中（gpg-agent に解錠のキャッシュがない）で、復号できなかった。
	ErrLocked = errors.New("store is locked")
	// ErrNotFound はエントリが見つからない。
	ErrNotFound = errors.New("entry not found")
	// ErrExists は宛先にエントリが既にある。
	ErrExists = errors.New("destination already exists")
	// ErrIsDirectory は宛先に同名のディレクトリがある（または末尾が "/"）。
	ErrIsDirectory = errors.New("destination is a directory")
	// ErrConflict は編集中にエントリが外で変えられた（ハッシュが違う、または消えた）。
	ErrConflict = errors.New("entry was changed")
)
