import * as THREE from "three";

const palette = {
  blackForest: 0x143109,
  drySage:     0xaaae7f,
  beige:       0xd0d6b3,
  balticBlue:  0x41619c,
  brightSnow:  0xf7f7f7,
  platinum:    0xefefef,
};

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

const hemi = new THREE.HemisphereLight(palette.balticBlue, palette.drySage, 0.85);
scene.add(hemi);
const sun = new THREE.DirectionalLight(palette.beige, 0.6);
sun.position.set(40, 80, 30);
scene.add(sun);
const fill = new THREE.DirectionalLight(palette.balticBlue, 0.35);
fill.position.set(-50, 40, -20);
scene.add(fill);

const GRID_X = 60;
const GRID_Z = 110;
const CELL = 1.7;
const HEIGHT_SCALE = 4.8;
const DRIFT = 3.2;

const cubeGeo = new THREE.BoxGeometry(CELL * 0.94, 1, CELL * 0.94);
const cubeMat = new THREE.MeshStandardMaterial({
  roughness: 0.95,
  metalness: 0.0,
  flatShading: true,
});
const mesh = new THREE.InstancedMesh(cubeGeo, cubeMat, GRID_X * GRID_Z);
mesh.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
scene.add(mesh);

const tmpObj = new THREE.Object3D();
const tmpColor = new THREE.Color();
const cLow    = new THREE.Color(palette.blackForest);
const cMid    = new THREE.Color(palette.drySage);
const cHigh   = new THREE.Color(palette.beige);
const cAccent = new THREE.Color(palette.balticBlue);

function heightAtWorld(wx, wz) {
  const a = Math.sin(wz * 0.18 + wx * 0.11);
  const b = Math.cos(wz * 0.31 - wx * 0.17);
  const c = Math.sin(wx * 0.27 + 1.3);
  const ridge = Math.pow(Math.max(0, a * 0.55 + b * 0.45), 1.4);
  return (ridge + 0.32 * c + 0.45) * HEIGHT_SCALE;
}

function colorForHeight(h, ix, iz) {
  const n = Math.min(1, Math.max(0, h / (HEIGHT_SCALE * 1.4)));
  if (n < 0.45) tmpColor.copy(cLow).lerp(cMid, n / 0.45);
  else          tmpColor.copy(cMid).lerp(cHigh, (n - 0.45) / 0.55);
  if (((ix * 7 + iz * 13) % 113) === 0) tmpColor.lerp(cAccent, 0.45);
  return tmpColor;
}

// initial fill so first frame renders before tick()
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
const dustCount = 220;
const dustPos = new Float32Array(dustCount * 3);
for (let k = 0; k < dustCount; k++) {
  dustPos[k * 3]     = (Math.random() - 0.5) * 220;
  dustPos[k * 3 + 1] = 25 + Math.random() * 38;
  dustPos[k * 3 + 2] = (Math.random() - 0.5) * 220;
}
dustGeo.setAttribute("position", new THREE.BufferAttribute(dustPos, 3));
const dust = new THREE.Points(dustGeo, new THREE.PointsMaterial({
  color: palette.platinum, size: 0.5, transparent: true, opacity: 0.32, depthWrite: false,
}));
scene.add(dust);

const clock = new THREE.Clock();
let lastBaseStep = -1;

function onResize() {
  const w = window.innerWidth, h = window.innerHeight;
  renderer.setSize(w, h, false);
  camera.aspect = w / h;
  camera.updateProjectionMatrix();
}
window.addEventListener("resize", onResize, { passive: true });

function tick() {
  const t = clock.getElapsedTime();
  const scroll = t * DRIFT;
  // recompute heights exactly when the world-row offset advances; this is
  // the same boundary at which mesh.position.z wraps back to 0, so heights
  // and translation stay in sync (no visible pop on CELL crossings).
  const baseStep = Math.floor(scroll / CELL);
  if (baseStep !== lastBaseStep) {
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

  // gentle camera sway — feels like a window seat
  camera.position.x = Math.sin(t * 0.08) * 1.2;
  camera.position.y = 24 + Math.sin(t * 0.05) * 0.6;
  camera.lookAt(Math.sin(t * 0.04) * 4, 4, -60);

  renderer.render(scene, camera);
  requestAnimationFrame(tick);
}
tick();
