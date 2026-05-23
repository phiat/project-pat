// Force-directed idea-cluster board.
// Pure 2D canvas, no external graph lib. Renders over the existing
// three.js landscape (which keeps drawing in #bg-canvas behind us).

const PALETTE = ["#41619c", "#aaae7f", "#d0d6b3", "#f7f7f7", "#efefef", "#7a8a4a"];

const canvas = document.getElementById("board-canvas");
const emptyEl = document.getElementById("board-empty");
const ctx = canvas.getContext("2d");
const dpr = Math.min(window.devicePixelRatio || 1, 2);

let nodes = [];          // {id, title, x, y, vx, vy, r, color, selected}
let edges = [];          // {a, b, weight, reason}
let nodeById = new Map();
let clusterColor = new Map();
let hoverId = null;
let selectedIds = [];
let drag = null;

const cssBg = getComputedStyle(document.body);

resize();
loadAndStart();

window.addEventListener("resize", resize, { passive: true });

document.getElementById("cluster-btn").addEventListener("click", runCluster);
document.getElementById("synth-btn").addEventListener("click", runSynth);

canvas.addEventListener("pointermove", onMove);
canvas.addEventListener("pointerdown", onDown);
canvas.addEventListener("pointerup",   onUp);
canvas.addEventListener("pointerleave", () => { hoverId = null; });
canvas.addEventListener("dblclick", onDblClick);

function resize() {
  const w = canvas.clientWidth, h = canvas.clientHeight;
  canvas.width  = Math.floor(w * dpr);
  canvas.height = Math.floor(h * dpr);
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
}

async function loadAndStart() {
  const res = await fetch("/board/data");
  const data = await res.json();
  buildGraph(data);
  requestAnimationFrame(tick);
}

function buildGraph(data) {
  nodes = []; edges = []; nodeById.clear(); clusterColor.clear(); selectedIds = [];
  const cx = canvas.clientWidth / 2;
  const cy = canvas.clientHeight / 2;

  (data.clusters || []).forEach((c, i) => clusterColor.set(c.id, PALETTE[i % PALETTE.length]));

  (data.ideas || []).forEach((idea, i) => {
    const ang = (i / Math.max(1, data.ideas.length)) * Math.PI * 2;
    const r0 = 120 + Math.random() * 60;
    const node = {
      id: idea.id, title: idea.title, body: idea.body,
      x: cx + Math.cos(ang) * r0,
      y: cy + Math.sin(ang) * r0,
      vx: 0, vy: 0,
      r: 8,
      color: clusterColor.get(idea.cluster_id) || "#aaae7f",
      selected: false,
    };
    nodes.push(node);
    nodeById.set(node.id, node);
  });

  (data.links || []).forEach(l => {
    if (nodeById.has(l.a) && nodeById.has(l.b)) edges.push(l);
  });

  // node radius bumps by degree
  const deg = new Map();
  edges.forEach(e => { deg.set(e.a, (deg.get(e.a) || 0) + e.weight); deg.set(e.b, (deg.get(e.b) || 0) + e.weight); });
  nodes.forEach(n => { n.r = 7 + Math.min(8, (deg.get(n.id) || 0) * 4); });

  emptyEl.hidden = nodes.length > 0;
}

// physics
function step() {
  const w = canvas.clientWidth, h = canvas.clientHeight;
  const cx = w / 2, cy = h / 2;
  const k_rep = 1600;     // repulsion
  const k_spring = 0.045; // spring constant scaled by weight
  const restLen = 90;
  const k_center = 0.012;
  const damping = 0.84;

  for (let i = 0; i < nodes.length; i++) {
    const a = nodes[i];
    let fx = 0, fy = 0;

    // repel
    for (let j = 0; j < nodes.length; j++) {
      if (i === j) continue;
      const b = nodes[j];
      let dx = a.x - b.x, dy = a.y - b.y;
      let d2 = dx*dx + dy*dy;
      if (d2 < 1) d2 = 1;
      const d = Math.sqrt(d2);
      const f = k_rep / d2;
      fx += (dx / d) * f;
      fy += (dy / d) * f;
    }
    // weak center pull
    fx += (cx - a.x) * k_center;
    fy += (cy - a.y) * k_center;
    a.fx = fx; a.fy = fy;
  }
  // springs from edges
  edges.forEach(e => {
    const a = nodeById.get(e.a), b = nodeById.get(e.b);
    if (!a || !b) return;
    const dx = b.x - a.x, dy = b.y - a.y;
    const d = Math.sqrt(dx*dx + dy*dy) || 0.01;
    const f = k_spring * (1 + 2 * e.weight) * (d - restLen);
    const ux = dx / d, uy = dy / d;
    a.fx += ux * f; a.fy += uy * f;
    b.fx -= ux * f; b.fy -= uy * f;
  });
  // integrate
  for (const n of nodes) {
    if (drag && drag.id === n.id) { n.vx = 0; n.vy = 0; continue; }
    n.vx = (n.vx + n.fx * 0.016) * damping;
    n.vy = (n.vy + n.fy * 0.016) * damping;
    n.x += n.vx;
    n.y += n.vy;
    // soft bounds
    const pad = 30;
    if (n.x < pad)        { n.x = pad; n.vx *= -0.3; }
    if (n.x > w - pad)    { n.x = w - pad; n.vx *= -0.3; }
    if (n.y < pad + 60)   { n.y = pad + 60; n.vy *= -0.3; }
    if (n.y > h - pad)    { n.y = h - pad; n.vy *= -0.3; }
  }
}

function draw() {
  const w = canvas.clientWidth, h = canvas.clientHeight;
  ctx.clearRect(0, 0, w, h);

  // edges
  ctx.lineCap = "round";
  edges.forEach(e => {
    const a = nodeById.get(e.a), b = nodeById.get(e.b);
    if (!a || !b) return;
    ctx.strokeStyle = `rgba(208, 214, 179, ${0.18 + 0.7 * e.weight})`;
    ctx.lineWidth = 0.6 + 1.6 * e.weight;
    ctx.beginPath();
    ctx.moveTo(a.x, a.y);
    ctx.lineTo(b.x, b.y);
    ctx.stroke();
  });

  // nodes
  nodes.forEach(n => {
    ctx.beginPath();
    ctx.arc(n.x, n.y, n.r, 0, Math.PI * 2);
    ctx.fillStyle = n.color;
    ctx.fill();
    if (n.selected || hoverId === n.id) {
      ctx.lineWidth = 2.5;
      ctx.strokeStyle = n.selected ? "#f7f7f7" : "rgba(247,247,247,0.6)";
      ctx.stroke();
    }
    // label
    if (hoverId === n.id || n.selected) {
      ctx.fillStyle = "#f7f7f7";
      ctx.font = "13px ui-sans-serif, system-ui, sans-serif";
      ctx.textBaseline = "middle";
      ctx.fillText(n.title, n.x + n.r + 6, n.y);
    } else {
      ctx.fillStyle = "rgba(247,247,247,0.55)";
      ctx.font = "11.5px ui-sans-serif, system-ui, sans-serif";
      ctx.textBaseline = "middle";
      ctx.fillText(truncate(n.title, 36), n.x + n.r + 6, n.y);
    }
  });
}

function tick() {
  step();
  draw();
  requestAnimationFrame(tick);
}

// interaction
function pickNode(x, y) {
  for (let i = nodes.length - 1; i >= 0; i--) {
    const n = nodes[i];
    const dx = x - n.x, dy = y - n.y;
    if (dx*dx + dy*dy <= (n.r + 4) * (n.r + 4)) return n;
  }
  return null;
}
function pointerXY(ev) {
  const r = canvas.getBoundingClientRect();
  return { x: ev.clientX - r.left, y: ev.clientY - r.top };
}
function onMove(ev) {
  const { x, y } = pointerXY(ev);
  if (drag) {
    const n = nodeById.get(drag.id);
    if (n) { n.x = x; n.y = y; }
  } else {
    const n = pickNode(x, y);
    hoverId = n ? n.id : null;
    canvas.style.cursor = n ? "pointer" : "default";
  }
}
function onDown(ev) {
  const { x, y } = pointerXY(ev);
  const n = pickNode(x, y);
  if (n) { drag = { id: n.id }; canvas.setPointerCapture(ev.pointerId); }
}
function onUp(ev) {
  if (drag) {
    const moved = pointerXY(ev);
    const n = nodeById.get(drag.id);
    drag = null;
    canvas.releasePointerCapture(ev.pointerId);
    // distinguish click from drag (small movement)
    if (n) {
      const dx = moved.x - n.x, dy = moved.y - n.y;
      if (Math.abs(dx) < 3 && Math.abs(dy) < 3) toggleSelect(n);
    }
  }
}
function onDblClick(ev) {
  const { x, y } = pointerXY(ev);
  const n = pickNode(x, y);
  if (n) window.open(`/ideas`, "_self");  // ideas list — no per-idea route yet
}

function toggleSelect(n) {
  n.selected = !n.selected;
  if (n.selected) {
    selectedIds.push(n.id);
    if (selectedIds.length > 2) {
      const dropped = selectedIds.shift();
      const d = nodeById.get(dropped);
      if (d) d.selected = false;
    }
  } else {
    selectedIds = selectedIds.filter(id => id !== n.id);
  }
  document.getElementById("synth-btn").disabled = selectedIds.length !== 2;
}

async function runCluster() {
  const spin  = document.getElementById("board-spin");
  const notes = document.getElementById("cluster-notes");
  spin.hidden = false;
  notes.textContent = "";
  const res = await fetch("/board/cluster", { method: "POST" });
  if (!res.ok || !res.body) {
    notes.innerHTML = `<p class="err">cluster failed (${res.status})</p>`;
    spin.hidden = true; return;
  }
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buffer = "", text = "";
  let done = false;
  while (!done) {
    const { value, done: d } = await reader.read();
    if (d) break;
    buffer += dec.decode(value, { stream: true });
    let idx;
    while ((idx = buffer.indexOf("\n\n")) !== -1) {
      const block = buffer.slice(0, idx); buffer = buffer.slice(idx + 2);
      const evt = parseSSE(block);
      if (evt.event === "delta") {
        text += evt.data;
        notes.textContent = text;
      } else if (evt.event === "end") {
        done = true;
      } else if (evt.event === "error") {
        notes.innerHTML = `<p class="err">${evt.data}</p>`;
        spin.hidden = true;
        return;
      }
    }
  }
  spin.hidden = true;
  // reload graph data
  const data = await (await fetch("/board/data")).json();
  buildGraph(data);
}

async function runSynth() {
  if (selectedIds.length !== 2) return;
  const [a, b] = selectedIds;
  const spin  = document.getElementById("board-spin");
  const notes = document.getElementById("cluster-notes");
  spin.hidden = false;
  notes.innerHTML = `<p class="muted">synthesizing #${a} × #${b}…</p>`;
  const fd = new FormData();
  fd.set("a", a); fd.set("b", b);
  const res = await fetch("/board/synthesize", { method: "POST", body: fd });
  if (!res.ok || !res.body) {
    notes.innerHTML = `<p class="err">synth failed (${res.status})</p>`;
    spin.hidden = true; return;
  }
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buffer = "", pid = null;
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += dec.decode(value, { stream: true });
    let idx;
    while ((idx = buffer.indexOf("\n\n")) !== -1) {
      const block = buffer.slice(0, idx); buffer = buffer.slice(idx + 2);
      const evt = parseSSE(block);
      if (evt.event === "project") pid = evt.data;
      else if (evt.event === "end") {
        spin.hidden = true;
        if (pid) window.location.href = `/projects/${pid}`;
        return;
      } else if (evt.event === "error") {
        notes.innerHTML = `<p class="err">${evt.data}</p>`;
        spin.hidden = true; return;
      }
    }
  }
}

function parseSSE(block) {
  const out = { event: "delta", data: "" };
  const lines = [];
  for (const line of block.split("\n")) {
    if (line.startsWith("event:")) out.event = line.slice(6).trim();
    else if (line.startsWith("data:")) lines.push(line.slice(5).replace(/^ /, ""));
  }
  out.data = lines.join("\n");
  return out;
}
function truncate(s, n) { return s.length > n ? s.slice(0, n) + "…" : s; }
