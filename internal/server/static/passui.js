// passui のブラウザ側の処理（素の JS）。自動テストの対象外で、手動で確かめる。
// 右ペインの中身は htmx で差し替わるので、イベントは document で受ける（委譲）。
(function () {
  "use strict";

  // 「表示」: data-reveal="<セレクタ>" の input を、伏せ字と平文で切り替える。
  // 伏せ字に戻す処理（エントリの切り替え、タブの非表示）は clipboard.js にある。
  function toggleReveal(button) {
    var input = document.querySelector(button.dataset.reveal);
    if (!input) return;
    var reveal = input.type === "password";
    input.type = reveal ? "text" : "password";
    button.setAttribute("aria-pressed", reveal ? "true" : "false");
    setRevealLabel(button, reveal ? "隠す" : "表示");
  }

  // 表示ボタンはアイコンだけなので、ラベルは aria-label と title に持つ。
  // アイコンは aria-pressed から CSS が選ぶ。伏せ字に戻すときは clipboard.js の maskAll も同じことをする。
  function setRevealLabel(button, label) {
    button.setAttribute("aria-label", label);
    button.setAttribute("title", label);
  }

  // ツリー（または検索結果）で、右ペインに出しているエントリに aria-current を付ける。
  // 右ペインの直下の [data-entry]（詳細、ロック中、編集、削除の確認）がそのエントリ。
  function markSelected() {
    var shown = document.querySelector("#right-pane > [data-entry]");
    var name = shown ? shown.getAttribute("data-entry") : null;
    var links = document.querySelectorAll("#tree .entry > .entry-link");
    Array.prototype.forEach.call(links, function (link) {
      if (name !== null && link.parentElement.getAttribute("data-entry") === name) {
        link.setAttribute("aria-current", "true");
      } else {
        link.removeAttribute("aria-current");
      }
    });
  }

  // 右ペインとツリーのどちらが差し替わっても付け直す。
  document.addEventListener("htmx:afterSettle", markSelected);
  document.addEventListener("DOMContentLoaded", markSelected);

  // 「コピー」: data-copy="<セレクタ>" の要素の文字列をクリップボードに入れる。
  // シークレットではない文字列（解錠のワンライナーなど）に使う。
  // パスワードや値のコピー（クリップボードのクリア付き）は clipboard.js の data-copy-secret。
  function copyText(button) {
    var el = document.querySelector(button.dataset.copy);
    if (!el || !navigator.clipboard) return;
    var text = "value" in el && el.tagName === "INPUT" ? el.value : el.textContent;
    navigator.clipboard.writeText(text).then(function () {
      var label = button.textContent;
      button.textContent = "コピーしました";
      setTimeout(function () { button.textContent = label; }, 1500);
    });
  }

  document.addEventListener("click", function (event) {
    var target = event.target;
    if (!(target instanceof Element)) return;
    var reveal = target.closest("[data-reveal]");
    if (reveal) {
      toggleReveal(reveal);
      return;
    }
    var copy = target.closest("[data-copy]");
    if (copy) {
      copyText(copy);
    }
  });
})();
