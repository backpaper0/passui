# mise で単一バイナリと systemd unit を配布する方法の調査

- 対応チケット: [#5](https://github.com/backpaper0/passage/issues/5)（マップ [#2](https://github.com/backpaper0/passage/issues/2)）
- 調査日: 2026-11-03
- 用語は `GLOSSARY.md` に従う（ホスト / ゲスト / 環境 / ストア / エントリ / 互換）。

## 結論（要約）

1. **mise の `github` バックエンドで配布する**のが最も手間が少ない。goreleaser の既定のアーカイブ名（`passage_<version>_linux_<arch>.tar.gz`）と `checksums.txt` をそのまま mise が自動検出・検証できる。ゲストでは `mise use -g github:backpaper0/passage` だけで済む。
2. **mise registry への登録は不要かつ不可能に近い**。registry は「数千スター級」の広く使われるツールに限られ、短縮名がなくても `github:owner/repo` で使える。
3. `ubi` は非推奨（mise 2027.1.0 で削除予定）、`aqua` は aqua-registry へのレシピ登録が要る、`go` はゲストに Go ツールチェーンが要りソースからビルドする。いずれも `github` より不利。
4. **systemd ユーザー unit はバイナリ自身の `install-service` サブコマンドで配置する**のがよい。mise の `postinstall` はツール側から配布できず、利用者が自分の mise 設定に書く必要があるため、配布手段としては使えない（補助としては使える）。
5. unit の `ExecStart` に **バージョン付きの実体パスを書いてはいけない**。`mise upgrade` 後、古いバージョンは猶予期間後に削除される。shim（`~/.local/share/mise/shims/passage`）か `mise exec` 経由で起動し、アップグレード後は `systemctl --user restart passage` が必要。
6. apt は goreleaser の `nfpms` で `.deb`（バイナリ + `/usr/lib/systemd/user/passage.service`）を同時に出せる。ただし `apt upgrade` で更新できるようにするには apt リポジトリの運用（署名鍵・ホスティング）が必要で、mise より手間が大きい。GitHub Releases に `.deb` を置くだけなら手間は小さいが、更新は手動。

## 1. mise のバックエンド比較

| バックエンド | 仕組み | passage 側の準備 | ゲスト側の前提 | 評価 |
|---|---|---|---|---|
| `github` | GitHub Releases のアセットを選んでダウンロード・検証・展開 | goreleaser でリリースするだけ | mise のみ | **第一候補** |
| `ubi` | ubi ライブラリで Releases から単一実行ファイルを取得 | 同上 | mise のみ | 非推奨・2027.1.0 で削除予定 |
| `aqua` | aqua registry のレシピに従ってダウンロード | aqua-registry へのレシピ登録、または自前 registry | mise のみ | レシピ管理の手間が増える |
| `go` | `go install` でソースからビルド | Go モジュールとして公開するだけ | Go ツールチェーン（cgo 次第で C コンパイラ等） | ゲストに Go が要る |

### `github` バックエンド

- 書式は `github:owner/repo`、バージョン固定は `@1.2.3`。公開リリースならトークン不要。[mise: github backend](https://mise.jdx.dev/dev-tools/backends/github.html)
- アセット選択: `url` 指定 → `asset_pattern` 指定 → それ以外は OS・CPU アーキテクチャ・libc（glibc/musl）の一致、扱いやすいアーカイブ形式、リポジトリ名で始まる名前に加点し、チェックサムや署名ファイルなどを減点して最高点を選ぶ。アーキテクチャ名は `amd64` / `x86_64` / `aarch64` も受け付ける。[同上](https://mise.jdx.dev/dev-tools/backends/github.html)
  - goreleaser 既定のアーカイブ名テンプレートは `{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}` で始まり、既定形式は `tar.gz`。[goreleaser: archives](https://goreleaser.com/customization/archive/) → 自動検出に素直に乗るので `asset_pattern` は不要の見込み。
- 検証: GitHub が報告するダイジェスト、または同じリリース内のチェックサムファイルと照合する。lockfile 有効時は `mise.lock` に記録。公開 GitHub では artifact attestation も検証する。SLSA 検証には `slsa_signer_identity` / `slsa_signer_issuer` を指定する。[同上](https://mise.jdx.dev/dev-tools/backends/github.html)
  - したがって GitHub Actions でリリースする際に attestation（`actions/attest-build-provenance` など）を付けておくと、利用者側で追加設定なしに検証が効く。
- オプション: `bin`（単一ファイル名の変更）、`bin_path`、`rename_exe` など。単一バイナリの tar.gz なら通常は不要。

### `ubi` バックエンド

- 「deprecated: move each `ubi:` tool to the github, gitlab or http backend」「The ubi backend will be removed in mise 2027.1.0」。[mise: ubi backend](https://mise.jdx.dev/dev-tools/backends/ubi.html) → 採用しない。

### `aqua` バックエンド

- aqua registry のレシピでビルド済みバイナリを導入する。レシピは `aqua.registries` で指定した registry、または mise に同梱された aqua registry のスナップショットから探す。[mise: aqua backend](https://mise.jdx.dev/dev-tools/backends/aqua.html)
- 検証（チェックサム・attestation・cosign・SLSA・minisign）はレシピの宣言次第。[同上](https://mise.jdx.dev/dev-tools/backends/aqua.html)
- passage で使うには aqua-registry 本体へのレシピ追加、または自前 registry の運用が必要で、`github` に対する利点が乏しい。

### `go` バックエンド

- `go install` でビルドする。Go が必要で、mise はプロジェクトに設定された Go を使う。cgo を使うなら C コンパイラなども要る。[mise: go backend](https://mise.jdx.dev/dev-tools/backends/go.html)
- ゲストに Go を入れる必要があり、「単一バイナリを配る」という前提から外れる。

## 2. mise registry への登録

- 短縮名は必須ではない。`github:owner/repo` のようなバックエンド識別子で使える。[mise: registry](https://mise.jdx.dev/registry.html)
- 登録基準: 「The bar is normally thousands of GitHub stars, active maintenance, and real use outside the author's own projects.」PR に `## Popularity` 節が要る。[mise: Adding tools to the registry](https://mise.jdx.dev/contributing/registry.html)
- バックエンドの受け入れ階層: 1 `packslip`、2 `aqua` / `github` / `gitlab`、3 `conda`、4 `go` など（事前合意が必要）、`asdf` / `vfox` / `ubi` は新規不可。[同上](https://mise.jdx.dev/contributing/registry.html)
- 結論: passage は個人用ツールなので登録対象外。登録しなくても機能上の不足はない。

## 3. systemd ユーザー unit の配置

### unit の置き場所

- ユーザー unit の検索パス（抜粋、優先度順）: `~/.config/systemd/user` → `/etc/systemd/user` → `~/.local/share/systemd/user` → `/usr/local/share/systemd/user`・`/usr/share/systemd/user` → `/usr/local/lib/systemd/user` → `/usr/lib/systemd/user`。`systemd-analyze --user unit-paths` で確認できる。[systemd.unit(5)](https://www.freedesktop.org/software/systemd/man/latest/systemd.unit.html)
- `%h` はサービスマネージャを動かすユーザーのホームディレクトリに展開される。[同上](https://www.freedesktop.org/software/systemd/man/latest/systemd.unit.html)
- mise で入れる場合（root 不要）は `~/.config/systemd/user/passage.service`、apt の場合は `/usr/lib/systemd/user/passage.service` が自然。

### 方式の比較

| 方式 | 内容 | 評価 |
|---|---|---|
| A. `passage install-service` サブコマンド | バイナリが unit を `~/.config/systemd/user/` に書き出し、`systemctl --user daemon-reload` と `enable --now` を行う（またはその手順を表示） | **推奨**。mise でも apt でも手動配置でも同じ手順で使える。unit の内容をバイナリと同じバージョンで管理できる |
| B. mise の per-tool `postinstall` | `[tools]` の `"github:backpaper0/passage" = { version = "latest", postinstall = "passage install-service" }` のように書く | 利用者が自分の mise 設定に書くもので、ツール側から配布はできない。A を自動で呼ぶ補助としては有用 |
| C. アーカイブに unit ファイルを同梱 | goreleaser `archives.files` で同梱し、利用者が手でコピー | パスの書き換えが必要になりがちで、A に劣る |

- per-tool `postinstall` は既定で「mise がそのツールをインストールまたは修復したときだけ」走る。`when = "always"` で毎回実行も可。`MISE_TOOL_INSTALL_PATH` などが渡され、失敗するとインストール自体が失敗する。[mise: dev tools](https://mise.jdx.dev/dev-tools/)
- グローバルな `[hooks].postinstall` は `mise install` 全体の後に 1 回走り、`MISE_INSTALLED_TOOLS`（JSON、`install_path` 含む）が渡される。[mise: hooks](https://mise.jdx.dev/hooks.html)
- `MISE_SAFE=1` や `--no-hooks` ではフックは走らない。[同上](https://mise.jdx.dev/hooks.html)

## 4. バージョンを上げたときのバイナリのパス

### mise のディレクトリ構成

- 実体は `~/.local/share/mise/installs/<tool>/<version>/` に入る（既定の legacy レイアウト）。プレフィックスやエイリアスのリンク（例: `installs/node/24`）も作られる。ただし「Use `mise where node` or `mise which node` instead of building paths from them.」と、パスを自前で組み立てないよう案内されている。[mise: directories](https://mise.jdx.dev/directories.html)
- 実験的な identity レイアウトでは実体が `installs/<label>-<hash>/` に入り、`installs/<tool>/<version>` はそこへのリンクになる。[mise: install layout](https://mise.jdx.dev/dev-tools/install-layout.html) → レイアウトは将来変わりうるので、`installs/` 配下のパスを unit に書くのは避けるべき。
- `mise upgrade` 後、古いバージョンは `upgrade.prune_after` の猶予期間後に削除される（`--prune` で即時）。[mise upgrade](https://mise.jdx.dev/cli/upgrade.html) → **バージョン付きの絶対パスを `ExecStart` に書くと、アップグレード後しばらくして起動できなくなる。**
- `mise which passage` は shim を経由せず実体のパスを出す。[mise which](https://mise.jdx.dev/cli/which.html) → これもバージョン付きパスなので unit に焼き込むのには向かない。

### shim の挙動

- shim は `~/.local/share/mise/shims` にあり、Unix では mise バイナリへのシンボリックリンク。実行時に「現在のディレクトリの設定を読んでバージョンを選び」、その `[env]` を設定して実体を起動する。[mise: shims](https://mise.jdx.dev/dev-tools/shims.html)
- systemd ユーザーサービスの `WorkingDirectory=` 未指定時の既定はユーザーのホームディレクトリ。[systemd.exec(5)](https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html) → ホームで解決されるので、グローバル設定（`mise use -g`）のバージョンが選ばれる。
- `ExecStart=` の実行ファイルは絶対パスか、スラッシュを含まない名前（`/usr/local/bin` や `/usr/bin` などの固定検索パスで解決）。[systemd.service(5)](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html) → mise の shim や `~/.local/bin` は固定検索パスに含まれないため、`%h` を使った絶対パスで書く。

### 推奨する unit の形

```ini
[Unit]
Description=passage (password-store 互換 Web ツール)

[Service]
# shim 経由にすると mise のグローバル設定のバージョンが選ばれる
ExecStart=%h/.local/share/mise/shims/passage serve
Restart=on-failure

[Install]
WantedBy=default.target
```

- 代替: `ExecStart=%h/.local/bin/mise exec github:backpaper0/passage@latest -- passage serve` のように `mise exec` を使う方法もある（mise 本体の場所に依存する）。
- `install-service` は `os.Executable()` から mise 配下か apt 配下かを判定し、mise 配下なら shim パス、`/usr/bin` なら `/usr/bin/passage` を書く、という実装が考えられる。
- **アップグレード後の再起動**: unit は実行中のプロセスを差し替えないので、`mise upgrade` 後に `systemctl --user restart passage` が必要。利用者の mise 設定の per-tool `postinstall` に書いておけば自動化できる（例: `postinstall = "systemctl --user try-restart passage"`）。

### ゲスト側の前提（OrbStack / WSL2）

- ログインしていなくてもユーザーサービスを動かすには `loginctl enable-linger`（「a user manager is spawned for the user at boot and kept around after logouts」）。[loginctl(1)](https://www.freedesktop.org/software/systemd/man/latest/loginctl.html)
- WSL2: 現行 Ubuntu では systemd が既定。それ以外は `/etc/wsl.conf` に `[boot] systemd=true`。また「systemd services will NOT keep your WSL instance alive」— ゲストが停止すれば passage も止まる。[Microsoft Learn: systemd in WSL](https://learn.microsoft.com/en-us/windows/wsl/systemd)

## 5. apt（次点）との比較と goreleaser

### goreleaser で両方出せるか

- 出せる。`nfpms` で `deb` / `rpm` / `apk` などを生成でき、バイナリは自動で入り、追加ファイルは `contents`（`src` / `dst` / `type`）で入れる。`scripts.postinstall` などもある。[goreleaser: nFPM](https://goreleaser.com/customization/nfpm/)
- 同じ `.goreleaser.yaml` で `archives`（mise の `github` バックエンド向け tar.gz）と `nfpms`（`.deb`）を同時に生成し、GitHub Release に載せられる。

```yaml
nfpms:
  - formats: [deb]
    contents:
      - src: packaging/passage.service
        dst: /usr/lib/systemd/user/passage.service
```

- `.deb` の場合、unit はシステム全体に置かれるが、有効化は各ユーザーが `systemctl --user enable --now passage` を行う（パッケージの postinstall から個々のユーザーの unit を有効化するのは筋が悪い）。

### apt の配布経路の手間

| 経路 | 手間 | 更新 |
|---|---|---|
| GitHub Releases に `.deb` を置く | goreleaser が生成するだけ。利用者は `sudo apt install ./passage_*.deb` | 手動でダウンロードし直す |
| 自前 apt リポジトリ | リポジトリのメタデータ生成・GPG 署名鍵の管理・ホスティングが必要 | `apt upgrade` で可 |
| Gemfury / Cloudsmith | goreleaser の publisher は **Pro 限定**。[Fury](https://goreleaser.com/customization/fury/)・[Cloudsmith](https://goreleaser.com/customization/publish/cloudsmith/) | `apt upgrade` で可 |
| PPA（Launchpad） | Launchpad 上でソースパッケージからビルドする仕組みのため、goreleaser の成果物をそのまま流せない（Go 依存の vendoring など Debian パッケージングが必要） | `apt upgrade` で可 |

- PPA の項は Launchpad の一般的な仕組みに基づく判断で、本調査では一次資料を個別に確認していない。

### 比較まとめ

- mise（`github` バックエンド）: root 不要、署名・リポジトリ運用不要、更新は `mise upgrade`、検証（チェックサム・attestation）も自動。既に mise を使うゲストの運用（mise + fnox + pass + GnuPG）とも一致する。
- apt: `.deb` は goreleaser でほぼ無料で作れるが、自動更新させたいならリポジトリ運用コストが乗る。「GitHub Releases の `.deb` を手動導入」までを次点の範囲とするのが現実的。

## 未確認・残課題

- goreleaser が `nfpms` の成果物を GitHub Release に既定でアップロードすること（一般的な挙動だが、本調査では該当ドキュメントの明記を確認できなかった）。
- 実際の goreleaser の出力に対して `mise use github:backpaper0/passage` が `asset_pattern` なしで正しいアセットを選ぶかは、初回リリース時に確認する。
- shim 経由で起動した場合のプロセス構成（shim が `exec` で置き換わるか、子プロセスになるか）とシグナル伝搬。`systemctl --user stop` で正しく止まるかを実機で確認する。
