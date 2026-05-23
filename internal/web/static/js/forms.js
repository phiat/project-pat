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
