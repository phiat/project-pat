// Global form-related helpers. Currently:
//
//   .cron-preset <select data-target="<id>"> — selecting a preset writes
//   the value into the input with the given id, then clears the select.
document.addEventListener("change", (ev) => {
  const sel = ev.target.closest(".cron-preset");
  if (!sel || !sel.value) return;
  const tgt = document.getElementById(sel.dataset.target);
  if (tgt) {
    tgt.value = sel.value;
    tgt.dispatchEvent(new Event("input", { bubbles: true }));
  }
  sel.value = "";
});

// Global error toast for htmx requests. Without this, a 4xx/5xx with
// hx-swap="none" produces zero visible feedback and the user thinks
// the click did nothing.
(function () {
  let toast = null;
  let hideTimer = null;
  function showError(msg) {
    if (!toast) {
      toast = document.createElement("div");
      toast.id = "error-toast";
      toast.setAttribute("role", "alert");
      document.body.appendChild(toast);
    }
    toast.textContent = msg;
    toast.classList.add("show");
    clearTimeout(hideTimer);
    hideTimer = setTimeout(() => toast.classList.remove("show"), 5000);
  }
  document.body.addEventListener("htmx:responseError", (ev) => {
    const xhr = ev.detail && ev.detail.xhr;
    const body = (xhr && xhr.responseText || "").trim();
    showError(`request failed (${xhr ? xhr.status : "?"})${body ? ": " + body.slice(0, 200) : ""}`);
  });
  document.body.addEventListener("htmx:sendError", () => {
    showError("network error — request did not reach the server");
  });
})();
