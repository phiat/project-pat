import * as THREE from "three";

const palette = {
  blackForest: 0x143109,
  drySage:     0xaaae7f,
  beige:       0xd0d6b3,
  balticBlue:  0x41619c,
  brightSnow:  0xf7f7f7,
  platinum:    0xefefef,
};

// Hue-cycle timing. Every CYCLE_INTERVAL seconds the scene runs a short
// CYCLE_DURATION-second hue sweep that visibly rotates through the whole
// spectrum and lands a small HUE_DRIFT_PER_CYCLE step away from where it
// started — so the palette slowly walks over time instead of resetting.
const CYCLE_INTERVAL = 180;     // 3 min idle between sweeps
const CYCLE_DURATION = 5;       // 5 s sweep
const HUE_DRIFT_PER_CYCLE = 14; // degrees the palette drifts each cycle

const canvas = document.getElementById("bg-canvas");
const renderer = new THREE.WebGLRenderer({ canvas, antialias: true, powerPreference: "high-performance" });
renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
renderer.setSize(window.innerWidth, window.innerHeight, false);
renderer.setClearColor(palette.blackForest, 1);

const scene = new THREE.Scene();
scene.fog = new THREE.Fog(palette.blackForest, 50, 180);

const camera = new THREE.PerspectiveCamera(55, window.innerWidth / window.innerHeight, 0.1, 600);
camera.position.set(0, 24, 30);
camera.lookAt(0, 4, -60);

// Lights — each gets a "base color" we shift via HSL on hue cycles.
const lights = {
  hemiSky:   new THREE.Color(palette.balticBlue),
  hemiGround:new THREE.Color(palette.drySage),
  sun:       new THREE.Color(palette.beige),
  fill:      new THREE.Color(palette.balticBlue),
  ptA:       new THREE.Color(palette.balticBlue),
  ptB:       new THREE.Color(palette.drySage),
  fog:       new THREE.Color(palette.blackForest),
  clear:     new THREE.Color(palette.blackForest),
};

const hemi = new THREE.HemisphereLight(palette.balticBlue, palette.drySage, 0.85);
scene.add(hemi);
const sun = new THREE.DirectionalLight(palette.beige, 0.6);
sun.position.set(40, 80, 30);
scene.add(sun);
const fill = new THREE.DirectionalLight(palette.balticBlue, 0.35);
fill.position.set(-50, 40, -20);
scene.add(fill);
// Moving accent point lights — give the landscape a sense of drifting
// clouds/aurora overhead. Decay=1.6 keeps fall-off soft and atmospheric.
const ptA = new THREE.PointLight(palette.balticBlue, 1.4, 90, 1.6);
ptA.position.set(20, 22, -30);
scene.add(ptA);
const ptB = new THREE.PointLight(palette.drySage, 1.0, 70, 1.6);
ptB.position.set(-25, 18, -10);
scene.add(ptB);

const GRID_X = 60;
const GRID_Z = 110;
const CELL = 1.7;
const HEIGHT_SCALE = 4.8;
const DRIFT = 3.2;

const cubeGeo = new THREE.BoxGeometry(CELL * 0.94, 1, CELL * 0.94);
const cubeMat = new THREE.MeshStandardMaterial({
  roughness: 0.92,
  metalness: 0.04,
  flatShading: true,
});
const mesh = new THREE.InstancedMesh(cubeGeo, cubeMat, GRID_X * GRID_Z);
mesh.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
scene.add(mesh);

// Ground plane sitting just below the cubes, matched to the "low"
// (valley) palette colour. In light theme the cream page-bg would
// otherwise show through the 6%-cell gaps and read as a hard seam at
// the horizon; the plane keeps the floor hue-matched to the cubes.
const groundGeo = new THREE.PlaneGeometry(GRID_X * CELL * 4, GRID_Z * CELL * 4);
const groundMat = new THREE.MeshStandardMaterial({
  color: palette.blackForest,
  roughness: 0.98,
  metalness: 0.0,
});
const ground = new THREE.Mesh(groundGeo, groundMat);
ground.rotation.x = -Math.PI / 2;
ground.position.y = -0.01; // just below cube bottoms (which sit at y=0)
scene.add(ground);

const tmpObj = new THREE.Object3D();
const tmpColor = new THREE.Color();
const baseColors = {
  low:    new THREE.Color(palette.blackForest),
  mid:    new THREE.Color(palette.drySage),
  high:   new THREE.Color(palette.beige),
  accent: new THREE.Color(palette.balticBlue),
};
// shiftedColors are baseColors + current hue offset; recomputed when the
// offset changes (idle = once at start, during cycle = each frame).
const shiftedColors = {
  low:    baseColors.low.clone(),
  mid:    baseColors.mid.clone(),
  high:   baseColors.high.clone(),
  accent: baseColors.accent.clone(),
};

function heightAtWorld(wx, wz) {
  const a = Math.sin(wz * 0.18 + wx * 0.11);
  const b = Math.cos(wz * 0.31 - wx * 0.17);
  const c = Math.sin(wx * 0.27 + 1.3);
  const ridge = Math.pow(Math.max(0, a * 0.55 + b * 0.45), 1.4);
  return (ridge + 0.32 * c + 0.45) * HEIGHT_SCALE;
}

function colorForHeight(h, ix, iz) {
  const n = Math.min(1, Math.max(0, h / (HEIGHT_SCALE * 1.4)));
  if (n < 0.45) tmpColor.copy(shiftedColors.low).lerp(shiftedColors.mid, n / 0.45);
  else          tmpColor.copy(shiftedColors.mid).lerp(shiftedColors.high, (n - 0.45) / 0.55);
  if (((ix * 7 + iz * 13) % 113) === 0) tmpColor.lerp(shiftedColors.accent, 0.45);
  return tmpColor;
}

// shiftHSL rotates a color's hue by `deg` degrees, preserving s/l.
const _hsl = { h: 0, s: 0, l: 0 };
function shiftHSL(src, dst, deg) {
  src.getHSL(_hsl);
  let h = _hsl.h + deg / 360;
  h -= Math.floor(h);
  dst.setHSL(h, _hsl.s, _hsl.l);
}

// Light-theme wash: cream backdrop + how aggressively the palette is
// lerped toward white. Tuned to read as a faint pastel landscape so the
// page text stays the focus.
const LIGHT_BG_COLOR  = new THREE.Color(0xf6f3e8);  // matches css --c-bg
const LIGHT_TINT      = new THREE.Color(0xffffff);
const LIGHT_FOG_NEAR  = 18;   // pull fog in so far cubes dissolve into cream
const LIGHT_FOG_FAR   = 120;
const LIGHT_INT_MUL   = 0.55; // dim point/dir lights so highlights don't glare
const LIGHT_DUST_OP   = 0.10;
const DARK_DUST_OP    = 0.32;

// applyHue updates derived palette objects (cube ramp + lights + fog +
// clear color) for the given hue offset. If `lightBlend` > 0, the
// shifted palette is then lerped toward white and the clear/fog swap to
// cream — same hue cycle, washed-out reading.
function applyHue(deg, lightBlend) {
  shiftHSL(baseColors.low,    shiftedColors.low,    deg);
  shiftHSL(baseColors.mid,    shiftedColors.mid,    deg);
  shiftHSL(baseColors.high,   shiftedColors.high,   deg);
  shiftHSL(baseColors.accent, shiftedColors.accent, deg);
  shiftHSL(lights.hemiSky,    hemi.color,           deg);
  shiftHSL(lights.hemiGround, hemi.groundColor,     deg);
  shiftHSL(lights.sun,        sun.color,            deg);
  shiftHSL(lights.fill,       fill.color,           deg);
  shiftHSL(lights.ptA,        ptA.color,            deg);
  shiftHSL(lights.ptB,        ptB.color,            deg);
  shiftHSL(lights.fog,        scene.fog.color,      deg);
  shiftHSL(lights.clear,      tmpColor,             deg);

  if (lightBlend > 0) {
    // Cubes: stronger lerp toward white for lower-elevation cells so the
    // valleys disappear and only ridge tints remain. Accent stays a touch
    // more saturated so the sparkles still read.
    shiftedColors.low.lerp(LIGHT_TINT,    lightBlend * 0.88);
    shiftedColors.mid.lerp(LIGHT_TINT,    lightBlend * 0.78);
    shiftedColors.high.lerp(LIGHT_TINT,   lightBlend * 0.66);
    shiftedColors.accent.lerp(LIGHT_TINT, lightBlend * 0.55);
    hemi.color.lerp(LIGHT_TINT,           lightBlend * 0.55);
    hemi.groundColor.lerp(LIGHT_TINT,     lightBlend * 0.55);
    sun.color.lerp(LIGHT_TINT,            lightBlend * 0.40);
    fill.color.lerp(LIGHT_TINT,           lightBlend * 0.40);
    ptA.color.lerp(LIGHT_TINT,            lightBlend * 0.40);
    ptB.color.lerp(LIGHT_TINT,            lightBlend * 0.40);
    scene.fog.color.lerp(LIGHT_BG_COLOR,  lightBlend);
    tmpColor.copy(LIGHT_BG_COLOR);
  }
  // Ground tracks the (already-blended) "low" colour, but stays a touch
  // darker so the cube bottoms still read as sitting on something.
  groundMat.color.copy(shiftedColors.low).multiplyScalar(lightBlend > 0 ? 0.92 : 0.75);
  renderer.setClearColor(tmpColor, 1);
}

function rewriteInstanceColors() {
  for (let iz = 0; iz < GRID_Z; iz++) {
    for (let ix = 0; ix < GRID_X; ix++) {
      const i = iz * GRID_X + ix;
      const px = (ix - GRID_X / 2) * CELL;
      // height depends on world-z, but we only need a stable per-instance
      // height for color; use the current baseStep so colors track terrain.
      const pz = (iz - GRID_Z / 2) * CELL;
      const wz = pz + lastBaseStep * CELL;
      const h = heightAtWorld(px, wz);
      mesh.setColorAt(i, colorForHeight(h, ix, iz));
    }
  }
  if (mesh.instanceColor) mesh.instanceColor.needsUpdate = true;
}

function isLightTheme() {
  return document.documentElement.getAttribute("data-theme") === "light";
}

// initial fill so first frame renders before tick()
let lastBaseStep = 0;
applyHue(0, isLightTheme() ? 1 : 0);
for (let iz = 0; iz < GRID_Z; iz++) {
  for (let ix = 0; ix < GRID_X; ix++) {
    const i = iz * GRID_X + ix;
    const px = (ix - GRID_X / 2) * CELL;
    const pz = (iz - GRID_Z / 2) * CELL;
    const h = heightAtWorld(px, pz);
    tmpObj.position.set(px, h / 2, pz);
    tmpObj.scale.set(1, Math.max(0.5, h), 1);
    tmpObj.updateMatrix();
    mesh.setMatrixAt(i, tmpObj.matrix);
    mesh.setColorAt(i, colorForHeight(h, ix, iz));
  }
}
mesh.instanceMatrix.needsUpdate = true;
if (mesh.instanceColor) mesh.instanceColor.needsUpdate = true;

const dustGeo = new THREE.BufferGeometry();
const dustCount = 320;
const dustPos = new Float32Array(dustCount * 3);
for (let k = 0; k < dustCount; k++) {
  dustPos[k * 3]     = (Math.random() - 0.5) * 240;
  dustPos[k * 3 + 1] = 25 + Math.random() * 42;
  dustPos[k * 3 + 2] = (Math.random() - 0.5) * 240;
}
dustGeo.setAttribute("position", new THREE.BufferAttribute(dustPos, 3));
const dustMat = new THREE.PointsMaterial({
  color: palette.platinum, size: 0.5, transparent: true, opacity: DARK_DUST_OP, depthWrite: false,
});
const dust = new THREE.Points(dustGeo, dustMat);
scene.add(dust);
dustMat.opacity = isLightTheme() ? LIGHT_DUST_OP : DARK_DUST_OP;

const clock = new THREE.Clock();
lastBaseStep = -1;

// Hue cycle state. `hueOffset` is the permanent drift accumulated across
// previous cycles. `cycleStartT` is the elapsed-time stamp of the in-
// flight cycle, or null when idle. `nextCycleT` is when the next cycle
// kicks off.
let hueOffset = 0;
let cycleStartT = null;
let nextCycleT = CYCLE_INTERVAL;

function easeInOutCubic(x) {
  return x < 0.5 ? 4 * x * x * x : 1 - Math.pow(-2 * x + 2, 3) / 2;
}

function effectiveHueAt(t) {
  if (cycleStartT === null) return hueOffset;
  const dt = t - cycleStartT;
  if (dt >= CYCLE_DURATION) {
    // commit the drift, clear the cycle
    hueOffset = (hueOffset + HUE_DRIFT_PER_CYCLE) % 360;
    cycleStartT = null;
    nextCycleT = t + CYCLE_INTERVAL;
    return hueOffset;
  }
  // Smoothly walk through 360° + HUE_DRIFT_PER_CYCLE over the cycle, so
  // we end exactly at the next committed offset without a snap.
  const phase = easeInOutCubic(dt / CYCLE_DURATION);
  return hueOffset + phase * (360 + HUE_DRIFT_PER_CYCLE);
}

function onResize() {
  const w = window.innerWidth, h = window.innerHeight;
  renderer.setSize(w, h, false);
  camera.aspect = w / h;
  camera.updateProjectionMatrix();
}
window.addEventListener("resize", onResize, { passive: true });

let lastAppliedHue = 0;
let lastLightBlend = isLightTheme() ? 1 : 0;

function tick() {
  const lightBlend = isLightTheme() ? 1 : 0;
  const themeChanged = lightBlend !== lastLightBlend;
  if (themeChanged) dustMat.opacity = lightBlend > 0 ? LIGHT_DUST_OP : DARK_DUST_OP;

  const t = clock.getElapsedTime();
  const scroll = t * DRIFT;
  const baseStep = Math.floor(scroll / CELL);
  const baseStepChanged = baseStep !== lastBaseStep;

  if (baseStepChanged) {
    lastBaseStep = baseStep;
    const baseZ = baseStep * CELL;
    for (let iz = 0; iz < GRID_Z; iz++) {
      const pz = (iz - GRID_Z / 2) * CELL;
      const wz = pz + baseZ;
      for (let ix = 0; ix < GRID_X; ix++) {
        const i = iz * GRID_X + ix;
        const px = (ix - GRID_X / 2) * CELL;
        const h = heightAtWorld(px, wz);
        tmpObj.position.set(px, h / 2, pz);
        tmpObj.scale.set(1, Math.max(0.5, h), 1);
        tmpObj.updateMatrix();
        mesh.setMatrixAt(i, tmpObj.matrix);
        mesh.setColorAt(i, colorForHeight(h, ix, iz));
      }
    }
    mesh.instanceMatrix.needsUpdate = true;
    if (mesh.instanceColor) mesh.instanceColor.needsUpdate = true;
  }
  mesh.position.z = -(scroll - baseStep * CELL);

  // Kick off a cycle if it's due.
  if (cycleStartT === null && t >= nextCycleT) {
    cycleStartT = t;
  }

  const targetHue = effectiveHueAt(t);
  // While cycling we need to rewrite the cube colors every frame; idle we
  // can skip (already handled by baseStepChanged path). A theme flip also
  // forces a full repaint so the wash lands on every cube right away.
  const cycling = cycleStartT !== null;
  if (cycling || themeChanged || Math.abs(targetHue - lastAppliedHue) > 0.05) {
    applyHue(targetHue, lightBlend);
    lastAppliedHue = targetHue;
    lastLightBlend = lightBlend;
    if ((cycling || themeChanged) && !baseStepChanged) rewriteInstanceColors();
  }

  // Dynamic lighting — keep it gentle so it never demands attention.
  // In light mode the whole scene dims (LIGHT_INT_MUL) so the cream
  // backdrop stays dominant.
  const im = 1 - lightBlend * (1 - LIGHT_INT_MUL);
  hemi.intensity   = (0.78 + Math.sin(t * 0.07) * 0.14) * im;
  sun.intensity    = (0.55 + Math.sin(t * 0.05 + 1.3) * 0.10) * im;
  fill.intensity   = (0.32 + Math.sin(t * 0.06 + 2.1) * 0.08) * im;
  ptA.intensity    = (1.10 + Math.sin(t * 0.18) * 0.45) * im;
  ptB.intensity    = (0.85 + Math.sin(t * 0.13 + 0.8) * 0.35) * im;
  ptA.position.set(Math.sin(t * 0.11) * 45, 22 + Math.sin(t * 0.07) * 4, -30 + Math.cos(t * 0.12) * 25);
  ptB.position.set(Math.cos(t * 0.09) * 35, 16 + Math.cos(t * 0.10) * 3, -10 + Math.sin(t * 0.13) * 18);
  sun.position.set(40 + Math.sin(t * 0.03) * 25, 80, 30 + Math.cos(t * 0.03) * 25);
  const fogNearDark = 50 + Math.sin(t * 0.05) * 6;
  const fogFarDark  = 180 + Math.sin(t * 0.04 + 1.0) * 10;
  scene.fog.near = fogNearDark + (LIGHT_FOG_NEAR - fogNearDark) * lightBlend;
  scene.fog.far  = fogFarDark  + (LIGHT_FOG_FAR  - fogFarDark ) * lightBlend;

  // gentle camera sway — feels like a window seat
  camera.position.x = Math.sin(t * 0.08) * 1.2;
  camera.position.y = 24 + Math.sin(t * 0.05) * 0.6;
  camera.lookAt(Math.sin(t * 0.04) * 4, 4, -60);

  renderer.render(scene, camera);
  requestAnimationFrame(tick);
}
tick();
