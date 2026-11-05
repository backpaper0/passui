# バックアップの秘密鍵は gpg-agent の鍵ファイルを写して取る

passui は TTY のない systemd ユーザーサービスで、パスフレーズを扱いません。一方、`gpg --export-secret-keys` は解錠中でも gpg-agent がパスフレーズを求めるため、passui からは使えません。そこでバックアップでは、`private-keys-v1.d/<keygrip>.key`（homedir は `gpgconf --list-dirs homedir` で求める）、`gpg --export` の公開鍵、ストアの鍵に絞った ownertrust をそのまま写すことにしました。鍵は元のパスフレーズで保護されたままで、ロック中でも書き出せます。代わりに、復元は手作業になり、gpg 2.1 以降の内部形式に依存します。

## Considered Options

- **OpenPGP 形式＋画面でパスフレーズ入力（`--pinentry-mode loopback`）**: 復元は `gpg --import` で済みます。ただし、passui がパスフレーズを受け取ることになり、「解錠はターミナルで行う」という前提が崩れるため採りませんでした。
- **`--export-options backup,mode1003`（2.4 以降）**: パスフレーズなしで書き出せます。しかし GnuPG 2.4.8 では、取り込み後の復号が `Invalid cipher algorithm` で失敗しました（原因は未確認）。往復を保証できないため採りませんでした。

## Consequences

- `.gpg-id` の ID が解決できない、gpg / gpgconf の終了コードが 0 でない、`.key` が読めない、のどれかが起きたらバックアップ全体を失敗にします。`gpg --export*` は存在しない ID を黙って飛ばすため、件数の照合が要ります。
- 主鍵がオフラインの鍵（`sec#`）は、秘密部分がある keygrip（暗号化サブ鍵など）の `.key` だけを写します。
- 秘密部分があるかは、`gpg --list-secret-keys` の `sec`／`ssb` の第 15 フィールド（`#`）ではなく、`<keygrip>.key` があるかで決めます。gpg-agent は読めない `.key` も「ない」（`ssb#` など）と報告するため、第 15 フィールドに頼ると、読めない `.key` を黙って飛ばしたバックアップが成功してしまいます。`.key` がなければ飛ばし、あっても読めなければ失敗にします。
- ストアの鍵が 1 つもない（`.gpg-id` のない）ストアでも、配置をそろえるため空の `public-keys.asc` と `ownertrust.txt` を入れます。空の `public-keys.asc` は `gpg --import` が失敗するので、復元の手順では鍵を取り込む手順を飛ばします。

調査の詳細: [systemd サービスの passui から GPG 秘密鍵を書き出せるか](https://github.com/backpaper0/passui/issues/44)
