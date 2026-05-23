// Shared SSE-streaming client used by every page that consumes the
// server's /event-stream endpoints. Exposed on window so inline scripts
// (which are not ES modules) can use it without import boilerplate.
//
// usage:
//   const r = await window.streamSSE("/some/url", formData, {
//     onDelta:   (text) => pre.textContent += text,
//     onMeta:    (s)    => meta.textContent = s,
//     onProject: (id)   => navigate(id),
//     onEnd:     (html) => out.innerHTML = html,
//     onError:   (msg)  => out.innerHTML = `<p class="err">${window.escapeHTML(msg)}</p>`,
//     onAny:     (event, data) => { /* fallback */ },
//   });
//
// returns { ok: boolean, accumulated: string }.
(function () {
  async function streamSSE(url, body, handlers) {
    handlers = handlers || {};
    let res;
    try {
      res = await fetch(url, { method: "POST", body });
    } catch (e) {
      fire(handlers, "onError", "network: " + e.message);
      return { ok: false, accumulated: "" };
    }
    if (!res.ok || !res.body) {
      fire(handlers, "onError", "request failed (" + res.status + ")");
      return { ok: false, accumulated: "" };
    }
    const reader = res.body.getReader();
    const dec = new TextDecoder();
    let buffer = "", accumulated = "";
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += dec.decode(value, { stream: true });
      let idx;
      while ((idx = buffer.indexOf("\n\n")) !== -1) {
        const block = buffer.slice(0, idx);
        buffer = buffer.slice(idx + 2);
        const evt = parseSSE(block);
        if (evt.event === "delta") accumulated += evt.data;
        const h = handlers["on" + cap(evt.event)];
        if (h) h(evt.data);
        else if (handlers.onAny) handlers.onAny(evt.event, evt.data);
      }
    }
    return { ok: true, accumulated: accumulated };
  }

  function parseSSE(block) {
    const out = { event: "delta", data: "" };
    const lines = [];
    for (const line of block.split("\n")) {
      if (line.startsWith(":")) continue; // SSE comment
      if (line.startsWith("event:")) out.event = line.slice(6).trim();
      else if (line.startsWith("data:")) lines.push(line.slice(5).replace(/^ /, ""));
    }
    out.data = lines.join("\n");
    return out;
  }

  function fire(h, name, arg) {
    if (h[name]) h[name](arg);
  }
  function cap(s) { return s.charAt(0).toUpperCase() + s.slice(1); }

  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;", "'": "&#39;" }[c]));
  }

  window.streamSSE = streamSSE;
  window.escapeHTML = escapeHTML;
})();
