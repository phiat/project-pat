// Theme toggle. The initial theme is applied by the inline <head>
// script in layout.html before the stylesheet loads (no FOUC); this
// file only handles click toggles and persisting the choice.
(function () {
  const KEY = "pat-theme";
  function current() {
    return document.documentElement.getAttribute("data-theme") === "light" ? "light" : "dark";
  }
  function apply(theme) {
    if (theme === "light") document.documentElement.setAttribute("data-theme", "light");
    else document.documentElement.removeAttribute("data-theme");
    localStorage.setItem(KEY, theme);
    const btn = document.querySelector(".theme-toggle");
    if (btn) {
      const next = theme === "light" ? "dark" : "light";
      btn.setAttribute("aria-label", "switch to " + next + " mode");
      btn.setAttribute("title", "switch to " + next + " mode");
    }
  }
  document.addEventListener("click", function (ev) {
    const btn = ev.target.closest(".theme-toggle");
    if (!btn) return;
    apply(current() === "light" ? "dark" : "light");
  });
  // Pick up system-preference changes only if the user hasn't pinned a choice.
  if (window.matchMedia) {
    const mq = window.matchMedia("(prefers-color-scheme: light)");
    if (mq.addEventListener) {
      mq.addEventListener("change", function (e) {
        if (localStorage.getItem(KEY)) return;
        apply(e.matches ? "light" : "dark");
      });
    }
  }
})();
