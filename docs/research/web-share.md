# ホストのブラウザで Web Share API によるファイル共有が使えるか

調査チケット: [#43](https://github.com/backpaper0/passui/issues/43)（マップ: [#42](https://github.com/backpaper0/passui/issues/42)）

## 問い

passui（`http://passui.localhost:3560`）の画面から `navigator.share({ files })` で**バックアップ**のファイルを OS の共有シートに渡し、ホスト側の OneDrive（公用: macOS）／Google Drive（私用: Windows）へ保存できるか。セキュアコンテキスト、共有できるファイル型、ユーザーアクティベーション、フォールバックも調べる。

## 結論（要約）

- **`http://passui.localhost` はセキュアコンテキスト**として扱われる（W3C Secure Contexts、Chromium・WebKit のソースで確認）。Web Share を使ううえで HTTPS は要らない。
- **Chrome / Edge（Chromium）は zip・tar・gz を共有できない。** ブラウザ側の許可リストにあるのは画像・音声・動画・`pdf`・`txt`/`csv`/`html`/`css` だけで、`application/zip`、`application/gzip`、`application/x-tar`、`application/octet-stream` は入っていない。リスト外のファイルは `share()` が `NotAllowedError` で拒否される。しかも `canShare()` はレンダラー側でファイル型を見ないので、**`canShare()` が true でも `share()` は失敗しうる**。
- **Safari（WebKit）は WebCore にファイル型の制限がない**（`Navigator.cpp` で確認。ファイルを読む `ShareDataReader` は未確認）。
- **共有シートに OneDrive / Google Drive が出るかは一次情報では確認できなかった。実機確認が要る。** 構造上は出ない見込みが強い。
  - macOS: 共有シートに出るのは `com.apple.share-services` の共有拡張を持つアプリ。OneDrive for Mac にあると確認できたのは Finder 拡張（右クリックの「共有」）だけで、共有シート用の拡張は確認できなかった。
  - Windows: 共有ターゲットになれるのはパッケージ ID（MSIX など）を持つアプリだけ。Google Drive for desktop は通常の Win32 インストーラーで、共有ターゲットとして登録するという一次情報は見つからなかった。
- **ユーザーアクティベーションの有効期間は 5 秒**（Chromium `kActivationLifespan`、WebKit `defaultTransientActivationDuration`）。`share()` を呼ぶとアクティベーションは消費される。クリック後に `fetch` でバックアップを取ってくる間に 5 秒を過ぎると `NotAllowedError` になる。確実にするなら「作る」と「共有する」の 2 クリックに分ける。
- Chromium には件数 10・合計 50 MiB の上限がある。
- **フォールバックはダウンロード**（`Content-Disposition: attachment`、または Blob + `<a download>`）が全ブラウザで確実に使える。Chrome / Edge なら `showSaveFilePicker()` で、同期フォルダ（OneDrive・Google Drive for desktop のローカルフォルダ）へ直接保存させる手もある（Safari は非対応）。
- **ファイル形式の判断への影響**: Chromium で共有シートを使うには、ファイルを許可リストの型（実質 `.txt` / `text/plain`）にするしかない。tar.gz を `.txt` と名付けて渡すのは技術的には通るが、偽装になる。私用（Windows の Edge / Chrome）では共有シートがそもそも Google Drive に届かない可能性が高いので、**私用はダウンロード（または保存ダイアログ）を主経路にする**のが現実的。

## 詳細

### 1. セキュアコンテキスト（`http://*.localhost`）

- Web Share 仕様では `share()` と `canShare()` が `[SecureContext]`。[W3C Web Share](https://w3c.github.io/web-share/)
- Secure Contexts 仕様の「Is origin potentially trustworthy?」では、UA が "let-localhost-be-localhost"（localhost 名をループバックにだけ解決する）に従う場合、ホストが `localhost` か、`.localhost` で終わるなら Potentially Trustworthy。[W3C Secure Contexts §3.1](https://w3c.github.io/webappsec-secure-contexts/)
- Chromium: `net::IsLocalHostname()` は `localhost` と `.localhost` で終わるホストを localhost として扱う。[net/base/url_util.cc](https://chromium.googlesource.com/chromium/src/+/main/net/base/url_util.cc)
- WebKit: `SecurityOrigin::isLocalhostAddress()` は `host == "localhost" || host.endsWith(".localhost")`。これを `shouldTreatAsPotentiallyTrustworthy()` が使う。[Source/WebCore/page/SecurityOrigin.cpp](https://github.com/WebKit/WebKit/blob/main/Source/WebCore/page/SecurityOrigin.cpp)
- MDN も、`.localhost` で終わるホストを potentially trustworthy と明記している。[MDN Secure contexts](https://developer.mozilla.org/en-US/docs/Web/Security/Secure_Contexts)

→ Safari・Chrome・Edge のどれでも `http://passui.localhost:3560` はセキュアコンテキスト。ポート番号は関係ない。

### 2. ブラウザごとの対応状況（デスクトップ）

MDN browser-compat-data（`api/Navigator.json`）より。[BCD](https://github.com/mdn/browser-compat-data/blob/main/api/Navigator.json)

| ブラウザ | `share()` | `files` |
| --- | --- | --- |
| Chrome | 128 から全デスクトップ（89〜127 は「ChromeOS と Windows だけ」の部分実装。macOS は 128 から） | 89 |
| Edge | 93（81〜92 は Windows だけ） | 81 |
| Safari（macOS） | 12.1 | 14 |
| Firefox | 71（`dom.webshare.enabled` フラグが要る） | 非対応 |

- Chrome のブラウザ側の振り分け: Windows は `ShareOperation`（Windows の共有 UI）、macOS は `SharingServiceOperation`（`NSSharingServicePicker`）。Windows では「タブがアクティブで表示中でなければ共有できない」チェックがある。[chrome/browser/webshare/share_service_impl.cc](https://chromium.googlesource.com/chromium/src/+/main/chrome/browser/webshare/share_service_impl.cc)

### 3. 共有できるファイル型

#### Chromium（Chrome / Edge）

`share_service_impl.cc` の `IsDangerousFilename()` と `IsDangerousMimeType()` は、名前に反して**許可リスト**になっている。拡張子と MIME 型の両方がリストに入っていないと拒否される。[share_service_impl.cc](https://chromium.googlesource.com/chromium/src/+/main/chrome/browser/webshare/share_service_impl.cc)

- 許可される拡張子: `.avif .bmp .css .csv .ehtml .flac .gif .htm .html .ico .jfif .jpeg .jpg .m4a .m4v .mp3 .mp4 .mpeg .mpg .oga .ogg .ogm .ogv .opus .pdf .pjp .pjpeg .png .shtm .shtml .svg .svgz .text .tif .tiff .txt .wav .weba .webm .webp .xbm`
- 許可される MIME 型: `application/pdf`、`audio/*`（flac, mp3, mpeg, ogg, wav, webm, x-m4a）、`image/*`（avif, bmp, gif, jpeg, png, svg+xml, tiff, webp, x-icon, x-ms-bmp, x-xbitmap）、`text/comma-separated-values`、`text/css`、`text/csv`、`text/html`、`text/plain`、`video/*`（mp4, mpeg, ogg, webm）
- **`.zip` `.tar` `.gz` `.tgz` `.gpg` `.asc` は入っていない。`application/zip`・`application/gzip`・`application/x-tar`・`application/octet-stream`・`application/pgp-*` も入っていない。**
- 上限: `kMaxSharedFileCount = 10`、`kMaxSharedFileBytes = 50 * 1024 * 1024`（合計 50 MiB）。[share_service_impl.h](https://chromium.googlesource.com/chromium/src/+/main/chrome/browser/webshare/share_service_impl.h)
- レンダラー側（`navigator_share.cc`）がファイルについて確かめるのは、名前の安全性（`SafeBaseName`）と件数・サイズだけ。`canShare()` は型を見ない。つまり **`canShare({files:[zip]})` は true を返し、`share()` を呼んで初めてブラウザ側で `NotAllowedError`（"Permission denied"）になる**。[third_party/blink/renderer/modules/webshare/navigator_share.cc](https://chromium.googlesource.com/chromium/src/+/main/third_party/blink/renderer/modules/webshare/navigator_share.cc)
- MDN の「たいてい共有できるファイル型」の一覧も、この許可リストと同じ内容（アーカイブなし）。[MDN Navigator.share](https://developer.mozilla.org/en-US/docs/Web/API/Navigator/share)

#### WebKit（Safari）

- `Navigator::canShare` / `share` が確かめるのは、`webShareFileAPIEnabled` 設定と、`files` が空でないことだけ。拡張子・MIME 型の制限はない。[Source/WebCore/page/Navigator.cpp](https://github.com/WebKit/WebKit/blob/main/Source/WebCore/page/Navigator.cpp)
- **未確認**: ファイルを読み込む `ShareDataReader` や UIProcess 側で型が弾かれないか。tar.gz を渡したとき、macOS の共有シートにどのサービスが出るか（サービスは受け取る型で絞り込まれる）。実機確認が要る。

### 4. 共有シートに OneDrive / Google Drive が出るか

**一次情報では確認できなかった。実機確認が要る。**

- macOS: Safari と Chrome（`SharingServiceOperation`）は `NSSharingServicePicker` を使う。このピッカーに出るのは、システムのサービス（AirDrop、メール、メッセージ、メモなど）と、共有拡張（`NSExtensionPointIdentifier = com.apple.share-services`）を持つアプリ。[Apple NSSharingServicePicker](https://developer.apple.com/documentation/appkit/nssharingservicepicker)
  - OneDrive for Mac について確認できたのは **Finder 拡張**（Finder で右クリックしたときの「共有」）だけ。これは共有シートの拡張とは別物。OneDrive が共有シートのターゲットになるという Microsoft の一次文書は見つからなかった。
  - macOS の共有シートには「ファイルに保存」に当たる汎用のサービスがない（iOS の「ファイルに保存」に相当するものがない）。そのため、同期フォルダへ保存する経路は共有シートからは期待しにくい（未確認）。
- Windows: Chrome / Edge は Windows の共有 UI（DataTransferManager）を使う。共有ターゲットになれるのは**パッケージ ID を持つアプリ**（MSIX でパッケージしたもの、または外部の場所を持つパッケージ）で、マニフェストに Share Target 拡張を宣言したものだけ。[Microsoft Learn: Integrate packaged apps with Windows Share](https://learn.microsoft.com/windows/apps/develop/windows-integration/integrate-sharesheet-packaged)、[Integrate unpackaged apps with Windows Share](https://learn.microsoft.com/en-us/windows/apps/develop/windows-integration/integrate-sharesheet-unpackaged)
  - Google Drive for desktop が共有ターゲットとして登録されるという Google の一次文書は見つからなかった。Explorer の右クリックの「Google ドライブで共有」はあるが、Windows の共有 UI とは別物。
  - 加えて、Chromium の許可リストのせいで、Windows の共有 UI に zip・tar.gz を渡すこと自体ができない。

### 5. ユーザーアクティベーション

- 仕様: `share()` は呼ばれた時点で transient activation があるか確かめ、なければ `NotAllowedError`。確かめると同時に消費する。`canShare()` にはアクティベーションは要らない。[W3C Web Share](https://w3c.github.io/web-share/)
- 有効期間は Chromium・WebKit とも **5 秒**。
  - Chromium: `kActivationLifespan = base::Seconds(5)`。[user_activation_state.h](https://chromium.googlesource.com/chromium/src/+/main/third_party/blink/public/common/frame/user_activation_state.h)
  - WebKit: `defaultTransientActivationDuration { 5_s }`。`share()` は `window->consumeTransientActivation()` を呼ぶ。[LocalDOMWindow.cpp](https://github.com/WebKit/WebKit/blob/main/Source/WebCore/page/LocalDOMWindow.cpp)、[WebKit Bug 239832](https://bugs.webkit.org/show_bug.cgi?id=239832)
- つまり「クリック → `await fetch()` でサーバーにバックアップを作らせる → `share()`」は、**5 秒以内に終われば通る**（`await` そのものではアクティベーションは失われない）。サーバー側で gpg が遅い、ストアが大きい、などで 5 秒を超えると失敗する。
- 確実にする設計は 2 段階: ①「バックアップを作る」クリックで fetch して Blob（`File`）を作る、②「共有」ボタンを出し、そのクリックの中で同期的に `share()` を呼ぶ。
- Chromium（Android 以外）は、前の共有が終わっていないうちに次を呼ぶと `InvalidStateError` を返す。
- 戻り値の Promise は相手に渡した時点で resolve する（相手が保存に成功したかはわからない）。ユーザーが閉じたときは `AbortError`。

### 6. フォールバック

| 方式 | 対応ブラウザ | ファイル型の制限 | 保存先 | 備考 |
| --- | --- | --- | --- | --- |
| ダウンロード（サーバーから `Content-Disposition: attachment`、または Blob URL + `<a download>`） | 全て | なし | ダウンロードフォルダ（ブラウザの設定次第で毎回確認） | 最も確実。ユーザーはそのあと同期フォルダへ移すか、クラウドの Web 画面でアップロードする |
| `showSaveFilePicker({ suggestedName, types })` | Chrome / Edge（Safari・Firefox は非対応） | なし | ユーザーが選んだ場所。OneDrive / Google Drive for desktop の同期フォルダを選べる | セキュアコンテキストとアクティベーションが要る（`SecurityError`）。[MDN showSaveFilePicker](https://developer.mozilla.org/en-US/docs/Web/API/Window/showSaveFilePicker)。同期フォルダを選べるかは未確認（実機確認が要る） |
| `navigator.share` | Safari（型の制限なし）、Chrome / Edge（許可リストの型だけ） | 上記 | 共有シートのターゲット次第 | OneDrive / Google Drive が出るかは未確認 |

典型的な実装は「`navigator.canShare?.({files})` が true なら共有を試し、`NotAllowedError` か `AbortError` 以外の失敗、または非対応ならダウンロード」というもの。ただし Chromium では `canShare` が型を見ないので、zip・tar.gz だと `share()` が必ず `NotAllowedError` になる。これを「ユーザーのキャンセル」（`AbortError`）と区別して、ダウンロードに落とす必要がある。

## バックアップのファイル形式と画面の流れへの含意

- **形式**: 中身（ストアのディレクトリ＋鍵のエクスポート＋ownertrust）を 1 ファイルにするなら tar.gz か zip が自然だが、どちらも Chromium の共有シートには渡せない。共有シートを使うために `.txt` / `text/plain` に偽装するのは、受け取る側のアプリ（テキストとして扱うメモアプリなど）で壊れたり、混乱を招いたりするので勧めにくい。形式はアーカイブの実用性（復元のしやすさ）だけで選び、共有シートは「通る環境では通る」くらいに扱うのがよい。
- **画面の流れ**:
  - 主経路はダウンロード（Chrome / Edge では `showSaveFilePicker` も候補）。Safari だけ共有シートを試す価値がある（OneDrive が出るかは実機確認が要る）。
  - 共有を使うなら「作る」と「共有」の 2 クリックにして、5 秒の制限を避ける。
- **実機確認が要る項目**:
  1. macOS Safari / Chrome 128 以降で、tar.gz（または zip）を共有したときに共有シートに OneDrive が出るか。
  2. Windows 11 の Edge / Chrome の共有 UI に Google Drive for desktop が出るか（`.txt` で試す）。
  3. Chrome / Edge の `showSaveFilePicker` で、OneDrive（`~/Library/CloudStorage/OneDrive-*`）や Google Drive（`G:` など）の同期フォルダを保存先に選べるか。
  4. Safari で tar.gz を `share()` に渡したとき、`ShareDataReader` などで弾かれないか。
