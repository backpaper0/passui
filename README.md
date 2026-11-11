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

## エクスポート

画面のエクスポート ボタンを押すと、その時点のストアとストアの鍵を 1 つのファイル（エクスポートファイル）にまとめて、ブラウザにダウンロードさせます。確認は挟みません。ロック中でも同じように動きます。成功したときは画面が変わらず、ダウンロードだけが始まります。ゲストには何も残さず、ストアにも gpg-agent にも書き込みません。

- ファイル名: `passui-export-YYYYMMDD-HHMMSS.tar.gz`（ゲストのローカル時刻）。展開すると、同じ名前のディレクトリが 1 つできます。
- 中身:
  - `store/`: ストアを丸ごと写したもの（`.git` や `.gpg-id` を含む）。ソケットなどの特殊なファイルは飛ばします。
  - `gnupg/public-keys.asc`: ストアの鍵の公開鍵。
  - `gnupg/ownertrust.txt`: ストアの鍵の ownertrust（ストアの鍵の行だけ）。
  - `gnupg/private-keys-v1.d/<keygrip>.key`: ストアの鍵のうち、ゲストに秘密部分がある主鍵・副鍵の gpg-agent の鍵ファイル。
  - `README.txt`: 作成日時、passui のバージョン、ストアの鍵の一覧（指紋、秘密鍵の有無、keygrip）、飛ばしたファイル、そのエクスポートファイル向けの手作業で戻す手順。
- 秘密鍵は、元のパスフレーズで保護されたまま入っています。戻すにはそのパスフレーズが要ります。言い換えると、守っているのはパスフレーズだけです。置き場所の共有範囲やアクセス権に注意してください。
- ダウンロードしたファイルをどこに置き、どう保管するか（クラウドストレージに上げるかなど）は、passui の外で決めてください。passui からは、保存できたかどうかもわかりません。
- ストアの鍵の ID が解決できない、gpg が失敗した、などで作れなかったときは、ダウンロードの代わりに結果ページで原因を出します。
- ゲストに秘密鍵のないストアの鍵は、公開鍵だけが入ります。その鍵だけで暗号化したエントリは、このエクスポートファイルから戻しても復号できません。その鍵の秘密鍵は、別に持っている場所から取り込んでください（`README.txt` では、その鍵が「秘密鍵: なし」になります）。

以前のバージョンはバックアップをゲストの `~/.local/share/passui/backup`（または `PASSUI_BACKUP_DIR`）に置いていました。もう使わないので、不要なら消してかまいません。`PASSUI_BACKUP_DIR` は読まなくなったので、env ファイルに残っていれば知らない変数として警告されます。消してください。

### 手作業で戻す

新しいゲストで、次の手順を手で実行します。各エクスポートファイルの `README.txt` にも、そのエクスポートファイルの名前、指紋、keygrip を埋めた同じ手順が書かれています。

<!-- restore-steps:begin -->
手作業で戻す手順

ストアもストアの鍵もない、新しいゲストに戻すことを前提にしています。ゲストに pass と
gnupg を入れ、元のパスフレーズを用意してから、エクスポートファイルがあるディレクトリで
次のとおりに実行してください。

1. ストアがまだないことを確かめます。ストアがあれば、ここで止めてください（上書きしません）。

       test ! -e "${PASSWORD_STORE_DIR:-$HOME/.password-store}"

2. エクスポートファイルを展開し、その中に移ります。

       tar -xzf passui-export-<日時>.tar.gz
       cd passui-export-<日時>

3. ストアの鍵の公開鍵を取り込みます。

       gpg --batch --import gnupg/public-keys.asc

4. 秘密鍵（.key）を gpg-agent の鍵ファイルの場所に写し、gpg-agent に読み直させます。

       dir="$(gpgconf --list-dirs homedir)/private-keys-v1.d"
       install -d -m 700 "$dir"
       install -m 600 gnupg/private-keys-v1.d/<keygrip>.key "$dir"
       gpgconf --kill gpg-agent

5. ストアの鍵の ownertrust を取り込みます。

       gpg --batch --import-ownertrust gnupg/ownertrust.txt

6. ストアを置きます。

       cp -a store "${PASSWORD_STORE_DIR:-$HOME/.password-store}"

7. ストアに書き込まずに確かめます。秘密鍵が一覧に出ること、警告なしに暗号化できること
   （ownertrust が効いていること）、元のパスフレーズでエントリを復号できること、最後の
   コミットがエクスポートした時点のものであること（ストアを git で管理しているときだけ）。
   エントリの印は、ストアにあるエントリの名前に置き換えてください。

       gpg --list-secret-keys <指紋>
       echo test | gpg --batch -e -r <指紋> >/dev/null
       pass show <エントリ>
       git -C "${PASSWORD_STORE_DIR:-$HOME/.password-store}" log -1

8. 後片付けとして、展開したディレクトリを消します（秘密鍵が入っています）。

       cd ..
       rm -rf passui-export-<日時>
<!-- restore-steps:end -->

鍵の数だけ、`install` の行（keygrip ごと）と、`gpg --list-secret-keys`・`gpg -e` の行（秘密鍵のある鍵ごと）が並びます。秘密鍵が 1 つもないエクスポートファイルでは、4 の手順は飛ばします。ストアの鍵が 1 つもないエクスポートファイル（`.gpg-id` のないストア）では、3〜5 の手順と `pass show` も飛ばします。

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

## ライセンス

[MIT License](LICENSE)
