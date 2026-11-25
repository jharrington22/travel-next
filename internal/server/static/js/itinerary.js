// Day switching, copy-link and a browser-local packing checklist.
(function () {
  "use strict";

  var tabs = document.querySelectorAll(".daytab");
  var panels = document.querySelectorAll("[data-daypanel]");
  tabs.forEach(function (tab) {
    tab.addEventListener("click", function () {
      var want = tab.getAttribute("data-day");
      tabs.forEach(function (t) { t.classList.toggle("on", t === tab); });
      panels.forEach(function (p) {
        p.classList.toggle("hidden", p.getAttribute("data-daypanel") !== want);
      });
    });
  });

  document.querySelectorAll("[data-copy]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var url = btn.getAttribute("data-copy");
      var done = function () {
        var original = btn.textContent;
        btn.textContent = "Copied";
        setTimeout(function () { btn.textContent = original; }, 1500);
      };
      if (navigator.clipboard) {
        navigator.clipboard.writeText(url).then(done, done);
      } else {
        done();
      }
    });
  });

  // localStorage can throw (private mode, blocked site data), so every access
  // is guarded and the list still works without it.
  var list = document.querySelector("[data-checklist]");
  if (!list) return;
  var key = "tn-packing-" + list.getAttribute("data-checklist");
  var state = {};
  try { state = JSON.parse(localStorage.getItem(key) || "{}") || {}; } catch (e) { state = {}; }

  list.querySelectorAll("input[type=checkbox]").forEach(function (box) {
    var idx = box.getAttribute("data-idx");
    box.checked = state[idx] === true;
    box.addEventListener("change", function () {
      state[idx] = box.checked;
      try { localStorage.setItem(key, JSON.stringify(state)); } catch (e) { /* ignore */ }
    });
  });
})();
