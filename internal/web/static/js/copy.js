// Global click handler for .copy-btn buttons.
//
// Sources are resolved in this priority:
//   1. data-copy-source-id  -> #<id>     (use .value if textarea, else textContent)
//   2. data-copy-source     -> selector  (same accessor logic)
//   3. data-copy-target     -> #<id>     (textContent of that element)
//
// Visual feedback: adds .copied for ~1.2s after a successful write. On
// failure (no clipboard permission, missing source), adds .copy-failed
// and logs to the console.
document.addEventListener("click", async (ev) => {
  const btn = ev.target.closest(".copy-btn");
  if (!btn) return;

  let el = null;
  if (btn.dataset.copySourceId)  el = document.getElementById(btn.dataset.copySourceId);
  else if (btn.dataset.copySource) el = document.querySelector(btn.dataset.copySource);
  else if (btn.dataset.copyTarget) el = document.getElementById(btn.dataset.copyTarget);

  if (!el) { flash(btn, "copy-failed"); console.warn("copy: no source"); return; }
  const text = (el.tagName === "TEXTAREA" || el.tagName === "INPUT") ? el.value : el.textContent;
  if (!text) { flash(btn, "copy-failed"); console.warn("copy: empty source"); return; }

  try {
    await navigator.clipboard.writeText(text);
    flash(btn, "copied");
  } catch (e) {
    // legacy fallback
    try {
      const ta = document.createElement("textarea");
      ta.value = text;
      ta.style.position = "fixed"; ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      document.execCommand("copy");
      ta.remove();
      flash(btn, "copied");
    } catch (e2) {
      flash(btn, "copy-failed");
      console.warn("copy failed:", e2);
    }
  }
});

function flash(btn, cls) {
  btn.classList.add(cls);
  setTimeout(() => btn.classList.remove(cls), 1200);
}

// Helper exposed for streaming handlers that accumulate markdown text and
// want to persist it into the page's hidden source element before the
// rendered HTML swap.
window.setCopySource = function(name, text) {
  const el = document.querySelector(`[data-source-for="${CSS.escape(name)}"]`);
  if (el) {
    if (el.tagName === "TEXTAREA") el.value = text;
    else el.textContent = text;
  }
};
