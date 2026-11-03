# WSL2 ゲストを常時稼働させる方法

調査日：2026-11-03 / 対象チケット：[#12](https://github.com/backpaper0/passage/issues/12)（前提：[#4 の調査](https://github.com/backpaper0/passage/blob/research/systemd-and-port-forwarding/docs/research/systemd-and-port-forwarding.md)）

passage は WSL2 のゲスト（Ubuntu）で systemd **ユーザーサービス**として動く。WSL2 は、Windows 側の `wsl.exe` クライアントがすべて終わってから約 15 秒でディストリビューションを止める。この調査では、(1) `.wslconfig` の `instanceIdleTimeout=-1` が本当に効くか、(2) Windows ログオン時にゲストを起こす方法、(3) passage のインストール手順に何を入れるべきか、を一次情報で確認した。

出典の凡例：**[文書]** 公式ドキュメント、**[ソース]** microsoft/WSL のソースコード（コミット [`42efa52`](https://github.com/microsoft/WSL/tree/42efa52b4cb21c10c9fb05b9e26d53d72e80c2d0)、`3.0.5-16-g42efa52`、2026-11-03 時点の `master`）、**[issue]** microsoft/WSL の issue での WSL チーム（Collaborator / Member）の発言、**[報告]** 同 issue でのユーザー報告、**[未確認]** 一次情報で裏付けられず、実機確認が必要なもの。

この調査は OrbStack ゲスト上で行ったので、Windows 実機での動作確認はしていない。

## 結論（要約）

- **`[general] instanceIdleTimeout=-1` は効く。** ソース上、値が負ならインスタンスにアイドル終了のタイマーが一切登録されない。WSL チームも、この設定で「ディストリビューションがタイムアウトしなくなる」と複数の issue で案内している。導入されたのは **WSL 2.5.4**（2025-03）。
- **#13416 と #13291 は「設定が効かない」報告ではない。** どちらも、`instanceIdleTimeout` を設定していない（または `vmIdleTimeout` だけを設定した）状態で、既定の 15 秒で止まったもの。#13291 は WSL チームが `instanceIdleTimeout=-1` を案内して解決済み。#13416（open）のスレッドにも、`instanceIdleTimeout=-1` を設定して止まったという報告はない。
- **キーは `[general]` に書く。** `[wsl2]` に書くと別のキー（`wsl2.instanceIdleTimeout`）として扱われ、未知のキーの警告が出るだけで効かない。`[wsl2] vmIdleTimeout` は VM の寿命を決める別のタイマー。ディストリビューションが動いている間は VM はアイドル扱いにならないので、passage の用途では設定不要。
- **`-1` にしても、ゲストを最初に起動するのは誰かの役目。** Windows の起動、ログオフ、`wsl --shutdown`、WSL の更新（サービス停止）のあとは、ゲストは止まったまま。ログオン時に `wsl.exe`（または `wslg.exe`）を 1 回実行して起こす仕組みが要る。
- **推奨構成：** `instanceIdleTimeout=-1`、ログオン時に `wslg.exe -d <distro> --shell-type none -- /bin/true` を 1 回実行（タスクスケジューラかスタートアップフォルダ）、ゲスト側で `systemd=true`、`loginctl enable-linger`、`systemctl --user enable passage`。どれも管理者権限は要らない見込み（タスクスケジューラ分は **[未確認]**）。

---

## 1. `instanceIdleTimeout=-1` は効くか

### 1.1 文書上の定義

- `.wslconfig` の `[general]` セクションに `instanceIdleTimeout` がある。既定 `15000`。「ディストリビューションがアイドルになってから停止するまでのミリ秒。`-1` で自動停止を無効化」。**[文書]** <https://learn.microsoft.com/en-us/windows/wsl/wsl-config#general-wsl-settings>（ms.date 2026-10-16）
- `[wsl2]` の `vmIdleTimeout`（既定 `60000`、Windows 11 のみ）は「VM がアイドルになってから停止するまでのミリ秒」。**[文書]** 同ページ
- WSL Settings アプリの Optional Features ページにも「Distribution idle timeout」として出ている。説明は「ディストリビューションがアイドルになってから終了するまでのミリ秒」。**[ソース]** [`localization/strings/en-US/Resources.resw`](https://github.com/microsoft/WSL/blob/42efa52b4cb21c10c9fb05b9e26d53d72e80c2d0/localization/strings/en-US/Resources.resw)（`Settings_InstanceIdleTimeout.*`）、`src/windows/wslsettings/Views/Settings/OptionalFeaturesPage.xaml`

### 1.2 導入バージョン

- WSL **2.5.4**（pre-release、2025-03-26）のリリースノート：「Introduce general.instanceIdleTimeout to control distribution termination timeouts (solves #10138, #9401)」。**[文書]** <https://github.com/microsoft/WSL/releases/tag/2.5.4>
- 同日、WSL チームの OneBlue が #10138（「How to make wsl2 alive in the background」）に「2.5.4 で解決。`[general] instanceIdleTimeout=-1` でディストリビューションのタイムアウトを防げる」とコメントしてクローズ。**[issue]** <https://github.com/microsoft/WSL/issues/10138>
- それ以前（〜2.4.x）は、ディストリビューションのアイドルタイムアウトを変える手段がなかった。2023 年の同 issue で OneBlue が `vmIdleTimeout=-1` を案内したが、質問者は「ターミナルを閉じて約 10 秒でディストリビューションが止まる。欲しいのはこれではない」と返している。**[issue]** 同上
- 2026-11 時点の安定版は 3.0.3（2026-10-09）。現行ソースでも同じ実装（下記）が残っている。**[文書]** <https://github.com/microsoft/WSL/releases>

### 1.3 ソース上の動作

設定のキー：

- `general.instanceIdleTimeout`（既定 `15 * 1000`）と `wsl2.vmIdleTimeout`（既定 `60 * 1000`）。**[ソース]** [`src/windows/common/WslCoreConfig.h`](https://github.com/microsoft/WSL/blob/42efa52b4cb21c10c9fb05b9e26d53d72e80c2d0/src/windows/common/WslCoreConfig.h)（L255、L275、L339–340）
- キー名はセクション込みで照合する。未知のキーは「Unknown key '{}' in {}:{}」の警告になる。**[ソース]** [`src/shared/configfile/configfile.cpp`](https://github.com/microsoft/WSL/blob/42efa52b4cb21c10c9fb05b9e26d53d72e80c2d0/src/shared/configfile/configfile.cpp)（L216）
  - → `[wsl2]` の下に書いた `instanceIdleTimeout` は `wsl2.instanceIdleTimeout` となり、効かない。#13291 でも、OneBlue が最初に `[wsl2]` の下として案内し、報告者が `[general]` に直している。**[issue]**

ディストリビューション（インスタンス）の停止：

- インスタンスを作るとき（または既存インスタンスに接続するとき）、`GetIdleTimeout() >= 0` の場合だけ、呼び出し元の Windows プロセスをライフタイムマネージャに登録する。コメント：「A value of less than zero indicates that the instance should never be idle-terminated.」**[ソース]** [`src/windows/service/exe/LxssUserSession.cpp`](https://github.com/microsoft/WSL/blob/42efa52b4cb21c10c9fb05b9e26d53d72e80c2d0/src/windows/service/exe/LxssUserSession.cpp)（L2950–2968）
- 登録したプロセスがすべて終了すると、`timeout` ミリ秒のタイマーが動き、満了で `s_TerminateInstance` が呼ばれる。**[ソース]** [`src/windows/service/exe/Lifetime.cpp`](https://github.com/microsoft/WSL/blob/42efa52b4cb21c10c9fb05b9e26d53d72e80c2d0/src/windows/service/exe/Lifetime.cpp)（`s_OnClientProcessTerminated`、`s_OnTimeout`）
- 停止要求を受けた Linux 側の init は、systemd 有効時は `systemctl poweroff` を実行する。#13416 で報告者が journal に見た「外部から要求されたクリーンな poweroff」はこれ。**[ソース]** [`src/linux/init/init.cpp`](https://github.com/microsoft/WSL/blob/42efa52b4cb21c10c9fb05b9e26d53d72e80c2d0/src/linux/init/init.cpp)（`InitTerminateInstanceInternal`）
- 判定に使うのは Windows 側のクライアントプロセスだけ。Linux 側で systemd サービスが動いているかは見ていない。
- WSL2 では、インスタンスの idle timeout は **VM 作成時に読んだ設定**（`m_vmConfig.InstanceIdleTimeout`）から渡る。**[ソース]** [`src/windows/service/exe/WslCoreVm.cpp`](https://github.com/microsoft/WSL/blob/42efa52b4cb21c10c9fb05b9e26d53d72e80c2d0/src/windows/service/exe/WslCoreVm.cpp)（L1365）
  - → `.wslconfig` を書き換えたら **`wsl --shutdown` で VM を止めてから**再起動しないと反映されない。

VM の停止（`vmIdleTimeout`）との関係：

- VM がアイドルとみなされるのは「VM があり、ロック中のディストリビューションがなく、実行中の WSL2 インスタンスが 1 つもない」ときだけ。その場合に `vmIdleTimeout` のタイマーが動く。負の値なら `wsl --shutdown` かサービス停止まで VM は止まらない。**[ソース]** `LxssUserSession.cpp`（`_VmCheckIdle`、`_VmIsIdle`、L4196–4230）
- → `instanceIdleTimeout=-1` でディストリビューションが動き続ける限り、VM もアイドルにならない。**passage の目的には `vmIdleTimeout` の設定は不要。** 逆に `vmIdleTimeout=-1` だけを設定しても、ディストリビューションは 15 秒で止まる（#13291 と #13033 の状況）。

### 1.4 #13416 / #13291 / 関連 issue の整理

| issue | 状態 | 設定 | 内容 | 結論 |
| --- | --- | --- | --- | --- |
| [#13291](https://github.com/microsoft/WSL/issues/13291)（WSL 2.5.9） | closed (completed) | `[wsl2] vmIdleTimeout=-1` のみ | Ollama や sshd が短時間で「suspend」される | OneBlue が `[general] instanceIdleTimeout=-1` も必要と回答 **[issue]**。報告者が解決を確認 **[報告]**。craigloewen-msft は「ドキュメント化すべき」とコメント **[issue]** |
| [#13033](https://github.com/microsoft/WSL/issues/13033)（2.5.7） | closed (completed) | `[wsl2] vmIdleTimeout=-1` + mirrored | 8 秒後に新しいシェルが遅くなる | OneBlue：「ログ上 VM は止まっていないが、ディストリビューションがタイムアウトしている」。`instanceIdleTimeout=-1` で解決 **[issue]** |
| [#13416](https://github.com/microsoft/WSL/issues/13416)（2.6.1〜2.7.3） | open（label: `systemd`） | `.wslconfig` なし、または `vmIdleTimeout=-1` | 最後の端末を閉じた 15〜20 秒後に、systemd サービスがあっても止まる | 原因の説明も WSL チームの回答もない。`instanceIdleTimeout=-1` を試した報告はない **[報告]**。15 秒という時間とソースから見て、既定の `instanceIdleTimeout=15000` どおりの動作と判断できる（2.5.10 で止まらなかったという最初の報告は、別のクライアントがインスタンスを保持していた可能性があるが、詳細は不明） |
| [#9968](https://github.com/microsoft/WSL/issues/9968)（1.2.0） | open | `vmIdleTimeout` | 同上 | 2026-04 にユーザー（chemwolf6922、WSL リポジトリの Contributor）が `[general] instanceIdleTimeout=-1` を案内 **[報告]** |
| [#9401](https://github.com/microsoft/WSL/issues/9401)、[#10138](https://github.com/microsoft/WSL/issues/10138) | closed | — | バックグラウンドで動き続けない | 2.5.4 の `instanceIdleTimeout` で解決扱い **[文書]**（リリースノート） |

- 「`instanceIdleTimeout=-1` を設定したのに止まる」という報告は、検索した範囲（microsoft/WSL の issue 本文とコメント）では見つからなかった。ただし網羅できたとは言えない。**[未確認]**

### 1.5 `-1` にしても止まる場合

ソースから読み取れる、`instanceIdleTimeout=-1` でもゲストが止まる経路：

1. **`wsl --shutdown` / `wsl --terminate <distro>`**：強制終了。**[文書]** wsl-config の「8 second rule」節
2. **Windows からのサインアウト**：サービスは `WTS_SESSION_LOGOFF` を受けると、そのセッションのユーザーセッション（VM を含む）を終了する。**[ソース]** [`src/windows/service/exe/ServiceMain.cpp`](https://github.com/microsoft/WSL/blob/42efa52b4cb21c10c9fb05b9e26d53d72e80c2d0/src/windows/service/exe/ServiceMain.cpp)（`OnSessionChanged`）、[`LxssUserSessionFactory.cpp`](https://github.com/microsoft/WSL/blob/42efa52b4cb21c10c9fb05b9e26d53d72e80c2d0/src/windows/service/exe/LxssUserSessionFactory.cpp)（`TerminateSession`）
   - ユーザーセッションはユーザー SID ごとに 1 つで、最初にインスタンスを作ったプロセスのセッション ID で記録される。セッション 0 から作られたもの（「ユーザーがログオンしているかどうかにかかわらず実行」のタスクなど）は、サインアウトでは終了せず、サービス停止時だけ終了する、とコメントにある。**[ソース]** 同ファイル（`TerminateSession` のコメント、`CreateInstanceForCurrentUser`）
3. **WSL サービスの停止**：WSL の更新（MSI のインストール）時は `WSLService` を止める（`ServiceControl Stop="both"`）。サービス停止時には全ユーザーセッションを終了する（`ClearSessionsAndBlockNewInstances`）。**[ソース]** [`msipackage/package.wix.in`](https://github.com/microsoft/WSL/blob/42efa52b4cb21c10c9fb05b9e26d53d72e80c2d0/msipackage/package.wix.in)（L295）、`ServiceMain.cpp`（`ServiceStopped`）
   - サービス自身は更新があるかを 24 時間ごとに確認し、通知を出すだけ（自動では入れない）。**[ソース]** `ServiceMain.cpp`（`StartCheckingForUpdates`、`CheckForUpdates`）。Microsoft Store など、別経路で自動更新されるかは **[未確認]**
4. **Windows の再起動、シャットダウン**：VM ごと終わる。
5. **スリープ、休止状態**：WSL のサービスに電源イベントを扱うコードは見当たらない（`SERVICE_CONTROL_POWEREVENT` / `PBT_APM*` なし）。VM は Hyper-V 側で一時停止し、復帰後に再開すると考えられるが、passage が復帰後もそのまま応答するかは **[未確認]**

いずれの場合も、止まったあとは誰かが再び `wsl.exe` を実行するまでゲストは起きない。

### 1.6 副作用

- `.wslconfig` は全ディストリビューション共通。passage 以外のディストリビューションも、一度起動すると止まらなくなる。**[文書]** wsl-config（`.wslconfig` は全 WSL 2 ディストリビューションに適用）
- VM も止まらないので、Windows 側のメモリ（`vmmem` / `VmmemWSL`）を使い続ける。キャッシュの解放は `[experimental] autoMemoryReclaim`（既定 `dropCache`）に任せることになる。**[文書]** 同ページ

## 2. Windows ログオン時にゲストを起こす方法

前提：`instanceIdleTimeout=-1` を設定すれば、`wsl.exe` で**一度起動するだけ**でゲストは動き続ける（1.3 のとおり、クライアントプロセスの登録そのものが行われない）。設定しない場合は、`wsl.exe` プロセスを**常駐させ続ける**必要がある。この差で各方式の評価が変わる。

ゲスト側に `[boot] systemd=true` と `loginctl enable-linger` があれば、ゲストの起動だけで systemd がユーザーマネージャを立ち上げる。`default.target` に `enable` したユーザーサービス（passage）も起動する。**[文書]** [loginctl(1)](https://www.freedesktop.org/software/systemd/man/latest/loginctl.html)（#4 の調査も参照）。起動に使うコマンドが passage を直接起動する必要はない。

### 2.1 `wsl.exe` と `wslg.exe`

- `wsl.exe` はコンソールアプリ。タスクスケジューラやスタートアップから起動すると、コンソールウィンドウ（Windows 11 で既定ターミナルが Windows Terminal なら、そのウィンドウ）が開く。**[未確認]**（Windows の一般的な挙動。WSL の文書に記載はない）
- **`wslg.exe`** は WSL パッケージに同梱され、`C:\Program Files\WSL\wslg.exe` に入る。**Windows サブシステム（`add_executable(wslg WIN32 ...)`、`wWinMain`）でビルドされるので、コンソールウィンドウを作らない。** エラー時は親コンソールに書き、なければメッセージボックスを出す。**[ソース]** [`src/windows/wslg/CMakeLists.txt`](https://github.com/microsoft/WSL/blob/42efa52b4cb21c10c9fb05b9e26d53d72e80c2d0/src/windows/wslg/CMakeLists.txt)、[`src/windows/wslg/main.cpp`](https://github.com/microsoft/WSL/blob/42efa52b4cb21c10c9fb05b9e26d53d72e80c2d0/src/windows/wslg/main.cpp)、`msipackage/package.wix.in`（INSTALLDIR = `ProgramFiles64Folder\WSL`）、`src/windows/common/WslClient.cpp`（L1976–1984）
  - 受け付けるオプションは `-d`/`--distribution`、`--shell-type {login|none|standard}`、`-u`/`--user`、`--cd`、`--`。コマンドラインは必須。`--exec` はないので、シェルを経由しないなら `--shell-type none` を使う。既定ではログインシェル経由で実行する。**[ソース]** `WslClient.cpp`（`WslgMain`、L1393–1491）
  - `wsl.exe` と同じく、Linux 側のプロセスが終わるまで待って終了コードを返す（`service.LaunchProcess(...)` の戻り値が exitCode）。**[ソース]** `WslClient.cpp`（`LaunchProcess`、L663–700）。つまり `sleep infinity` を渡せば、`wslg.exe` 自身が常駐クライアントになる。
  - `wslg.exe` には App Execution Alias がない（MSIX 内では使えない）。フルパスで指定する。**[ソース]** `WslClient.cpp`（L1396）

### 2.2 方式ごとの比較

| 方式 | ゲストを保つ仕組み | ウィンドウ | 管理者権限 | 止まったあとの再起動 | 備考 |
| --- | --- | --- | --- | --- | --- |
| **A. `-1` + ログオン時に 1 回だけ起動**（タスクスケジューラ / スタートアップ） | `instanceIdleTimeout=-1` | `wslg.exe` なら出ない | スタートアップフォルダなら不要。タスクは **[未確認]** | 次のログオンまで起きない（タスクにトリガーを足せば補える） | 最小構成。プロセスが常駐しない |
| **B. `wsl.exe -d <distro> --exec sleep infinity` を常駐**（タスクスケジューラ） | `wsl.exe` プロセスがクライアントとして登録され続ける | `wsl.exe` だと出る。`wslg.exe` で回避 | 同上 | `wsl --shutdown` などで `sleep` が死ぬとタスクも終わる。再試行設定は **[未確認]** | タスクの既定の実行時間制限 72 時間で止められる **[文書]**。バッテリー時の既定の制約も外す必要がある |
| **C. スタートアップフォルダのショートカット** | A か B と同じ（何を起動するか次第） | `wslg.exe` なら出ない | 不要（`shell:startup` はユーザーごと） | 次のログオンまで起きない | 「設定 → アプリ → スタートアップ」から利用者が無効にできる **[未確認]** |
| **D. Windows Terminal の `startOnUserLogin`** | 開いたタブの `wsl.exe` | **ウィンドウが開く** | 不要 | タブを閉じると 15 秒後に止まる（`-1` なしの場合） | **[文書]** <https://learn.microsoft.com/en-us/windows/terminal/customize-settings/startup>。常駐用途には不向き |
| **E. 常駐ツール**（keepwsl、WSL-Keep-Alive など） | トレイアプリが `sleep infinity` を保持 | トレイのみ | 不要と思われる | ツール次第 | サードパーティ。WSL-Keep-Alive の作者は「趣味のプロジェクトで MS の見解ではない」と明記 **[報告]** |
| **F. ゲスト内の systemd サービスから interop で `wsl.exe sleep infinity`** | interop で起動した Windows 側の `wsl.exe` がクライアントになる | なし | 不要 | 最初の起動はほかの手段が要る | #13416 で動作報告あり **[報告]**。循環的で分かりにくいので非推奨 |

補足：

- **タスクスケジューラの既定値**：実行時間の制限は 72 時間（「By default, a task will be stopped 72 hours after it starts to run」）。**[文書]** <https://learn.microsoft.com/en-us/windows/win32/taskschd/tasksettings>。`DisallowStartIfOnBatteries` の既定は `True`（バッテリー駆動中は起動しない）。**[文書]** <https://learn.microsoft.com/en-us/windows/win32/taskschd/tasksettings-disallowstartifonbatteries>。PowerShell では `New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ...` で変える。**[文書]** <https://learn.microsoft.com/en-us/powershell/module/scheduledtasks/new-scheduledtasksettingsset>
  - 方式 B（常駐）ではこれらがすべて効いてくる。方式 A（一度だけ起動してすぐ終わる）では、ログオン時にバッテリー駆動だと起動されない点だけが問題になる。
- **タスクの「表示しない（Hidden）」はウィンドウを隠す設定ではない。** タスクスケジューラの UI に表示しないだけ。**[文書]** TaskSettings.Hidden（同上）
- **タスクの作成に管理者権限が要るか**：schtasks のリファレンスには「Only Administrators can schedule tasks, regardless of the value of the /ru parameter」とある（System アカウントで実行する節）。ただし、標準ユーザーが自分のアカウントでログオン時トリガーのタスクを作れるかは、文書からは判断できない。**[未確認]** <https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/schtasks-create>
- **スケジュールタスクから起動したインスタンス**：WSL のソースに、「スケジュールタスクのように、対話セッションとは別のコンテキスト（別の認証 ID）でインスタンスが作られることがある」ので、既存インスタンスでも Plan 9 の接続先を呼び出しユーザーに登録し直す、というコメントがある。WSL チームがタスク経由の起動を想定していることがわかる。**[ソース]** `LxssUserSession.cpp`（L2943–2950）
- **「ユーザーがログオンしているかどうかにかかわらず実行」**（セッション 0、`/np` ならローカルリソースのみ）は、パスワードの保存が要り、ネットワーク資源にも制約がある。**[文書]** schtasks `/np`。サインアウト後もゲストが残る（1.5 の 2）という利点はあるが、passage はホストのブラウザから使うので、ログオン中に動いていれば足りる。採用しない。
- **スリープ、休止状態からの復帰**：どの方式でも、VM は止まらずに一時停止するだけと考えられる（1.5 の 5）。そのため、復帰時にあらためて起動する必要はない見込み。**[未確認]**
- **高速スタートアップ**：シャットダウン時にユーザーはサインアウトするので、WSL のユーザーセッションは終わる。次回の起動はログオン扱いになり、ログオントリガーが動く見込み。**[未確認]**

## 3. passage への推奨

### 3.1 推奨手順（WSL2）

**Windows 側（利用者が行う）**

1. WSL を 2.5.4 以降にする（`wsl --version` で確認、`wsl --update`）。
2. `%UserProfile%\.wslconfig` に次を追記する（WSL Settings の「Distribution idle timeout」に `-1` を入れても同じ）。

   ```ini
   [general]
   instanceIdleTimeout=-1
   ```

   `[wsl2]` の下に書かないこと。`vmIdleTimeout` は変えなくてよい。
3. `wsl --shutdown` で VM を止めて、設定を反映させる。
4. ログオン時にゲストを起こす。次のどちらか。
   - **スタートアップフォルダ（管理者権限不要、もっとも単純）**：`shell:startup` に次のショートカットを置く。
     - リンク先：`"C:\Program Files\WSL\wslg.exe" -d Ubuntu --shell-type none -- /bin/true`
   - **タスクスケジューラ（`wsl --shutdown` や更新のあとも自動で起こしたい場合）**：トリガーは「ログオン時（自分のユーザー）」。操作は上と同じ `wslg.exe`。設定は「バッテリー駆動時も開始」「バッテリーに切り替わっても停止しない」。必要に応じて、定期実行のトリガー（例：1 時間ごと）を足す。`/bin/true` はゲストが動いていれば何もせずに終わるので、繰り返し実行しても害はない（冪等）。

**ゲスト側（passage のインストールスクリプトで扱える）**

5. `/etc/wsl.conf` に `[boot] systemd=true` があることを確認する（なければ追記して 3 をやり直す）。
6. `loginctl enable-linger "$USER"` を実行する。
7. `systemctl --user enable --now passage.service` を実行する（`WantedBy=default.target`）。

**判断の理由**

- 常駐プロセス方式（B）は、タスクの 72 時間制限、バッテリー条件、`wsl --shutdown` 後の再起動、ウィンドウなど、気にすべき点が多い。`-1` + 一度だけ起動（A）なら、Windows 側に常駐するプロセスがなく、手順も短い。
- `wslg.exe` を使えば、コンソールウィンドウが一瞬開くことも避けられる（ソース上）。
- ゲストの外（Windows 側）の設定は、ゲスト内のスクリプトからは安全に書き換えにくい。`.wslconfig` は全ディストリビューションに影響するし、`/mnt/c` 経由で書けても反映には `wsl --shutdown` が要る。そのため、インストール手順では **利用者に案内して確認させる**のがよい。passage 側では、起動時やヘルスチェックで「`instanceIdleTimeout` が `-1` でない」ことを検出して警告するのが現実的（検出方法は下の未確認事項 6）。
- 方式 A の弱点は「`wsl --shutdown` や WSL の更新のあと、次のログオンまで passage が止まる」こと。タスクスケジューラに定期トリガーを足すか、利用者が `wsl.exe` を一度実行すれば復旧する。

### 3.2 #13416 の結末次第で変わること

- #13416 は open のまま。将来、WSL 側が「Linux 側にサービスが動いていればアイドルとみなさない」ように変わる可能性はあるが、2026-11 時点のソースにその実装はない。**[ソース]**
- 逆に `instanceIdleTimeout` が廃止、変更されたら、手順を見直す必要がある。リリースノートを追う。

## 未確認事項（実機で確認すべきもの）

Windows 11 + WSL 3.0.x（安定版）+ Ubuntu で確認する。各手順の前に `wsl --version` を記録しておく。

1. **`instanceIdleTimeout=-1` で端末を閉じても passage が生き続けるか**
   - `.wslconfig` に設定 → `wsl --shutdown` → `wsl -d Ubuntu` で入り、`systemctl --user status passage` が active なことを確認 → 端末を閉じる → 1 分以上待つ → PowerShell で `wsl -l -v`（`Running` のままか）、ブラウザで `http://localhost:<port>` が応答するか。30 分後にもう一度確認する。
   - 対照実験として、設定を消した状態で 15 秒後に `Stopped` になることも確認する。
2. **`[wsl2]` の下に書いた場合に警告が出て効かないか**：`wsl.exe` の起動時に「Unknown key 'wsl2.instanceIdleTimeout'」の警告が出るか。
3. **`wslg.exe -d Ubuntu --shell-type none -- /bin/true` でウィンドウが出ないか**：スタートアップフォルダとタスクスケジューラの両方で試し、ログオン後に `wsl -l -v` が `Running`、passage が応答するか。`wsl.exe -d Ubuntu --exec /bin/true` の場合にウィンドウが一瞬出るかも比べる。
4. **標準ユーザーがログオン時トリガーのタスクを作れるか**：管理者でないアカウントで `Register-ScheduledTask -TaskName passage-wsl -Trigger (New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME) -Action (New-ScheduledTaskAction -Execute 'C:\Program Files\WSL\wslg.exe' -Argument '-d Ubuntu --shell-type none -- /bin/true') -Settings (New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries)` を実行し、成功するか、次のログオンで動くか。
5. **スリープ、休止状態、高速スタートアップ後の挙動**：それぞれのあとに `wsl -l -v` と `curl http://localhost:<port>` を実行する。ゲスト内の時刻のずれ（`date` と Windows の時刻の比較）も見る（セッションの有効期限に関わる）。
6. **ゲスト内から `instanceIdleTimeout` の設定を検出できるか**：`/mnt/c/Users/<user>/.wslconfig` を読めるか（`wslpath "$(cmd.exe /c 'echo %UserProfile%' 2>/dev/null | tr -d '\r')"`）。そもそも設定ではなく実効値を知る手段がないか。
7. **`wsl --update` 後の挙動**：更新でゲストが止まること、止まったあとは定期トリガー（またはタスクの手動実行）で復旧することを確認する。Microsoft Store などで WSL が自動更新されるかも確認する。
8. **Windows のサインアウトと再ログオン**：サインアウトでゲストが止まり、再ログオンのトリガーで passage が戻るか。

## 出典

- Microsoft Learn — Advanced settings configuration in WSL（wsl.conf / .wslconfig）: <https://learn.microsoft.com/en-us/windows/wsl/wsl-config>
- Microsoft Learn — Windows Terminal Startup Settings: <https://learn.microsoft.com/en-us/windows/terminal/customize-settings/startup>
- Microsoft Learn — schtasks create: <https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/schtasks-create>
- Microsoft Learn — TaskSettings object / DisallowStartIfOnBatteries: <https://learn.microsoft.com/en-us/windows/win32/taskschd/tasksettings>、<https://learn.microsoft.com/en-us/windows/win32/taskschd/tasksettings-disallowstartifonbatteries>
- Microsoft Learn — New-ScheduledTaskSettingsSet: <https://learn.microsoft.com/en-us/powershell/module/scheduledtasks/new-scheduledtasksettingsset>
- microsoft/WSL リリース: [2.5.4](https://github.com/microsoft/WSL/releases/tag/2.5.4)、[一覧](https://github.com/microsoft/WSL/releases)
- microsoft/WSL ソース（コミット `42efa52`）:
  - `src/windows/common/WslCoreConfig.h` / `WslCoreConfig.cpp`（設定キーと既定値）
  - `src/shared/configfile/configfile.cpp`（未知のキーの警告）
  - `src/windows/service/exe/LxssUserSession.cpp`（インスタンス作成、ライフタイム登録、`_VmCheckIdle` / `_VmIsIdle`、`_TerminateInstanceInternal`）
  - `src/windows/service/exe/Lifetime.cpp`（クライアントプロセス終了とタイマー）
  - `src/windows/service/exe/WslCoreVm.cpp`（WSL2 インスタンスへの `InstanceIdleTimeout` の受け渡し）
  - `src/windows/service/exe/ServiceMain.cpp` / `LxssUserSessionFactory.cpp`（サインアウトとサービス停止時の終了、更新の確認）
  - `src/linux/init/init.cpp`（停止要求時の `systemctl poweroff`）
  - `src/windows/wslg/*`、`src/windows/common/WslClient.cpp`（`wslg.exe`）
  - `msipackage/package.wix.in`（インストール先、更新時のサービス停止）
  - `localization/strings/en-US/Resources.resw`（WSL Settings の文言）
- microsoft/WSL issues: [#13416](https://github.com/microsoft/WSL/issues/13416)、[#13291](https://github.com/microsoft/WSL/issues/13291)、[#13033](https://github.com/microsoft/WSL/issues/13033)、[#10138](https://github.com/microsoft/WSL/issues/10138)、[#9968](https://github.com/microsoft/WSL/issues/9968)、[#9401](https://github.com/microsoft/WSL/issues/9401)
- サードパーティ（参考）: [chemwolf6922/WSL-Keep-Alive](https://github.com/chemwolf6922/WSL-Keep-Alive)、[gardengim/keepwsl](https://github.com/gardengim/keepwsl)
- systemd: [loginctl(1)](https://www.freedesktop.org/software/systemd/man/latest/loginctl.html)
