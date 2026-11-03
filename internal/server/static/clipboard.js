// 詳細のコピーとクリップボードの自動クリア、「表示」を伏せ字に戻す処理（素の JS）。
// 自動テストの対象外で、手動で確かめる（#30）。
//
// - [data-copy-secret] のボタンは、同じ行（.password-row の input.secret、.field の dd）の値をコピーする。
//   値はボタンの属性に複製しない（平文は DOM の 1 か所だけ）。
// - コピーしてから 45 秒後にクリップボードを空文字で上書きしようとする。残り秒数を出す。
//   フォーカスがないなどで失敗したら、次にフォーカスが戻ったときに 1 回だけやり直す。
// - 右ペインを差し替えるとき（別のエントリに切り替えるなど）と、タブが非表示になったときは、
//   「表示」中の input を伏せ字に戻す。
(function () {
  "use strict";

  var CLEAR_AFTER_SECONDS = 45;

  var deadline = 0; // クリアする時刻（ms）。0 なら予定なし
  var ticker = null; // 残り秒数を更新する setInterval
  var retryOnFocus = false; // クリアに失敗し、フォーカスが戻るのを待っている

  // 画面の右下に出す状態表示。右ペインが差し替わっても残るように body に置く。
  function statusElement() {
    var el = document.getElementById("clipboard-status");
    if (!el) {
      el = document.createElement("p");
      el.id = "clipboard-status";
      el.className = "clipboard-status";
      el.setAttribute("role", "status");
      el.setAttribute("aria-live", "polite");
      document.body.appendChild(el);
    }
    return el;
  }

  function showStatus(text) {
    var el = statusElement();
    el.textContent = text;
    el.hidden = text === "";
  }

  function stopTicker() {
    if (ticker !== null) {
      clearInterval(ticker);
      ticker = null;
    }
  }

  function secretValue(button) {
    var row = button.closest(".password-row, .field");
    if (!row) return null;
    var el = row.querySelector("input.secret, dd");
    if (!el) return null;
    return el.tagName === "INPUT" ? el.value : el.textContent;
  }

  function copySecret(button) {
    var value = secretValue(button);
    if (value === null || !navigator.clipboard) {
      showStatus("コピーできませんでした。");
      return;
    }
    navigator.clipboard.writeText(value).then(function () {
      scheduleClear();
    }, function () {
      showStatus("コピーできませんでした。");
    });
  }

  // 新しくコピーしたら、45 秒を数え直す。前回のやり直し待ちも、このクリアに含まれる。
  function scheduleClear() {
    retryOnFocus = false;
    deadline = Date.now() + CLEAR_AFTER_SECONDS * 1000;
    stopTicker();
    tick();
    ticker = setInterval(tick, 1000);
  }

  // バックグラウンドのタブではタイマーが間引かれるので、残り秒数は時刻から計算する。
  function tick() {
    var remaining = Math.ceil((deadline - Date.now()) / 1000);
    if (remaining > 0) {
      showStatus("コピーしました。" + remaining + " 秒後にクリップボードを空にします。");
      return;
    }
    stopTicker();
    deadline = 0;
    clearClipboard(true);
  }

  // canRetry が true なら、失敗したときにフォーカスが戻るのを 1 回だけ待つ。
  function clearClipboard(canRetry) {
    var fail = function () {
      if (canRetry) {
        retryOnFocus = true;
        showStatus("クリップボードを空にできませんでした。このページにフォーカスが戻ったら、もう一度試します。");
      } else {
        showStatus("クリップボードを空にできませんでした。必要なら手動で消してください。");
      }
    };
    if (!navigator.clipboard) {
      fail();
      return;
    }
    navigator.clipboard.writeText("").then(function () {
      showStatus("クリップボードを空にしました。");
    }, fail);
  }

  window.addEventListener("focus", function () {
    if (!retryOnFocus) return;
    retryOnFocus = false;
    clearClipboard(false);
  });

  // 「表示」中の input を伏せ字に戻す。ボタンの表記は passui.js の toggleReveal と揃える。
  function maskAll() {
    var buttons = document.querySelectorAll('[data-reveal][aria-pressed="true"]');
    Array.prototype.forEach.call(buttons, function (button) {
      var input = document.querySelector(button.dataset.reveal);
      if (input) input.type = "password";
      button.setAttribute("aria-pressed", "false");
      button.textContent = "表示";
    });
  }

  document.addEventListener("visibilitychange", function () {
    if (document.visibilityState === "hidden") maskAll();
  });

  // 右ペインを差し替えるリクエストを送る時点で伏せる（応答が遅くても見せたままにしない）。
  document.addEventListener("htmx:beforeRequest", function (event) {
    var target = event.detail && event.detail.target;
    if (target && target.id === "right-pane") maskAll();
  });

  document.addEventListener("click", function (event) {
    var target = event.target;
    if (!(target instanceof Element)) return;
    var button = target.closest("[data-copy-secret]");
    if (button) copySecret(button);
  });
})();
