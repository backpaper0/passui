# 常駐プロセスから GPG 復号するときのパスフレーズ入力方式

調査チケット: [#3](https://github.com/backpaper0/passage/issues/3)（マップ: [#2](https://github.com/backpaper0/passage/issues/2)）

## 問い

systemd ユーザーサービスとして常駐する passage から、パスフレーズ付きの GPG 鍵でエントリを復号するには、どんな方式があるか。ターミナル側の `pass`（pinentry-curses）とキャッシュを共有できるか。暗号化にパスフレーズは要るか。Go からはどう扱うか。

## 結論（要約）

- **gpg-agent は 1 ゲストにつき 1 つ（`GNUPGHOME` ごとに 1 ソケット）で、パスフレーズのキャッシュは agent が鍵（keygrip）単位で持つ。** passage とターミナルの `pass` が同じ `GNUPGHOME` を使えば、キャッシュは双方向に共有される（実測）。
- systemd ユーザーサービスからも agent のソケットに到達できる（実測）。Ubuntu の `gpg-agent` パッケージは `gpg-agent.socket` をユーザーユニットとして有効化済みで、agent はソケット起動により passage とは独立したユニットで動く。
- サービスには TTY がないため、`--pinentry-mode` を指定しないと pinentry が起動できず復号に失敗する（実測: `Inappropriate ioctl for device`）。passage は **`--pinentry-mode loopback` か `--pinentry-mode error` を必ず明示する**必要がある。
- `loopback` は GnuPG 2.4 の既定設定で許可されており、`gpg-agent.conf` の変更は不要（`no-allow-loopback-pinentry` を書いた場合のみ拒否される）。loopback で入力したパスフレーズも agent にキャッシュされ、ターミナルの `pass` から再利用される（実測）。
- **暗号化（追加・上書き）にパスフレーズは不要**（公開鍵だけで済む。実測）。
- Go からは **`gpg` コマンドを子プロセスとして呼ぶ方式**が、互換・agent キャッシュ共有・単一バイナリの全条件を満たす。ProtonMail/go-crypto は agent を使わず、GnuPG の秘密鍵ストアも読めないため、復号には不向き。gpgme（cgo）は単一バイナリ要件と相性が悪い。

## 選択肢の比較

| 方式 | 仕組み | agent 設定変更 | ターミナル `pass` への影響 | パスフレーズが passage を通るか | 評価 |
| --- | --- | --- | --- | --- | --- |
| A. loopback | ブラウザで入力 → passage → `gpg --batch --pinentry-mode loopback --passphrase-fd N -d` | 不要（既定で許可） | なし。キャッシュは共有される | 通る（ブラウザ → localhost → passage → gpg の fd） | 有力 |
| B. キャッシュ依存（`--pinentry-mode error`） | passage はパスフレーズを扱わない。未キャッシュなら「ロック中」として UI に返し、ユーザーはターミナルで `pass show` 等を一度実行して解錠する | 不要 | なし | 通らない | 有力（A と併用も可） |
| C. カスタム pinentry | `pinentry-program` を passage 用プログラムに差し替え、ブラウザに入力を中継 | 必要（`gpg-agent.conf` の `pinentry-program`） | **ある**。設定は agent 全体に効くため、ターミナルの pinentry-curses も置き換わる。curses へのフォールバックを自作する必要がある | 通る | 非推奨 |
| D. Assuan 直接（`PRESET_PASSPHRASE`） | agent ソケットに Assuan で接続し、keygrip に対してパスフレーズを事前投入。復号は `gpg --pinentry-mode error` | 必要（`allow-preset-passphrase`） | 投入したパスフレーズは `default-cache-ttl` では失効せず、明示的に消すか agent 再起動まで残る（`max-cache-ttl` は守られる） | 通る | A より設定・挙動が重く利点が薄い |
| D'. Assuan 直接（`PKDECRYPT`） | agent に直接セッション鍵の復号を依頼 | 不要 | なし | 方式次第 | OpenPGP パケット解析を自前実装する必要があり非現実的 |
| E. go-crypto でネイティブ復号 | 秘密鍵を `gpg --export-secret-keys` で書き出して Go 側で `Entity.DecryptPrivateKeys(passphrase)` | — | agent を使わないためキャッシュ共有なし | 通る | 非推奨（秘密鍵の複製が生まれる） |
| F. gpgme（cgo） | `proglottis/gpgme` 等で libgpgme 経由。内部では gpg を起動 | 不要 | なし | 方式次第（`SetPinEntryMode` / `SetCallback`） | cgo と libgpgme への動的リンクで単一バイナリ要件を崩す |

## 詳細

### 1. pinentry モードと loopback

- `gpg --pinentry-mode` の値は `default`（agent 既定 = `ask`）、`ask`、`cancel`、`error`（「No Pinentry」エラーを返す）、`loopback`（pinentry の問い合わせを呼び出し元にリダイレクト）。loopback ではパスフレーズを間違えても再入力は求められない。[GPG Esoteric Options](https://www.gnupg.org/documentation/manuals/gnupg/GPG-Esoteric-Options.html)
- `--passphrase-fd` / `--passphrase-file` / `--passphrase` は 2.0 以降 `--batch` が必要で、2.1 以降は `--pinentry-mode loopback` も必要。`--passphrase` はプロセス一覧に見えるので使わず、`--passphrase-fd`（パイプ）を使う。[同上](https://www.gnupg.org/documentation/manuals/gnupg/GPG-Esoteric-Options.html)
- agent 側の `--allow-loopback-pinentry` / `--no-allow-loopback-pinentry` は「許可が既定」。[Agent Options](https://www.gnupg.org/documentation/manuals/gnupg/Agent-Options.html)
- 実測（GnuPG 2.4.8）: 既定設定で loopback 復号に成功。`no-allow-loopback-pinentry` を設定すると失敗した。

### 2. systemd ユーザーサービスから agent ソケットへの到達

- Ubuntu の `gpg-agent` パッケージは `/usr/lib/systemd/user/gpg-agent.socket`（`ListenStream=%t/gnupg/S.gpg-agent`）ほか `-extra` / `-ssh` / `-browser` のソケットユニットを提供し、既定で enabled。ゲスト（OrbStack Ubuntu）で確認。
- agent は必要に応じて `gpg` 等から自動起動される。[Invoking GPG-AGENT](https://www.gnupg.org/documentation/manuals/gnupg/Invoking-GPG_002dAGENT.html)
- 実測: `systemd-run --user` で起動したプロセス内の `gpgconf --list-dirs agent-socket` は `/run/user/<uid>/gnupg/S.gpg-agent` で、ターミナルと同じ。
- **注意 1: `GNUPGHOME` を既定以外にするとソケットは `/run/user/<uid>/gnupg/d.<ハッシュ>/S.gpg-agent` になる**（実測）。passage はターミナルと同じ `GNUPGHOME` で gpg を呼ぶ必要がある（ユニットファイルで環境変数を揃える）。
- **注意 2: ソケット起動が無効な状態で passage が agent を自動起動すると、agent は passage のユニットの cgroup に入り、passage 停止時に一緒に殺される**（実測。systemd の `KillMode=control-group` 既定「all remaining processes in the control group of this unit will be killed on unit stop」[systemd.kill](https://www.freedesktop.org/software/systemd/man/latest/systemd.kill.html)）。Ubuntu の既定（`gpg-agent.socket` 有効）ならこの問題は起きない。配布手順で `systemctl --user is-enabled gpg-agent.socket` を確認するとよい。
- WSL2 では systemd が有効であることが前提。`wsl --install` で入る現行 Ubuntu は systemd が既定、それ以外は `/etc/wsl.conf` の `[boot] systemd=true` で有効化する。systemd のサービスは WSL インスタンスを生かし続けない点にも注意。[Microsoft Learn: Use systemd with WSL](https://learn.microsoft.com/en-us/windows/wsl/systemd)（WSL2 上での実測は未実施）

### 3. TTY のないサービスからの pinentry

- 実測: サービスから `--pinentry-mode` を付けずに復号すると `Inappropriate ioctl for device` で失敗（pinentry-curses が端末を開けない）。`--pinentry-mode error` では `No pinentry` で即座に失敗する。
- ターミナルの pinentry にプロンプトを横取りさせる設計は成立しない。passage は必ず `loopback` か `error` を明示する。

### 4. キャッシュの共有

- キャッシュは agent が持ち、`--default-cache-ttl`（既定 600 秒、アクセスごとに延長）と `--max-cache-ttl`（既定 7200 秒、延長されない上限）で失効する。[Agent Options](https://www.gnupg.org/documentation/manuals/gnupg/Agent-Options.html)。ローカルでも `gpgconf --list-options gpg-agent` で同じ既定値を確認。
- 実測（同じ `GNUPGHOME`、agent はサービス外で稼働）:
  - サービスから loopback で復号 → ターミナルで `--pinentry-mode error` の復号が成功（キャッシュ利用）。
  - ターミナルで復号 → サービスから `--pinentry-mode error` の復号が成功。
- つまり **passage で解錠するとターミナルの `pass` も解錠され、その逆も成り立つ**。利便性の面では利点、セキュリティの面では「passage でのロック」が独立しないことを意味する。
- キャッシュ状態は Assuan の `KEYINFO <keygrip>` の 7 番目のフィールド（`1` = キャッシュ済み）で確認できる（実測: `gpg-connect-agent "KEYINFO <keygrip>" /bye`）。UI に「ロック中 / 解錠中」を出す材料になる。`CLEAR_PASSPHRASE` で明示的にロックすることもできる（実測、[GET_PASSPHRASE / CLEAR_PASSPHRASE](https://www.gnupg.org/documentation/manuals/gnupg/Agent-GET_005fPASSPHRASE.html)）。ただしロックはターミナル側にも効く。

### 5. 暗号化にパスフレーズは不要

- 公開鍵暗号の暗号化は受信者の公開鍵だけを使う。実測: キャッシュなし・pinentry なしの状態で `gpg --batch -e -r <id>` が成功。
- 署名を付けない限り（`pass` も署名しない）秘密鍵は使わない。追加・上書き・移動（再暗号化しない移動ならファイル操作のみ）にはパスフレーズ不要。ただし**編集**は既存エントリの復号を伴うためパスフレーズ（またはキャッシュ）が必要。
- 自分の鍵が ultimate trust でない受信者を `.gpg-id` に含む場合は trust の扱いが問題になりうる（MVP では複数 `.gpg-id` は対象外）。

### 6. `gpg-preset-passphrase` / `PRESET_PASSPHRASE`

- agent を `--allow-preset-passphrase` で起動している必要がある。投入したパスフレーズは `--forget` するか agent の再起動・SIGHUP まで失効しない（`--max-cache-ttl` は守られる）。キャッシュ ID は 40 桁の keygrip。[gpg-preset-passphrase](https://www.gnupg.org/documentation/manuals/gnupg/gpg_002dpreset_002dpassphrase.html)、[Invoking gpg-preset-passphrase](https://www.gnupg.org/documentation/manuals/gnupg/Invoking-gpg_002dpreset_002dpassphrase.html)
- 実測: `allow-preset-passphrase` 設定後、preset → `--pinentry-mode error` の復号に成功。
- loopback と比べ、agent 設定の変更が必要で TTL の挙動も異なるため、passage が採る利点は薄い。

### 7. Go からの扱い

- **`os/exec` で `gpg` を呼ぶ**: `pass` と同じ実装（gpg）で読み書きするため互換が最も確実。agent・キャッシュをそのまま共有。単一バイナリ要件も満たす（ゲストに gpg は既に必須）。パスフレーズは `--passphrase-fd` にパイプで渡し、引数や環境変数には載せない。`--status-fd` で機械可読な結果（`BAD_PASSPHRASE` 等）を得られる。
- **ProtonMail/go-crypto**: 鍵は `ReadArmoredKeyRing` / `ReadKeyRing` で OpenPGP 形式から読み、秘密鍵の復号は `Entity.DecryptPrivateKeys(passphrase)`。[pkg.go.dev](https://pkg.go.dev/github.com/ProtonMail/go-crypto/openpgp) のドキュメントに gpg-agent や GnuPG の `private-keys-v1.d` を扱う記述はない。GnuPG 2.1 以降の秘密鍵は agent 管理下にあるため、使うには秘密鍵を書き出して複製する必要があり、キャッシュも共有できない。**暗号化だけ**なら公開鍵で可能だが、gpg を呼べば済むため利点は小さい。
- **gpgme（`proglottis/gpgme`）**: libgpgme の Go ラッパー（cgo）。`SetPinEntryMode(PinEntryLoopback)` と `SetCallback` でパスフレーズを渡せる。[pkg.go.dev](https://pkg.go.dev/github.com/proglottis/gpgme)。cgo と libgpgme への依存が単一バイナリ要件・クロスビルドと相性が悪い。
- **Assuan を自前実装**: agent プロトコルはテキストベースで `gpg-connect-agent` から各コマンドの仕様を参照できる（`PKDECRYPT`、`PRESET_PASSPHRASE`、`GET_PASSPHRASE`、`CLEAR_PASSPHRASE` など）。[Agent Protocol](https://www.gnupg.org/documentation/manuals/gnupg/Agent-Protocol.html)。`KEYINFO` / `CLEAR_PASSPHRASE` 程度の補助用途なら `gpg-connect-agent` を呼ぶだけでも足りる。

## 決定チケットへの入力

1. **gpg 呼び出し方式を採る**（`os/exec` + `--batch` + `--pinentry-mode` 明示 + `--passphrase-fd` + `--status-fd`）。go-crypto・gpgme・カスタム pinentry は退ける理由が明確。
2. **残る選択は A（loopback でブラウザから入力）と B（キャッシュ依存、ターミナルで解錠）の二択、または併用**。
   - A: UX はよいが、パスフレーズがブラウザ → localhost → passage を通る。localhost 公開の認証方式（別チケット）と、ログ・メモリへの残留防止が前提になる。
   - B: passage はパスフレーズを一切扱わない。解錠はターミナルで `pass show` 等を一度実行するだけ。既定 TTL（600 秒・延長あり、上限 7200 秒）内でしか使えない。
   - 併用: 既定は B、未解錠時のみ A の入力欄を出す、など。
3. **キャッシュは passage とターミナルで共有される**ことを前提にする（独立したロックは作れない）。UI のロック表示は `KEYINFO`、明示ロックは `CLEAR_PASSPHRASE` で実装可能。
4. 配布・サービス登録では、ターミナルと同じ `GNUPGHOME` を使うことと、`gpg-agent.socket` が有効であることを確認する。
5. 追加・上書き（暗号化）にはパスフレーズ不要、閲覧・編集（復号）には必要、という区別を UI 設計に反映できる。

## 検証環境

- OrbStack 上の Ubuntu、GnuPG 2.4.8、libassuan 3.0.2、pinentry-curses 1.3.2、systemd ユーザーインスタンス。
- 実際の鍵・`~/.gnupg` は触らず、一時 `GNUPGHOME` にパスフレーズ付きの使い捨て鍵を作って検証。サービスからの実行は `systemd-run --user --wait --pipe` で再現。
- WSL2 での実測は未実施。
