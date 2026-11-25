// Copy-link and a browser-local packing checklist. Day navigation is plain
// anchor links (see .date-nav / .jumpnav in itinerary.html) with CSS
// scroll-behavior, so it needs no JS and still works if this script fails.
(function () {
  "use strict";

  // The hero photo URL is set here, from an external script, rather than as
  // an inline style attribute — our Content-Security-Policy intentionally has
  // no 'unsafe-inline' on style-src, which silently drops inline style="...".
  // Script-driven style changes aren't subject to that directive, so this is
  // the CSP-compliant way to do it. Loading the image itself still goes
  // through img-src, which only allows Unsplash's own CDN.
  var hero = document.querySelector("[data-hero-photo]");
  if (hero) {
    var photoURL = hero.getAttribute("data-hero-photo");
    if (/^https:\/\/images\.unsplash\.com\//.test(photoURL)) {
      hero.style.backgroundImage =
        "linear-gradient(165deg, rgba(21,46,40,.88) 0%, rgba(21,46,40,.55) 100%), url('" + photoURL + "')";
    }
  }

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
