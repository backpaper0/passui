# OrbStack / WSL2 での systemd ユーザーサービスと localhost 転送の挙動

調査日：2026-11-03 / 対象チケット：[#4](https://github.com/backpaper0/passage/issues/4)

passage をゲスト上の systemd **ユーザーサービス**として常駐させ、ホストのブラウザから `localhost` でアクセスする前提で、2 つの環境（macOS + OrbStack / Windows + WSL2）の挙動を一次情報で確認した。

出典の凡例：**[文書]** 公式ドキュメント、**[ソース]** 公式リポジトリのソースコード、**[実機]** この調査セッションが動いている OrbStack ゲストでの観測、**[未確認]** 一次情報で裏付けられず実機確認が必要なもの。

## 結論（要約）

| 項目 | OrbStack（Linux Machine / Ubuntu） | WSL2（Ubuntu） |
| --- | --- | --- |
| systemd が PID 1 か | 既定で systemd | `/etc/wsl.conf` の `[boot] systemd=true` が必要（既定 `false`） |
| `systemctl --user` | 使える | 使える（WSL がログインセッションを作る） |
| lingering | 実機では既に有効（OrbStack が設定している模様） | なくてもインスタンス起動中はユーザーマネージャが動く。有効化しておけば確実 |
| ゲストの寿命 | OrbStack 起動中は動き続ける（自動起動は未確認） | **最後の `wsl.exe` クライアントが閉じてから 15 秒でディストリビューションが止まる**。`instanceIdleTimeout=-1` が必要 |
| `127.0.0.1` バインドでホストから届くか | 届く見込み（要実機確認） | NAT：届く（`localhostForwarding` 既定 `true`）。mirrored：届く |
| LAN への露出 | `0.0.0.0` / `::` バインドだと **LAN に露出**（「Expose ports to LAN」設定で抑止可） | NAT：既定では LAN に出ない。mirrored：`0.0.0.0` で LAN 到達経路あり（Hyper-V ファイアウォールが既定で受信を通さない） |
| ホスト上の他ユーザー | ホストの loopback は全ユーザー共有なので到達可能 | 同左 |

**passage への含意**

- `127.0.0.1`（と必要なら `::1`）にだけバインドする。`0.0.0.0` にはしない。OrbStack では `0.0.0.0` が LAN 露出に直結する。
- `*.orb.local` ドメインはゲストの IP 宛てなので、`127.0.0.1` バインドでは使えない。`localhost:<port>` で運用する。
- ホストの他ユーザーやゲストの他ユーザー、ブラウザ上の他サイト（DNS rebinding 等）から localhost のポートには到達できるので、認証必須の決定（#2）は妥当。`Host` ヘッダ検証も検討に値する。
- WSL2 では、何もしないと端末を閉じた 15 秒後にゲストごと止まる。サービス登録手順に `.wslconfig` の `[general] instanceIdleTimeout=-1` を含める必要がある。

---

## 1. systemd とユーザーサービス

### OrbStack

- OrbStack のドキュメントには「ディストリビューションの init システムでサービスを動かせる。ディストリビューションによって init は systemd / OpenRC / runit。起動時のサービス開始も通常どおり設定できる」とある。**[文書]** <https://docs.orbstack.dev/machines/>
- 実機（Ubuntu 26.04.1 LTS、systemd 259）で確認した内容 **[実機]**：
  - PID 1 が `systemd`。`systemctl --user is-system-running` は `running`
  - `XDG_RUNTIME_DIR=/run/user/501`、`DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/501/bus` が設定済み
  - `loginctl list-users` で `dev` と `root` が `LINGER yes / STATE lingering`
  - `/var/lib/systemd/linger/dev` はマシン作成（2026-08-24 21:32）の約 3 分後に作られており、シェル履歴に `enable-linger` の実行はない。**OrbStack がプロビジョニング時に lingering を有効化していると考えられる**が、ドキュメントには記載がない。**[未確認]**
- 追加の有効化手順は不要。念のため `loginctl enable-linger` を実行しても冪等で害はない。

### WSL2

- systemd を有効化するには `/etc/wsl.conf` に次を書き、`wsl.exe --shutdown` で再起動する。`[boot] systemd` は既定 `false`、WSL 2 のみ対応、Windows 11 / Server 2022 で利用可。**[文書]** <https://learn.microsoft.com/en-us/windows/wsl/wsl-config#systemd-support>

  ```ini
  [boot]
  systemd=true
  ```

  ディストリビューションのイメージによっては最初から書かれている場合があるので、`cat /etc/wsl.conf` で確認する。
- 設定変更が反映されるのは、ディストリビューションが完全に止まって再起動してから（いわゆる「8 秒ルール」）。**[文書]** 同ページ
- WSL の init は、ユーザーのプロセスを起動するときに `/bin/login -f <user>` でログインセッションを作る（`CreateLoginSession`）。セッションはユーザーごとに 1 つ作られ、インスタンスが動いている間は保持される。そのため、`user@UID.service`（ユーザーマネージャ）は lingering がなくても起動する。**[ソース]** [`src/linux/init/config.cpp`](https://github.com/microsoft/WSL/blob/master/src/linux/init/config.cpp)（`CreateLoginSession`）。init は `XDG_RUNTIME_DIR=/run/user/<uid>` と `DBUS_SESSION_BUS_ADDRESS` も設定する。**[ソース]** [`src/linux/init/init.cpp`](https://github.com/microsoft/WSL/blob/master/src/linux/init/init.cpp)

### lingering（共通）

- `loginctl enable-linger`：「有効にすると、そのユーザーのユーザーマネージャがブート時に起動し、ログアウト後も維持される。ログインしていないユーザーが長時間動くサービスを実行できる」。**[文書]** <https://www.freedesktop.org/software/systemd/man/latest/loginctl.html>
- lingering がない場合、ユーザーマネージャは最初のセッション登録で起動し、最後のセッションが閉じると `/run/user/$UID` と一緒に停止する。**[文書]** <https://www.freedesktop.org/software/systemd/man/latest/pam_systemd.html>
- passage の判断：両環境とも**登録手順に `loginctl enable-linger` を含める**のが安全。OrbStack では既に有効、WSL2 でもセッションが作られるので実害はないが、ゲスト起動直後からサービスを `default.target` で立ち上げる保証になる。

## 2. ゲストの寿命（常駐できるか）

### WSL2：アイドル停止が最大の落とし穴

- `.wslconfig` の `[general] instanceIdleTimeout`：既定 `15000` ms。「ディストリビューションがアイドルになってから停止するまでのミリ秒。`-1` で自動停止を無効化」。`[wsl2] vmIdleTimeout`（既定 `60000` ms、Windows 11 のみ）は VM 側の同様のタイマー。**[文書]** <https://learn.microsoft.com/en-us/windows/wsl/wsl-config#main-wsl-settings>
- **「アイドル」の判定は Windows 側のクライアントプロセスで行われ、Linux 側のプロセスは見ていない**。インスタンス作成時に、呼び出し元の Windows プロセス（`wsl.exe` など）をライフタイムマネージャに登録し、そのプロセスがすべて終わってから `instanceIdleTimeout` 後にインスタンスを終了する。値が負なら終了しない。VM は、実行中のインスタンスがなくなってから `vmIdleTimeout` 後に終了する。**[ソース]** [`src/windows/service/exe/LxssUserSession.cpp`](https://github.com/microsoft/WSL/blob/master/src/windows/service/exe/LxssUserSession.cpp)（`RegisterCallback(..., GetIdleTimeout())`、`_VmIsIdle`）
- つまり、**systemd ユーザーサービスが動いていても、端末を閉じると約 15 秒後に passage ごとゲストが止まる**。同じ症状の報告が公式リポジトリにある（2026-11 時点で open）。[microsoft/WSL#13416](https://github.com/microsoft/WSL/issues/13416)、[#13291](https://github.com/microsoft/WSL/issues/13291)、[Discussion #8854](https://github.com/microsoft/WSL/discussions/8854)
- 対策：`%UserProfile%\.wslconfig` に次を書く。

  ```ini
  [general]
  instanceIdleTimeout=-1
  ```

  加えて、Windows 起動後にゲストを起こす手段が必要（ゲストが動いていないと passage も動かない）。Windows のログオン時に `wsl.exe` を実行するタスクを登録する方法が考えられるが、Microsoft の公式手順はない。**[未確認]**（配布とサービス登録の手順のチケットで扱う）

### OrbStack

- ドキュメントには「アイドルの Linux マシンは CPU をほぼ使わない」とあるだけで、アイドル停止の記述はない。**[文書]** <https://docs.orbstack.dev/machines/>
- 実機では、ユーザーのシェルがなくても lingering でユーザーマネージャが動いている（セッション `c2` は class `manager-early`）。**[実機]**
- macOS ログイン時の OrbStack 自動起動と、OrbStack 起動時のマシン自動起動は、ドキュメントに記載が見つからない。**[未確認]**

## 3. ホストのブラウザから届く条件

### OrbStack

- 「Linux マシンで動かしたサーバーにはすべて Mac から到達できる」。例として、ポート 8000 のサーバーに Mac の `localhost:8000` でアクセスできる。**[文書]** <https://docs.orbstack.dev/machines/network>
- 「サーバーが `0.0.0.0` または `::` で listen していると、ネットワーク上の他のデバイスからも到達できる」。Mac からだけに限るには、Machines 設定の「Expose ports to LAN」を切る（`machines.expose_ports_to_lan`）。**[文書]** 同ページ
- `127.0.0.1` バインドのサーバーが Mac の `localhost` に転送されるかは、文書の「すべてのサーバー」という書き方と、LAN 露出を `0.0.0.0` / `::` の場合に限っている書き方から、**転送される（ただし Mac の localhost のみ）と読める**が、明記はされていない。ゲスト内の `mac` コマンドはこのセッションでは使えず、ホスト側での確認はできなかった。**[未確認]**
  - 確認手順：ゲストで `python3 -m http.server 8000 --bind 127.0.0.1`、Mac で `curl localhost:8000`
- `*.orb.local`：マシンごとに `<machine>.orb.local` が自動で割り当てられ、`orb config set machine.<name>.http_port <port>` で非標準ポートに向けられる。**[文書]** 同ページ。これはマシンの IP 宛てなので、ゲストで `127.0.0.1` にバインドしたサーバーには届かない（Linux の loopback バインドの一般的な性質から推論）。

### WSL2：NAT モード（既定）

- NAT モードでは、Linux のネットワークアプリに Windows のブラウザから `localhost` でアクセスできる。**[文書]** <https://learn.microsoft.com/en-us/windows/wsl/networking#accessing-linux-networking-apps-from-windows-localhost>
- `.wslconfig` の `[wsl2] localhostForwarding`：既定 `true`。「WSL 2 VM でワイルドカードまたは localhost にバインドしたポートに、ホストから `localhost:port` で接続できるか」。mirrored モードでは無視される。**[文書]** <https://learn.microsoft.com/en-us/windows/wsl/wsl-config#main-wsl-settings>
- 実装：Windows 側の `wslrelay` は、Linux 側の listen アドレスが `0.0.0.0` / `127.0.0.1`（IPv6 なら `::` / `::1`）の TCP ポートだけを中継対象にし、**Windows 側では `127.0.0.1` / `::1` にだけ bind する**。**[ソース]** [`src/windows/wslrelay/localhost.cpp`](https://github.com/microsoft/WSL/blob/master/src/windows/wslrelay/localhost.cpp)（`BindRelayListener` で `INADDR_LOOPBACK` / `IN6ADDR_LOOPBACK_INIT`）
- → **`127.0.0.1` バインドで届く**。

### WSL2：mirrored モード

- `.wslconfig` の `[wsl2] networkingMode=mirrored`（Windows 11 22H2 以降）。Windows とゲストが互いに `localhost`（`127.0.0.1`）で接続できる。IPv6 の `::1` は非対応。**[文書]** <https://learn.microsoft.com/en-us/windows/wsl/networking#mirrored-mode-networking>
- → **`127.0.0.1` バインドで届く**。`::1` だけにバインドすると届かない。

## 4. ゲスト外からの到達（露出範囲）

### OrbStack

- `0.0.0.0` / `::` バインドは**既定で LAN に露出する**。「Expose ports to LAN」を切れば Mac のみに限定できる。**[文書]** <https://docs.orbstack.dev/machines/network>
- `127.0.0.1` バインドなら LAN 露出の条件に当てはまらない（文書の条件から推論）。**[未確認]**

### WSL2

- NAT：WSL 2 は独自 IP の仮想 NIC を持つので、LAN からのアクセスは既定では通らない。通すには `netsh interface portproxy` などの明示的な設定が必要。**[文書]** <https://learn.microsoft.com/en-us/windows/wsl/networking#accessing-a-wsl-2-distribution-from-your-local-area-network-lan>。`localhostForwarding` の中継は Windows の loopback にだけ bind する。**[ソース]** 前掲
  - ただし `0.0.0.0` にバインドすると、ゲストの仮想 NIC の IP（例 `172.x`）経由で Windows ホスト上のプロセスから届く。
- mirrored：利点の一つが「LAN から WSL に直接接続できる」こと。**[文書]** 前掲。受信を許すには Hyper-V ファイアウォールで `DefaultInboundAction Allow` にするか、ルールを追加する手順が示されている。つまり既定では、Hyper-V ファイアウォールが LAN からの受信を通さない前提。**[文書]** 前掲、<https://learn.microsoft.com/en-us/windows/security/operating-system-security/network-security/windows-firewall/hyper-v-firewall>
  - Hyper-V ファイアウォールは Windows 11 22H2 + WSL 2.0.9 以降で既定で有効（`.wslconfig` の `firewall` 既定 `true`）。ホストとゲスト間の loopback は `LoopbackEnabled` で既定で許可されている。**[文書]** 同上
  - 企業ポリシーや手動設定で `DefaultInboundAction Allow` にされている環境では、`0.0.0.0` バインドが LAN に露出しうる。公用環境（Windows）では特に `127.0.0.1` バインドを守るべき。

### ホスト上の他ユーザー（両環境共通）

- 転送先はホストの loopback（OrbStack は Mac の `localhost`、WSL の NAT は `127.0.0.1` / `::1`、mirrored は共有 loopback）で、loopback はユーザーごとに分かれていない。ホストにログインしている他ユーザーのプロセスも接続できる。ゲスト内の他ユーザーも同様。
- ブラウザで開いている他のサイトも、`localhost` へのリクエストは送れる（CSRF、DNS rebinding）。
- → ネットワーク層では隔離できないので、**アプリ層の認証が必須**（#2 の決定と整合）。

## 未確認事項（実機で確認すべきもの）

1. OrbStack：ゲストで `127.0.0.1` にバインドしたポートが Mac の `localhost` に転送されるか。そのとき Mac 側の bind が loopback だけか（Mac で `lsof -iTCP -sTCP:LISTEN -n -P` を実行）
2. OrbStack：lingering が既定で有効になるか（新規マシンで `loginctl show-user $USER -p Linger`）
3. OrbStack：macOS ログイン時の自動起動と、マシンの自動起動
4. WSL2：`instanceIdleTimeout=-1` を設定したうえで、端末を閉じても passage が生き続けるか（#13416 のような退行がないか）
5. WSL2：Windows ログオン時にゲストを起こす方法（タスクスケジューラ等）

## 出典

- OrbStack Docs — Linux machines: <https://docs.orbstack.dev/machines/>
- OrbStack Docs — Machine networking: <https://docs.orbstack.dev/machines/network>
- Microsoft Learn — Advanced settings configuration in WSL（wsl.conf / .wslconfig）: <https://learn.microsoft.com/en-us/windows/wsl/wsl-config>
- Microsoft Learn — Accessing network applications with WSL: <https://learn.microsoft.com/en-us/windows/wsl/networking>
- Microsoft Learn — Hyper-V Firewall: <https://learn.microsoft.com/en-us/windows/security/operating-system-security/network-security/windows-firewall/hyper-v-firewall>
- microsoft/WSL ソース: `src/windows/service/exe/LxssUserSession.cpp`、`src/windows/wslrelay/localhost.cpp`、`src/linux/init/config.cpp`、`src/linux/init/init.cpp`
- microsoft/WSL issues: [#13416](https://github.com/microsoft/WSL/issues/13416)、[#13291](https://github.com/microsoft/WSL/issues/13291)、[Discussion #8854](https://github.com/microsoft/WSL/discussions/8854)
- systemd: [loginctl(1)](https://www.freedesktop.org/software/systemd/man/latest/loginctl.html)、[pam_systemd(8)](https://www.freedesktop.org/software/systemd/man/latest/pam_systemd.html)
