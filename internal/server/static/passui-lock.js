// ロックとログアウトの後に平文を画面に残さないための処理（素の JS）。
// 自動テストの対象外で、手動で確かめる（docs/manual-checklist.md）。
(function () {
  "use strict";

  // 右ペイン（詳細、編集フォームの入力など）を確認なしで空にする。
  function clearRightPane() {
    var pane = document.getElementById("right-pane");
    if (pane) pane.textContent = "";
  }

  // ページを離れるとき（ログアウトの POST を含む）に右ペインを空にしておく。
  // bfcache に平文入りの DOM が残らないようにするため。
  window.addEventListener("pagehide", clearRightPane);

  // bfcache から戻ったとき（戻るボタン）は、古い DOM を見せずに読み直す。
  // ログアウト後ならログインの案内に、ロック後ならロック中の状態になる。
  window.addEventListener("pageshow", function (event) {
    if (event.persisted) {
      clearRightPane();
      location.reload();
    }
  });

  // ログアウトのフォームを送るときも、遷移を待たずに右ペインを空にする。
  // ロック ボタンは htmx で右ペインを差し替える（lock_button.html）。
  document.addEventListener("submit", function (event) {
    var form = event.target;
    if (form instanceof HTMLFormElement && form.getAttribute("action") === "/logout") {
      clearRightPane();
    }
  });
})();
