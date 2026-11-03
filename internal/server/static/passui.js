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
    button.textContent = reveal ? "隠す" : "表示";
  }

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
