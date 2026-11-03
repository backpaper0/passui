// Package store はストア バックエンドである。HTTP シームの裏側で、ストアの読み書きを
// 1 つのインターフェース（Backend）にまとめる。本物の実装は Pass で、テストでは
// passuitest.FakeStore を使う。
package store

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/backpaper0/passui/internal/secret"
)

// Backend はストア バックエンドのインターフェース。エントリはストアからの相対パスで
// 指定する（"email/work" のように、拡張子 .gpg を付けず、区切りは "/"）。
//
// 返すエラーは分類してある。errors.Is で ErrLocked、ErrNotFound、ErrExists、
// ErrIsDirectory、ErrConflict のどれかと比べ、どれでもなければ「そのほかの失敗」とする。
// そのほかの失敗のエラー文には内部の情報が入りうるので、ブラウザには出さない。
//
// 以降のチケットで、追加、生成、削除、移動、明示ロックを足す。
type Backend interface {
	// Entries はストアのエントリを名前順で返す。ストアのディレクトリを走査し、
	// *.gpg のファイル名だけを使う（復号しない。ロック中でも使える）。
	Entries(ctx context.Context) ([]string, error)
	// Exists はエントリ（name.gpg のファイル）があるかを返す。
	Exists(ctx context.Context, name string) (bool, error)
	// Hash はエントリの暗号文の SHA-256 を 16 進で返す。なければ ErrNotFound。
	Hash(ctx context.Context, name string) (string, error)
	// Show はエントリを復号して平文を返す（pass show）。平文はシークレットなので、
	// 呼び出し側はログやエラーに入れないこと。エントリがなければ ErrNotFound、
	// 復号に失敗してロック中（Locked が true）なら ErrLocked を返す。
	Show(ctx context.Context, name string) ([]byte, error)
	// Locked はロック中か（ストアの鍵の解錠のキャッシュが gpg-agent にないか）を返す。
	// gpg の文言ではなく、gpg-agent の KEYINFO の cached フラグで判定する。
	Locked(ctx context.Context) (bool, error)
	// Lock は gpg-agent から鍵の解錠のキャッシュを消す（明示ロック、CLEAR_PASSPHRASE）。
	// キャッシュは gpg-agent のものなので、ターミナルの pass もロックされる。
	Lock(ctx context.Context) error
	// Create は新しいエントリ name を content で作る（pass insert -m -f）。暗号化だけなので
	// ロック中でも作れる。content の末尾に改行がなければ足す（端末の pass insert -m と揃える）。
	// 名前が正しくなければ ErrInvalidName、末尾が "/" か同名のディレクトリがあれば
	// ErrIsDirectory、同じ名前のエントリがあれば ErrExists を返し、ストアを変えない。
	Create(ctx context.Context, name string, content secret.Value) error
	// Update は既存のエントリ e.Name の本文を書き換える（pass insert -m -f）。暗号化だけなので
	// ロック中でも書ける。今の暗号文のハッシュが e.Hash と違えば ErrConflict、エントリが
	// 消えていれば ErrConflict と ErrNotFound の両方を返し、ストアを変えない（作り直さない）。
	Update(ctx context.Context, e Edit) error
	// Generate は新しいエントリ name をパスワード 1 行だけで作る（pass generate -f）。
	// 宛先の確認は Create と同じ。生成したパスワードは返さない（読むなら Show）。
	Generate(ctx context.Context, name string, opts GenerateOptions) error
	// Regenerate はエントリ name の 1 行目だけを新しいパスワードに替え、2 行目以降を残す
	// （pass generate -i）。復号が要るので、ロック中なら ErrLocked、なければ ErrNotFound。
	Regenerate(ctx context.Context, name string, opts GenerateOptions) error
	// Delete はエントリ name を消す（pass rm -f）。復号しないのでロック中でも消せる。
	// エントリがなければ ErrNotFound を返す。
	Delete(ctx context.Context, name string) error
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
	// ErrInvalidName は新しいエントリの名前（パス）として使えない。
	ErrInvalidName = errors.New("invalid entry name")
)

// CheckNewName は、新しく作るエントリ（作成・生成・移動の宛先）の名前の形を確かめる。
// ストアは見ない（既存のエントリやディレクトリとの衝突は各実装が確かめる）。
//   - 末尾が "/" なら ErrIsDirectory（pass はディレクトリの下に作ってしまうため）。
//   - 空、絶対パス、"a//b" のような正規形でない名前、"." で始まる要素（".." や
//     ".git"、".gpg-id" のように一覧に出ない名前）を含む名前は ErrInvalidName。
func CheckNewName(name string) error {
	if strings.HasSuffix(name, "/") {
		return ErrIsDirectory
	}
	if name == "" || path.Clean(name) != name || strings.HasPrefix(name, "/") {
		return ErrInvalidName
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || strings.ContainsRune(part, 0) {
			return ErrInvalidName
		}
	}
	return nil
}

// WithTrailingNewline は本文の末尾に改行がなければ足す。Create に渡した本文が
// ストアにどう書かれるかを表す（フェイクの実装も本物と揃えるために使う）。
func WithTrailingNewline(content string) string {
	if content == "" || strings.HasSuffix(content, "\n") {
		return content
	}
	return content + "\n"
}
