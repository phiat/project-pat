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
canvas.addEventListener("pointerleave", () => { if (hoverId !== null) { hoverId = null; wakeAnim(); } });

function resize() {
  const w = Math.max(canvas.clientWidth, 1), h = Math.max(canvas.clientHeight, 1);
  canvas.width  = Math.floor(w * dpr);
  canvas.height = Math.floor(h * dpr);
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  needRender = true;
  if (!animRAF) animRAF = requestAnimationFrame(tick);
}
if (typeof ResizeObserver !== "undefined") {
  const ro = new ResizeObserver(() => resize());
  ro.observe(canvas);
}

async function loadAndStart() {
  const res = await fetch("/board/data");
  const data = await res.json();
  buildGraph(data);
  wakeAnim();
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
  needRender = true;
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

let animRAF = 0;
let needRender = true;

function tick() {
  step();
  draw();
  // schedule next frame only while the graph is still moving or a
  // re-render was explicitly requested (resize, hover, drag, rebuild).
  if (kineticEnergy() > 0.02 || needRender) {
    needRender = false;
    animRAF = requestAnimationFrame(tick);
  } else {
    animRAF = 0;
  }
}
function kineticEnergy() {
  let e = 0;
  for (const n of nodes) e += n.vx * n.vx + n.vy * n.vy;
  return e;
}
function wakeAnim() {
  needRender = true;
  if (!animRAF) animRAF = requestAnimationFrame(tick);
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
    if (n) { n.x = x; n.y = y; wakeAnim(); }
  } else {
    const n = pickNode(x, y);
    const newHover = n ? n.id : null;
    if (newHover !== hoverId) { hoverId = newHover; wakeAnim(); }
    canvas.style.cursor = n ? "pointer" : "default";
  }
}
function onDown(ev) {
  const { x, y } = pointerXY(ev);
  const n = pickNode(x, y);
  if (n) {
    // remember pointerdown coords so onUp can distinguish click from drag
    drag = { id: n.id, downX: x, downY: y };
    canvas.setPointerCapture(ev.pointerId);
  }
}
function onUp(ev) {
  if (!drag) return;
  const moved = pointerXY(ev);
  const n = nodeById.get(drag.id);
  const dx = moved.x - drag.downX, dy = moved.y - drag.downY;
  const isClick = Math.abs(dx) < 4 && Math.abs(dy) < 4;
  drag = null;
  canvas.releasePointerCapture(ev.pointerId);
  if (n && isClick) toggleSelect(n);
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
  wakeAnim();
}

async function runCluster() {
  const spin  = document.getElementById("board-spin");
  const notes = document.getElementById("cluster-notes");
  spin.hidden = false;
  notes.textContent = "";
  let failed = false;
  await window.streamSSE("/board/cluster", null, {
    onDelta: (t) => { notes.textContent += t; },
    onError: (msg) => { notes.innerHTML = `<p class="err">${window.escapeHTML(msg)}</p>`; failed = true; },
  });
  spin.hidden = true;
  if (failed) return;
  // refresh graph from server
  const data = await (await fetch("/board/data")).json();
  buildGraph(data);
  wakeAnim();
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
  let pid = null, failed = false;
  await window.streamSSE("/board/synthesize", fd, {
    onProject: (id) => { pid = id; },
    onError:   (msg) => { notes.innerHTML = `<p class="err">${window.escapeHTML(msg)}</p>`; failed = true; },
    onEnd:     () => {
      if (pid) window.location.href = `/projects/${pid}`;
    },
  });
  spin.hidden = true;
}

function truncate(s, n) { return s.length > n ? s.slice(0, n) + "…" : s; }
