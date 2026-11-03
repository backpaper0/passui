# 既存の pass 互換 GUI / Web ツールの調査

調査日: 2026-11-03 / 対応 issue: [#6](https://github.com/backpaper0/passage/issues/6)（マップ [#2](https://github.com/backpaper0/passage/issues/2)）

## 問い

password-store 形式を読み書きできる既存の GUI / Web ツールのうち、

- 「ゲストで常駐し、ホストのブラウザから使う」という要件をそのまま満たせるものはあるか
- 流用・参考にできるものはあるか

特にパスフレーズ入力と認証の扱いに注目し、自作が不要になる可能性も含めて判断材料を出す。

## 結論

- **要件をそのまま満たす既存ツールは見つからなかった。** 見つかった Web フロントエンド 3 つはいずれも**読み取り専用**で、追加・編集・削除・移動・パスワード生成を持たない。保守もほぼ止まっている。
- デスクトップ GUI（QtPass など）は機能を満たすが、ゲストで動かしてホストに画面を出す必要があり、「ホストのブラウザから使う」という前提に合わない。
- **自作は必要。** ただし Go で実装するなら、gopass の Go ライブラリ（`github.com/gopasspw/gopass/pkg/gopass/api`）がストア操作の部品として流用候補になる（後述の注意点あり）。
- パスフレーズの扱いは既存ツールで 3 方式に分かれた。passage の前提（pinentry と gpg-agent の短時間キャッシュ）に最も近いのは「`gpg` / `pass` を呼び出し、パスフレーズは gpg-agent と pinentry に任せる」方式（QtPass、gopass、gopass-ui）。ただし、どれもデスクトップ上のグラフィカルな pinentry を前提にしている。

## 調査対象の出どころ

- passwordstore.org の「Compatible Clients」一覧 <https://www.passwordstore.org/>
- awesome-password-store の「Interfaces」一覧 <https://github.com/tijn/awesome-password-store>（Web フロントエンドは載っていない）
- gopass の機能ドキュメント <https://github.com/gopasspw/gopass/blob/master/docs/features.md>
- GitHub 検索（"web interface for pass" など）で見つけたリポジトリ

保守状況は GitHub API の `pushed_at` と `archived`（2026-11-03 時点）で判定した。

## Web フロントエンド

| ツール | 言語 | ライセンス | 最終 push / 状態 | 書き込み | パスフレーズ | 認証 |
|---|---|---|---|---|---|---|
| [BenoitZugmeyer/pass-web](https://github.com/BenoitZugmeyer/pass-web) | Node.js | MIT | 2022-12 / **archived** | なし（読み取り専用） | 未調査 | 未調査 |
| [emersion/webpass](https://github.com/emersion/webpass) | Go + JS | MIT | 2022-05 / 停止 | なし（読み取り専用） | **ブラウザ側で復号**（秘密鍵をエクスポートしてサーバに置く） | none / PAM / git |
| [rrbarrero/pass-web](https://github.com/rrbarrero/pass-web) | Python(FastAPI) + React | GPL-3.0（README 記載） | 2025-05 / 開発中 | なし（検索と復号のみ） | **ブラウザで入力し、サーバへ送信** | JWT ログイン |

### BenoitZugmeyer/pass-web

- README: "This is currently a read-only interface, and there is no plan to support all the features of pass."
- HTTPS の利用を強く推奨。リポジトリはアーカイブ済み。
- 要件（追加・編集・削除・移動・生成）を満たさず、保守もされていない。

### emersion/webpass

- README: "When you click on an item, your PGP key password will be prompted and the password will be displayed." / "The PGP key password won't be sent to the server, since the passwords are decrypted client-side."
- `gpg --export-secret-keys > private-key.gpg` で秘密鍵を書き出してサーバに置くか、ブラウザから毎回渡す。復号は OpenPGP.js でブラウザ内で行う。
- API は `GET /pass/store`、`GET /pass/store/*.gpg`、`GET /pass/keys.gpg` だけで、書き込みはない（`webpass.go`）。
- 認証は `none`（リバースプロキシで HTTP 認証）、`pam`、`git`（リモート git に SSH で入れたらログイン成功）。
- systemd ユニットの例がある（`scripts/systemd/webpass.service`）。
- **passage の前提と合わない点**: 秘密鍵がゲストの GnuPG の外に出る、gpg-agent と pinentry のキャッシュを使わない、ストアの中身（暗号化済み）と秘密鍵をまとめてブラウザに渡す。

### rrbarrero/pass-web

- README: "the GPG master password is never saved or cached at any time or location by WebPass. It is used only temporarily during the decryption process."
- バックエンドは python-gnupg でパスフレーズを渡して復号（`src/repos/gpg_decryptor.py`）。API はログイン、検索、復号の 3 つ（`src/web/entrypoints.py`）。
- docker-compose でストアと GPG 鍵を**読み取り専用でマウント**する構成。
- README はパスフレーズが HTTP 上を平文で流れる危険を自ら警告している。
- **passage の前提と合わない点**: 書き込みがない、Docker 前提、パスフレーズをブラウザ経由で送る方式で pinentry を使わない。

## デスクトップ GUI / その他のクライアント

| ツール | 種別 | ライセンス | 最終 push / 状態 | メモ |
|---|---|---|---|---|
| [QtPass](https://github.com/IJHack/QtPass) | Qt デスクトップ GUI | GPL-3.0 | 2026-11 / 活発 | `pass` または `gpg2` と `git` を直接使う。パスワード生成あり。"Your GPG must be configured with a graphical pinentry when applicable." Web / リモートモードはない |
| [gopass](https://github.com/gopasspw/gopass) | CLI（Go） | MIT | 2026-11 / 活発 | "a drop-in replacement for `pass`"。Web UI や HTTP サーバモードはない。外部連携は native messaging の JSON API（gopass-jsonapi / gopassbridge） |
| [gopass-ui](https://github.com/codecentric/gopass-ui) | Electron GUI | MIT | 2023-03 / 停止気味 | gopass CLI をラップ。dialog 型 pinentry を推奨。"are not able to offer support for Linux and Windows releases" |
| [cortex/gopass](https://github.com/cortex/gopass) | GUI | GPL-3.0 | 2017 / **archived** | passwordstore.org の一覧に載る同名の別物。gopasspw/gopass とは無関係 |
| [Pass4Win](https://github.com/mbos/Pass4Win) | Windows GUI | GPL-3.0 | **archived** | |
| [ripasso](https://github.com/cortex/ripasso) | Rust（TUI / GUI） | GPL-3.0 | 2026-09 / 活発 | |
| [prs](https://github.com/timvisee/prs) | Rust CLI | GPL-3.0 | 2026-09 / 活発 | |
| [Identities](https://github.com/k8ieone/identities) | GNOME アプリ | GPL-3.0 | 2026-07 | |
| [browserpass](https://github.com/browserpass/browserpass-extension) / [passff](https://github.com/passff/passff) / [gopassbridge](https://github.com/gopasspw/gopassbridge) | ブラウザ拡張 | ISC / GPL-2.0 / MIT | 活発 | native messaging で**同じマシン上の**ホストアプリを呼ぶ。主目的はログインフォームへの入力で、ストア管理（編集・移動など）ではない |

### デスクトップ GUI を passage の代わりに使えるか

- ストアと GPG 秘密鍵はゲストにあるので、GUI はゲストで動かしてホストに画面を出す必要がある。WSL2 は WSLg で GUI を表示できるが、OrbStack の Linux Machine で GUI アプリを表示する公式な手段は今回の調査では確認できなかった。
- そもそも「操作 UI はホストのブラウザ」という前提（#2 で決定済み）に合わない。
- ブラウザ拡張は native messaging でホストアプリを起動する仕組みのため、ホストの拡張からゲストのストアを使うには橋渡しが要る。また機能がストア管理向けではない。

## パスフレーズと認証の扱いの比較

| 方式 | 採用ツール | 秘密鍵の場所 | passage の前提との相性 |
|---|---|---|---|
| A. `gpg` / `pass` を呼び出し、パスフレーズは gpg-agent + pinentry に任せる | QtPass、gopass、gopass-ui | ゲストの GnuPG のまま | **最も近い**。ただし各ツールはデスクトップ上のグラフィカル pinentry を前提にしている。systemd ユーザーサービスから呼ぶ場合、pinentry がどの端末・画面に出るかは別途検討が要る |
| B. ブラウザでパスフレーズを入力し、サーバで復号（loopback 相当） | rrbarrero/pass-web | ゲストの GnuPG のまま | pinentry を使わない。パスフレーズが HTTP を通る。gpg-agent のキャッシュとは別の扱いになる |
| C. 暗号化済みデータと秘密鍵をブラウザへ渡し、ブラウザで復号 | emersion/webpass | **ブラウザに渡る** | 秘密鍵がゲストの外に出るので不適 |

認証は、webpass が PAM / git / なし（リバースプロキシ任せ）、rrbarrero/pass-web が JWT ログイン。いずれも「ネットワーク越しに公開するので HTTPS 必須」という書き方で、localhost 限定の想定ではない。passage の「localhost 公開でも認証必須」という方針の具体策は、既存ツールからはそのまま借りられない。

## 流用・参考にできるもの

1. **gopass の Go ライブラリ `pkg/gopass/api`**（MIT）
   - `gopass.Store` インターフェースに `List`、`Get`、`Set`、`Remove`、`RemoveAll`、`Rename` があり、MVP のストア操作をほぼ覆う（`pkg/gopass/store.go`）。
   - GPG バックエンドは `gpgcli`（`gpg` コマンドを呼ぶ）で、方式 A に当たる（`internal/backend/crypto/gpgcli.go`）。
   - 注意点:
     - `api.New` は gopass の設定（`gopass setup` 済みであること）を前提にし、未初期化だと `ErrNotInitialized` を返す。
     - パッケージの安定性は "best-effort stable"。
     - gopass はシークレットの本文を独自に解釈する（key: value のメタデータなど）。書き戻しで `pass` から見た内容が変わらないか、ストレージに git を要求しないかは検証が要る。
     - gopass の `internal` パッケージを大量に取り込むので、単一バイナリの大きさと依存は増える。
   - 採否は実装スタックを決めるチケットで判断する材料とする。
2. **QtPass**: `pass` を使わずに `gpg2` を直接呼んでも互換を保てる、という実例。パスワード生成の設定項目（長さ、文字種）も参考になる。
3. **emersion/webpass**: Go 製で systemd ユニット例がある最小構成の Web フロントエンド。API 設計とユニットファイルの参考程度。
4. **rrbarrero/pass-web**: 検索とツリー表示の UI、パスフレーズを平文で流す危険への注意書きが参考になる。

## その他の気づき

- **名前の衝突**: [FiloSottile/passage](https://github.com/FiloSottile/passage) は、age を暗号化に使う password-store のフォーク（スター 1,200 超）。gopass のドキュメントも "passage" の名前でこれに言及している。本プロジェクトの名前（コマンド名、mise / apt のパッケージ名）を決めるときに衝突を考慮したほうがよい。

## 情報源

- passwordstore.org Compatible Clients: <https://www.passwordstore.org/>
- awesome-password-store: <https://github.com/tijn/awesome-password-store>
- BenoitZugmeyer/pass-web README: <https://github.com/BenoitZugmeyer/pass-web>
- emersion/webpass README と `webpass.go`、`pass/pass.go`: <https://github.com/emersion/webpass>
- rrbarrero/pass-web README と `src/web/entrypoints.py`、`src/repos/gpg_decryptor.py`: <https://github.com/rrbarrero/pass-web>
- QtPass README: <https://github.com/IJHack/QtPass>
- gopass features: <https://github.com/gopasspw/gopass/blob/master/docs/features.md>
- gopass `pkg/gopass/store.go`、`pkg/gopass/api/api.go`、`internal/backend/crypto/`: <https://github.com/gopasspw/gopass>
- gopass-ui README: <https://github.com/codecentric/gopass-ui>
- 各リポジトリのライセンス・最終 push・archived は GitHub REST API（`GET /repos/{owner}/{repo}`）で取得
