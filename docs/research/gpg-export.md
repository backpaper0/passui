# 調査: systemd サービスの passui から GPG 秘密鍵を書き出せるか

- チケット: [#44](https://github.com/backpaper0/passui/issues/44)（地図 [#42](https://github.com/backpaper0/passui/issues/42)）
- 調査日: 2026-11-04
- 実験環境: Ubuntu 26.04.1 LTS、GnuPG 2.4.8、libgcrypt 1.12.0、pass 1.7.4。使い捨ての `GNUPGHOME` で、
  `setsid -w … </dev/null` により制御端末なしで実行（systemd ユーザーサービスと同じく TTY なし）。
  `GPG_TTY`・`DISPLAY` は外した。実鍵と `~/.gnupg`・`~/.password-store` には触れていない。
- 用語は [GLOSSARY.md](../../GLOSSARY.md) に従う（解錠 / ロック / ストア / バックアップ）。

## 結論

1. **解錠中でも、`gpg --export-secret-keys`（OpenPGP 形式）はパスフレーズを求める。** gpg-agent のキャッシュは使われない。
   したがって、TTY のない passui からは、パスフレーズを渡さない限り**解錠・ロックに関係なく失敗する**。
   - 理由（ソース）: gpg-agent の `EXPORT_KEY` は、OpenPGP 形式（`--openpgp`）では `agent_key_from_file(…, CACHE_MODE_IGNORE, …)`
     で鍵を読む。キャッシュを無視してパスフレーズを問い合わせ、そのパスフレーズで OpenPGP の S2K 保護をかけ直すため。
     [agent/command.c `cmd_export_key`（STABLE-BRANCH-2-4）](https://github.com/gpg/gnupg/blob/STABLE-BRANCH-2-4/agent/command.c)
   - マニュアルにも「GnuPG may ask you to enter the passphrase for the key. This is required, because the internal
     protection method of the secret key is different from the one specified by the OpenPGP protocol.」とある
     （gpg マニュアル `--export-secret-keys`、`/usr/share/info/gnupg.info`）。
   - 実験: 解錠中（暗号化サブ鍵の keygrip が `KEYINFO` で cached=1）でも、`--pinentry-mode error` では `No pinentry` で失敗した。
2. **パスフレーズを求めずに書き出せる経路は 2 つある。** どちらも鍵は**元のパスフレーズで保護されたまま**になる。
   - (a) `gpg --export-options backup,mode1003 --export-secret-keys`（GnuPG 2.4.0 以降）。ロック中・`--pinentry-mode error`
     でも成功した。ただし**この環境では、書き出したものを取り込んだ後の復号・署名が `Invalid cipher algorithm` で失敗した**
     （RSA・ECC とも、`--import-options restore` の有無によらず）。原因は確かめられていない。
   - (b) gpg-agent の鍵ファイル `private-keys-v1.d/<keygrip>.key` をそのまま写す＋公開鍵を `gpg --export`＋ownertrust。
     取り込み先で元のパスフレーズで復号できた（gpg-agent 側の形式で、パスフレーズ保護つき）。
3. **pinentry を出せない状況での OpenPGP 形式の書き出しは、終了コード 2 と status 行で検出できる。** 出力は 0 バイトになる。
4. **ownertrust はバックアップに必須。** これがないと、復元先で `pass insert`（`gpg -e -r …`、trust-model 指定なし）が
   `Unusable public key` で失敗する。
5. **鍵 ID の集め方**: ストアの中のすべての `.gpg-id` を読み、`gpg --with-colons --list-secret-keys -- <ID…>` で主鍵の
   指紋に解決してから書き出す。`gpg --export-secret-keys` は、見つからない ID が混じっていても**終了コード 0** で
   見つかった分だけを書き出すので、件数の照合が要る。

## 1. 解錠中・ロック中の `--export-secret-keys`（OpenPGP 形式）

鍵は `gpg --quick-gen-key … future-default`（ed25519 主鍵 + cv25519 暗号化サブ鍵）。

| 状態 | オプション | 結果 |
|---|---|---|
| ロック中 | `--batch`（pinentry-mode 既定） | 終了 2、0 バイト。`error receiving key from agent: Inappropriate ioctl for device - skipped`（pinentry-curses が TTY を開けない） |
| ロック中 | `--batch --pinentry-mode error` | 終了 2、0 バイト。`… No pinentry - skipped` |
| 解錠中（サブ鍵 cached=1） | `--batch --pinentry-mode error` | **同じく失敗**（`No pinentry`） |
| 解錠中 | `--batch`（既定） | 失敗（`Inappropriate ioctl for device`） |
| どちらでも | `--batch --pinentry-mode loopback --passphrase-fd 0` に正しいパスフレーズ | 成功（終了 0）。主鍵とサブ鍵のどちらにも 1 回の入力で足りた |
| どちらでも | 同上で誤ったパスフレーズ | 終了 2、0 バイト。`Bad passphrase - skipped` |

`--status-fd` の出力（失敗時）:

```
[GNUPG:] KEY_CONSIDERED <主鍵の指紋> 0
gpg: key <keygrip>: error receiving key from agent: No pinentry - skipped
[GNUPG:] ERROR export_keys.secret 67108949
[GNUPG:] EXPORT_RES 1 1 0
[GNUPG:] FAILURE gpg-exit 33554433
```

- `67108949` = エラー源 GPG_ERR_SOURCE_GPGAGENT(4) << 24 | GPG_ERR_NO_PIN_ENTRY(85)。誤ったパスフレーズでは `67108875`（GPG_ERR_BAD_PASSPHRASE = 11）。
- `EXPORT_RES <count> <secret_count> <exported>` の 3 つ目が書き出せた数。失敗時は 0。
- 検出は「終了コード ≠ 0」で足りる。理由を区別するなら `ERROR export_keys.secret` のコードを見る。
  `--pinentry-mode error` を付けると、既定（ENOTTY）より理由が安定する。
- `--export-secret-subkeys` もサブ鍵の秘密部分で同じく失敗する（主鍵はスタブなので書き出され、終了 2 で 398 バイトの不完全な出力が残る）。
  **出力ファイルの有無ではなく終了コードで判定すること。**
- 未検証: gpg-agent にターミナルの startup tty（`gpg-connect-agent updatestartuptty /bye`）が登録されていると、
  passui からの要求で**ゲストのターミナルに** pinentry-curses が出る可能性がある。`--pinentry-mode error` を付ければこの経路は起きない。

OpenPGP 形式の書き出しは、**パスフレーズを passui が受け取る（loopback）**ことでしか TTY なしでは通らない。
これは README の「passui はパスフレーズを扱いません」と衝突する。

## 2. 書き出した鍵の保護

- OpenPGP 形式（loopback で書き出したもの）: `gpg --list-packets` で主鍵・サブ鍵とも
  `iter+salt S2K, algo: 7 (AES-128), SHA1 protection, … [v4 protected]`。gpg-agent は入力されたパスフレーズで
  保護をかけ直すので、**元のパスフレーズで保護されたまま**。取り込み（`gpg --batch --pinentry-mode error --import`）は
  パスフレーズなしで成功し、取り込み先で `--pinentry-mode error` の復号は `No pinentry` で失敗、元のパスフレーズで復号できた。
- mode1003（`--export-options mode1003`）: パケットは `gnu-mode1003` / `skey: [s-expression …]`。gpg-agent が保存している
  保護つきの S 式をそのまま入れる。マニュアル: 「This format avoids the re-encryption as required with the current OpenPGP
  format and also improves the security of the secret key if it has been protected with a passphrase. Note that an
  unprotected key is exported as-is and thus not secure … Versions of GnuPG before 2.4.0 are not able to import such a
  secret file.」（gpg マニュアル `--export-options mode1003`）
- 鍵ファイル（`private-keys-v1.d/<keygrip>.key`）: 中身は `(protected-private-key (ecc …) (protected openpgp-s2k3-ocb-aes …))`。
  パスフレーズ由来の鍵で保護された gpg-agent の形式。
- どの方法でも、**元の鍵にパスフレーズがなければ平文のまま書き出される**（mode1003 のマニュアルの注意と同じ）。

## 3. パスフレーズなしで書き出す 2 つの経路

### (a) `--export-options backup,mode1003`

- ソース: gpg の `export.c` で `"mode1003"` は `EXPORT_MODE1003`。`receive_seckey_from_agent` のコメント:
  「If MODE1003 is set, the key is requested in raw GnuPG format from the agent. This usually does not require a
  passphrase unless the gpg-agent has not yet used the key and needs to convert it to its internal format first.」
  ([g10/export.c](https://github.com/gpg/gnupg/blob/STABLE-BRANCH-2-4/g10/export.c))。gpg-agent 側は `EXPORT_KEY --mode1003`
  で `agent_raw_key_from_file` を呼ぶ（パスフレーズを問わない）。
- `backup`（2.1.18 で追加。NEWS: 「New --export-option flag "backup", new --import-option flag "restore"」）単独では
  OpenPGP 形式のままなので、**パスフレーズが要る**（実験で `No pinentry`）。パスフレーズを避けるのは `mode1003` のほう。
- 実験: ロック中・`--pinentry-mode error`・TTY なしで終了 0。複数 ID（メールアドレス表記と指紋の混在）でも成功。
- 注意（ソースのコメント）: 他から取り込んだばかりでまだ gpg-agent が使っていない鍵（`openpgp-native` のまま）は、
  変換のためにパスフレーズが要ることがある。未検証。
- **問題**: 2.4.8 で mode1003 を書き出し、別の `GNUPGHOME` に取り込む（`--import` / `--import-options restore`、loopback でパスフレーズを渡しても同じ）と、
  取り込まれた鍵ファイルは `(protected openpgp-native …)` になり、復号・署名が `Invalid cipher algorithm` で失敗した
  （rsa3072・ed25519/cv25519 の両方）。元の `GNUPGHOME` では同じパスフレーズで復号できる。上流の既知の不具合かは確認できなかった。
  **復元できない形式をバックアップに選ぶわけにはいかないので、この形式を採るなら実機の版での往復の確認が前提になる。**
- 取り込む側に GnuPG 2.4.0 以上が要る。

### (b) 鍵ファイルを写す

- 中身: ストアの鍵の主鍵・サブ鍵の keygrip ごとの `$GNUPGHOME/private-keys-v1.d/<keygrip>.key`、
  `gpg --armor --export <指紋…>`（公開鍵）、`gpg --export-ownertrust`。
- keygrip は `gpg --batch --with-colons --with-keygrip --list-secret-keys -- <ID…>` の `grp` 行（`sec`/`ssb` の直後）。
  passui の `keygrips()`（[internal/store/lock.go](../../internal/store/lock.go)）は暗号化用（`e`）だけに絞っているので、
  バックアップでは主鍵（署名・証明用）も含めるよう分ける必要がある。
- 実験の復元: 新しい `GNUPGHOME` で `gpg --import pub.asc` → `private-keys-v1.d/` に `.key` を置く（0700/0600）→
  `gpg --import-ownertrust`。`gpg -K` に `sec`/`ssb` が出て、元のパスフレーズで復号できた。gpg-agent の再起動は要らなかった
  （鍵ファイルは使うときに読まれる）。
- 鍵ファイルの形式（extended key format）は GnuPG 2.1.12 以降が読む。ゲストの Ubuntu の gnupg（2.4 系）なら問題ない。
- 書き出しに gpg-agent を通さないので、ロック中でも動き、pinentry を一切呼ばない。
- 未検証: スマートカードの鍵（`shadowed-private-key` のスタブ）は秘密鍵の実体を含まない。passui の想定（ゲストに秘密鍵がある）では対象外。

## 4. サブ鍵

- `gpg --export-secret-keys <主鍵の指紋>` は主鍵と全サブ鍵の秘密部分を書き出す（OpenPGP・mode1003 とも、パケットに `secret key` と `secret sub key` が出る）。
- `.gpg-id` にサブ鍵の ID が書かれていても、`!` を付けなければ `gpg` はその鍵ブロック全体を対象にする。
  `<サブ鍵指紋>!` では主鍵とそのサブ鍵だけになる（実験で主鍵・サブ鍵の 2 パケット）。バックアップは主鍵の指紋に解決してから書き出すのが安全。
- 秘密鍵は keygrip ごとに gpg-agent にキャッシュされる。暗号化サブ鍵で復号（`pass show`）しても主鍵はキャッシュされない
  （実験: 解錠後の `KEYINFO` で主鍵は `-`、サブ鍵は `1`）。ただし (1) のとおり OpenPGP 形式の書き出しはキャッシュを使わないので、この違いは書き出しの可否には効かない。

## 5. ownertrust

- `gpg --export-ownertrust` の出力（実験）:

  ```
  # List of assigned trustvalues, created Wed Nov  4 21:17:49 2026 JST
  # (Use "gpg --import-ownertrust" to restore them)
  34D49C71D8B803B522157A8346A4D43AAA5F3C39:6:
  ```

  `<指紋>:<値>:` の行。6 は ultimate。鍵ごとに絞るオプションはないので、**全鍵の分が出る**（ストアに関係ない鍵の指紋も含まれる）。
  ストアの鍵の行だけに絞るなら、指紋で行を選ぶ（形式はテキストで単純。行の選別は passui 側で可能）。
- `gpg --import-ownertrust <file>` で取り込める（既存の値は上書き）。マニュアル: 「existing values will be overwritten」。
- 秘密鍵を取り込んでも ownertrust は付かない。ないと `gpg -e -r <ID>` が `There is no assurance this key belongs to the named user` →
  `Unusable public key` で失敗した。pass 1.7.4 は trust-model を指定しない（`GPG_OPTS` に `--trust-model` がない）ので、
  **ownertrust なしでは復元先で `pass insert` などの書き込みが失敗する**。

## 6. ストアの鍵 ID の集め方と pass 1.7.4 の解決規則

pass 1.7.4（`/usr/bin/pass`）の `set_gpg_recipients`:

```bash
if [[ -n $PASSWORD_STORE_KEY ]]; then … return; fi      # 環境変数が .gpg-id より優先
local current="$PREFIX/$1"
while [[ $current != "$PREFIX" && ! -f $current/.gpg-id ]]; do
	current="${current%/*}"                             # 近い祖先の .gpg-id を 1 つだけ使う
done
…
while read -r gpg_id; do
	gpg_id="${gpg_id%%#*}" # strip comment
	[[ -n $gpg_id ]] || continue
	GPG_RECIPIENT_ARGS+=( "-r" "$gpg_id" )
done < "$current"
```

- エントリのディレクトリから上にたどり、最初に見つかった `.gpg-id` だけを使う（祖先の ID とは合算しない）。
  ルートまでなければエラー。
- 1 行 1 ID、複数行なら複数の受信者。空行は飛ばす。`#` 以降はコメント。
- ID はそのまま `gpg -r` に渡る。メールアドレス、指紋、長短の鍵 ID、`gpg.conf` の `group` 名のどれでもよい（解決は gpg が行う）。
- `PASSWORD_STORE_KEY` があれば `.gpg-id` は読まれない（スペース区切り）。passui の unit は `PASSWORD_STORE_*` を書き写すので、
  これが設定されている環境では、その鍵も対象に含めるべき。
- 実験で確かめた細部:
  - `read -r` が行頭・行末の空白を落とすので、`  k@example.invalid  ` は通る。
  - **行内コメント `j@example.invalid # comment` は、`#` の前の空白が残って `"j@example.invalid "` になり、
    `gpg: j@example.invalid : skipped: No public key` で暗号化が失敗する。** passui の `readGPGID` は `TrimSpace` するので通してしまう（pass より寛容）。
  - **末尾に改行のない最後の行は、`while read` が読まないので pass は無視する。** passui の `readGPGID` は含める。
  - `pass init` は `printf "%s\n" "$@"` で書くので、普通に作ったストアでは上の 2 つは起きない。
- バックアップの対象を決める手順（提案）:
  1. ストアのルートとすべてのサブディレクトリの `.gpg-id`（passui の `storeRecipients` と同じ。`.git`・`.extensions` は見ない）と、
     あれば `PASSWORD_STORE_KEY` の ID を集め、重複を除く。
  2. `gpg --batch --with-colons --list-secret-keys -- <ID…>` で `sec` 直後の `fpr` を主鍵の指紋として集める
     （メールアドレスは複数の鍵に当たりうる。当たった鍵はすべて含める）。
  3. 秘密鍵が見つからない ID（公開鍵だけの受信者＝他人の鍵）は、書き出せないので飛ばすか警告する。
     `gpg --export-secret-keys` は存在しない ID が混じっても**終了コード 0**（`EXPORT_RES 1 1 1`）で黙って飛ばすので、
     ID の解決は書き出しの前に自前で行い、結果の数を照合する。
  4. `group` 名は `--list-secret-keys` では展開されない（未検証）。使っていなければ問題ない。

## 判断への影響（地図 #42 向け）

- **ロック中の扱い**: 解錠・ロックは書き出しの可否を決めない。OpenPGP 形式はいつでもパスフレーズが要り、
  mode1003・鍵ファイルはいつでも要らない。画面で「解錠してから」と案内しても OpenPGP 形式の書き出しは通らない。
- **形式の候補**:
  - OpenPGP 形式（`--export-secret-keys --armor`）: 標準的で復元が `gpg --import` だけ。ただし passui がパスフレーズを受け取って
    loopback で渡す必要があり、「passui はパスフレーズを扱わない」方針の変更になる。
  - mode1003: パスフレーズ不要で `gpg --import` で戻せるはずだが、2.4.8 で往復が壊れた。採るなら要検証。
  - 鍵ファイルの写し: パスフレーズ不要、ロック中も可、この環境で往復を確認済み。復元は手作業（公開鍵の取り込み、
    `private-keys-v1.d/` への配置、ownertrust）。`~/.gnupg` 丸ごとではなく、ストアの鍵の分だけに絞れる。
- **ownertrust は必ず含める**（ないと復元後に `pass insert` が失敗する）。
- **失敗の検出**は gpg の終了コード（≠ 0）で行い、出力の有無では判定しない。

## 参考（一次情報）

- GnuPG マニュアル（2.4.8 同梱 `/usr/share/info/gnupg.info`）: `--export-secret-keys`、`--export-ownertrust`、`--import-ownertrust`、
  `--export-options backup / mode1003`、`--import-options restore`、`--pinentry-mode`、`--passphrase-fd`
- GnuPG NEWS（`/usr/share/doc/gnupg/NEWS.gz`）: 2.1.18「New --export-option flag "backup", new --import-option flag "restore"」
- [gnupg agent/command.c（STABLE-BRANCH-2-4）](https://github.com/gpg/gnupg/blob/STABLE-BRANCH-2-4/agent/command.c) `cmd_export_key`
- [gnupg g10/export.c（STABLE-BRANCH-2-4）](https://github.com/gpg/gnupg/blob/STABLE-BRANCH-2-4/g10/export.c) `parse_export_options`、`receive_seckey_from_agent`
- pass 1.7.4 `/usr/bin/pass`（`set_gpg_recipients`、`cmd_init`、`GPG_OPTS`）
- passui: [internal/store/lock.go](../../internal/store/lock.go)（`readGPGID`、`storeRecipients`、`keygrips`）
