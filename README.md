# passui

passui は、ゲスト上の password-store（`pass` のストア）をホストのブラウザから扱う Web ツールです。
ゲストで systemd ユーザーサービスとして動き、`127.0.0.1` だけで待ち受けます。ホストのブラウザから `http://passui.localhost:3560` で開きます。

- 画面は 2 ペインです。左に検索とツリー、右に選んだエントリの詳細を出します。
- MVP の機能: ツリー表示と検索、閲覧（表示・コピー）、追加、編集（移動・リネームを含む）、削除、パスワード生成。
- ストアへの書き込みは `pass` を呼んで行います。passui で書いたエントリは、ターミナルの `pass` と fnox がそのまま読めます。
- passui はパスフレーズを扱いません。解錠はゲストのターミナルで行います（`pass show <エントリ> >/dev/null` など）。gpg-agent のキャッシュはターミナルの `pass` と共有します。
- ログインには、起動ごとに作り直すトークンを使います。`passui login` が出す URL を開くとログインできます。

用語（ホスト / ゲスト / 環境 / ストア / エントリ / 解錠 / ロック / ログイン）は [GLOSSARY.md](GLOSSARY.md) に従います。

## 前提

どちらの環境でも、ゲストに次のものが要ります。

- systemd（ユーザーマネージャが動いていること。`systemctl --user show-environment` が成功すること）
- [mise](https://mise.jdx.dev/)
- `pass`（1.7.4 を想定）、bash、gnupg（`gpg`、`gpg-connect-agent`）
- ストアと GPG 秘密鍵（ゲストのターミナルで `pass` が使える状態）

あると望ましいもの（なくても登録はできますが、`install-service` が警告します）:

- linger: ログインしていなくても passui を起動しておくため。`loginctl enable-linger`
- `gpg-agent.socket` の有効化: passui を再起動したときに、ターミナルの `pass` までロックされないようにするため。`systemctl --user enable gpg-agent.socket`

### 私用: macOS + OrbStack

- ゲストは OrbStack の Linux マシン（Apple シリコンなら arm64）です。systemd は既定で動いています。
- ホストのブラウザから `http://passui.localhost:3560` に届きます（OrbStack がゲストのポートを Mac の `localhost` に転送します）。

### 公用: Windows + WSL2

- ゲストは WSL2 のディストリビューション（amd64）です。
- systemd が動いていなければ、ゲストの `/etc/wsl.conf` に次を書き、Windows 側で `wsl --shutdown` してから開き直します。

  ```ini
  [boot]
  systemd=true
  ```

- ホストのブラウザから `http://passui.localhost:3560` に届きます（WSL2 の localhost 転送）。
- ゲストが止まっている間は passui も止まります（ゲストを常駐させる設定は passui の対象外です）。

## インストール

```sh
mise use -g github:backpaper0/passui
passui version
passui install-service
```

`install-service` は次のことをまとめて行い、最後にログイン URL を表示します。何度実行しても同じ結果になります。

1. 前提を確かめる。systemd のユーザーセッションに繋がらないときや、`pass` が見つからないときは、何も書かずにエラーで終わります。
2. unit を `~/.config/systemd/user/passui.service` に書く（`XDG_CONFIG_HOME` があればその下）。
   - `ExecStart` には mise の shim（`~/.local/share/mise/shims/passui`）を書きます。shim がなければ実行ファイルの実体パスを書いて警告します（その場合は、更新でパスが変わったら `install-service` をやり直してください）。
   - シェルの `GNUPGHOME` と `PASSWORD_STORE_*` を `Environment=` に書き写します。`PATH` は mise の shims とシステムのパスから組み立てます。
3. `systemctl --user daemon-reload`、`enable`、`restart` を行う。

表示された URL をホストのブラウザで開くとログインできます。あとで URL がもう一度要るときは、ゲストのターミナルで次を実行します。

```sh
passui login
```

ログインは passui の再起動かログアウトまで続きます。passui を再起動したら `passui login` でログインし直してください。

## 更新

```sh
mise upgrade passui && passui install-service
```

`install-service` が unit の変更を反映し、passui を再起動します。

## 削除

```sh
passui uninstall-service
mise unuse -g github:backpaper0/passui
```

`mise unuse -g` は mise のグローバル設定から passui を外し、使われなくなったバージョンを消します。

`uninstall-service` はサービスを止めて無効にし、unit を消して `daemon-reload` します。
linger、`gpg-agent.socket`、env ファイル（`~/.config/passui/env`）には触りません。要らなければ自分で消してください。

## 設定

設定は環境変数で行います。サービスでは env ファイル `~/.config/passui/env`（`XDG_CONFIG_HOME` があれば `$XDG_CONFIG_HOME/passui/env`）に `KEY=VALUE` で書きます。
ファイルがなくても既定値で起動します。`install-service` はこのファイルを作りませんし、触りません。

| 変数 | 既定値 | 内容 |
|---|---|---|
| `PASSUI_PORT` | `3560` | 待ち受けるポート。使用中なら別のポートに逃げずにエラーで止まります。 |
| `PASSUI_LOG_LEVEL` | `info` | `info` か `debug`。 |

- 不正な値ならエラーで止まります（原因は journald に出ます）。知らない `PASSUI_*` は警告します。
- 変更は `systemctl --user restart passui` で反映します。
- ストアと gpg は、unit に書き写した `PASSWORD_STORE_DIR`（既定 `~/.password-store`）と `GNUPGHOME`（既定 `~/.gnupg`）で決まります。シェルの値を変えたら `passui install-service` をやり直してください。

例:

```sh
mkdir -p ~/.config/passui
printf 'PASSUI_PORT=3561\nPASSUI_LOG_LEVEL=debug\n' > ~/.config/passui/env
systemctl --user restart passui
```

## ログ

```sh
journalctl --user -u passui      # すべて
journalctl --user -u passui -f   # 追いかける
systemctl --user status passui   # 状態
```

- トークン、Cookie、フォームの値、エントリの中身は、どのログレベルでもログに出ません。
- ブラウザに「操作に失敗しました（エラー ID: …）」と出たら、そのエラー ID でログを探してください。

## 開発

```sh
go vet ./...
go test -race ./...
```

- 統合テストは実際の `pass` と `gpg` を使います。見つからなければスキップします（`PASSUI_REQUIRE_INTEGRATION=1` ならスキップせず失敗にします。CI はこちら）。
- リリース: `v1.2.3` の形のタグを push すると、`.github/workflows/release.yml` が goreleaser で Linux（amd64 / arm64）の tar.gz と `checksums.txt` を GitHub Releases に出します。
- リリース前の手動確認は [docs/manual-checklist.md](docs/manual-checklist.md) にあります。
