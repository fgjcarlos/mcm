// The live route diagram. Loaded only through dynamic import() from
// signal-hero.ts; nothing else may import this module or three.
import {
  AdditiveBlending,
  AmbientLight,
  BoxGeometry,
  CanvasTexture,
  CircleGeometry,
  Color,
  CylinderGeometry,
  DirectionalLight,
  Fog,
  Group,
  InstancedMesh,
  LinearSRGBColorSpace,
  Matrix4,
  Mesh,
  MeshLambertMaterial,
  PerspectiveCamera,
  RingGeometry,
  Scene,
  Sprite,
  SpriteMaterial,
  Vector3,
  WebGLRenderer,
  type BufferGeometry,
  type Material,
  type Object3D,
} from "three";
import {
  BLADE, BRANCH_DX, BRANCH_DY, COLORS, HOUSING_R, JUNCTION_X, LAMP_R, LOCK_LIFT, LOCK_Y,
  SIGNAL_HOUSING_R, SIGNAL_R, SIGNAL_X, STATIONS, TRACK_W,
} from "./route-geometry";

// ---- Story: one cycle, in seconds. Odd cycles fail the probe and roll back.

const CYCLE = 11.5;
const VERIFIED_HOLD = 8; // a moment in a verified cycle that matches the poster
const START = VERIFIED_HOLD - CYCLE; // cycle -1 = the poster's state; cycle 0 runs the full story

const clamp01 = (p: number) => Math.min(1, Math.max(0, p));
const easeOut = (p: number) => 1 - (1 - p) ** 3;
const easeInOut = (p: number) => (p < 0.5 ? 4 * p ** 3 : 1 - (-2 * p + 2) ** 3 / 2);
// Points and lock: a fast throw, a small overshoot, then it settles.
const thrown = (p: number) => (p >= 1 ? 1 : 1 - Math.exp(-7 * p) * Math.cos(9 * p));
const at = (t: number, start: number, dur: number, ease = easeOut) => ease(clamp01((t - start) / dur));

export interface RouteState {
  /** Lit fraction of edge→review, review→MFA, MFA→apply, apply→probe. */
  wipe: number[];
  /** Station lamps: review, MFA, apply, probe. */
  lamps: number[];
  /** 0 locked, 1 released. */
  lock: number;
  /** 0 set to the branch (previous revision), 1 set to the main line. */
  points: number;
  /** Lit fraction of probe→signal, trailing the probe pulse. */
  probe: number;
  /** Junction→signal stays lit (0 once a failed route is dropped). */
  tail: number;
  /** Probe pulse position along probe→signal, or -1 when none travels. */
  pulse: number;
  proceed: number;
  danger: number;
  /** Lit fraction of the branch from the junction to the rolled-back lamp. */
  branch: number;
  rolled: number;
}

export function routeState(t: number, fail: boolean): RouteState {
  const release = 1 - at(t, 10.7, 0.4);
  const reset = 1 - at(t, 10.75, 0.5, thrown);
  const probe = at(t, 4.8, 1, easeInOut);
  const s: RouteState = {
    wipe: [at(t, 0.3, 0.5), at(t, 1.2, 0.5), at(t, 2.5, 0.5), at(t, 3.8, 0.5)].map((w) => w * release),
    lamps: [0.8, 2.15, 3.0, 4.3].map((start) => at(t, start, 0.15) * release),
    lock: at(t, 1.8, 0.45, thrown) * reset,
    points: at(t, 3.0, 0.5, thrown) * reset,
    probe: probe * release,
    tail: 1,
    pulse: t >= 4.8 && t < 5.8 ? probe : -1,
    proceed: 0,
    danger: 0,
    branch: 0,
    rolled: 0,
  };
  if (fail) {
    const drop = at(t, 7.0, 0.3);
    s.danger = at(t, 5.8, 0.12) * (1 - drop);
    s.tail = 1 - drop;
    s.points = at(t, 3.0, 0.5, thrown) * (1 - at(t, 7.2, 0.5, thrown));
    s.branch = at(t, 7.7, 0.5) * release;
    s.rolled = at(t, 8.2, 0.15) * release;
  } else {
    s.proceed = at(t, 5.8, 0.2) * release;
  }
  return s;
}

// ---- Colour

type Lab = [number, number, number];
const srgb = (hex: string) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255) as Lab;

/** fg at alpha over the frame, mixed in sRGB like the poster's stroke-opacity. */
function over(fg: string, alpha: number): string {
  const b = srgb(COLORS.frame);
  const mix = srgb(fg).map((v, i) => Math.round((b[i] + (v - b[i]) * alpha) * 255));
  return `#${mix.map((c) => c.toString(16).padStart(2, "0")).join("")}`;
}

function oklab(hex: string): Lab {
  const [r, g, b] = srgb(hex).map((c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  return [
    0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
  ];
}

/** Blends in OKLab: a lighting lamp passes through a darker amber (or green, red), never an olive grey. */
function blend(out: Color, from: Lab, to: Lab, t: number): Color {
  const [L, A, B] = from.map((v, i) => v + (to[i] - v) * clamp01(t));
  const l = (L + 0.3963377774 * A + 0.2158037573 * B) ** 3;
  const m = (L - 0.1055613458 * A - 0.0638541728 * B) ** 3;
  const s = (L - 0.0894841775 * A - 1.291485548 * B) ** 3;
  return out.setRGB(
    Math.max(0, 4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s),
    Math.max(0, -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s),
    Math.max(0, -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s),
    LinearSRGBColorSpace,
  );
}

// ---- Camera: it starts as the poster's straight-on view of the track diagram,
// swings round once to rake along it from low on the left ("route opening"),
// then only trucks after the route light.

const DEG = Math.PI / 180;
const PITCH = 6 * DEG; // from a little below the track, looking up along it
const ENTER_AT = 0.7; // after the 700ms poster → canvas crossfade
const ENTER_FOR = 2.4;
const DOLLY = 0.16; // truck as a fraction of the route light's distance from the route's middle
const SPRING = 1.6; // rad/s, critically damped: slow, no overshoot
const lerp = (a: number, b: number, t: number) => a + (b - a) * t;

// ---- Volume, in rail px like the rest of the geometry. z points out of the poster.

const TRACK_H = 3;
const TRACK_Z = 2.5; // the bar sits on the sleepers
const HOUSING_H = 6;
const SLEEPER_GAP = 20;
const SLEEPERS = 1000;
// Label offsets from the track, as SignalHero places them (bottom-4, bottom-5, top-15).
const LABEL_STATION = 16;
const LABEL_SIGNAL = 20;
const LABEL_ROLLED = 60;

// ---- Scene

function glowTexture(): CanvasTexture {
  const c = document.createElement("canvas");
  c.width = c.height = 64;
  const g = c.getContext("2d")!;
  // Like the poster's glow, which the lens covers: nothing inside the lens
  // (a third of the sprite), so the lens keeps its own colour instead of
  // washing out to yellow, then a linear falloff.
  const grad = g.createRadialGradient(32, 32, 0, 32, 32, 32);
  grad.addColorStop(0.3, "rgb(255 255 255 / 0)");
  grad.addColorStop(0.34, "rgb(255 255 255 / 0.66)");
  grad.addColorStop(1, "rgb(255 255 255 / 0)");
  g.fillStyle = grad;
  g.fillRect(0, 0, 64, 64);
  return new CanvasTexture(c);
}

export function mountRouteScene(stage: HTMLElement, reduce: MediaQueryList): void {
  const canvas = stage.querySelector<HTMLCanvasElement>("[data-hero-canvas]");
  const rail = stage.querySelector<HTMLElement>("[data-hero-rail]");
  const toggle = stage.querySelector<HTMLButtonElement>("[data-hero-toggle]");
  const toggleLabel = stage.querySelector<HTMLElement>("[data-hero-toggle-label]");
  const toggleIcon = stage.querySelector<SVGPathElement>("[data-hero-toggle-icon]");
  if (!canvas || !rail || !toggle || !toggleLabel || !toggleIcon) return;

  let renderer: WebGLRenderer;
  try {
    renderer = new WebGLRenderer({ canvas, antialias: true, powerPreference: "low-power" });
  } catch {
    return; // the poster stays
  }
  renderer.setClearColor(COLORS.frame);

  const scene = new Scene();
  const fog = new Fog(COLORS.frame, 1e5, 2e5);
  scene.fog = fog;
  const camera = new PerspectiveCamera(30, 1, 10, 1e5);
  // Lit so the top faces read at their poster colour and the sides fall darker.
  scene.add(new AmbientLight(0xffffff, 1.7));
  const sun = new DirectionalLight(0xffffff, 2);
  sun.position.set(-0.4, -0.8, 1);
  scene.add(sun);

  const geometries: BufferGeometry[] = [];
  const materials: Material[] = [];
  const geo = <T extends BufferGeometry>(g: T) => (geometries.push(g), g);
  const box = geo(new BoxGeometry());
  const bladeGeo = geo(new BoxGeometry().translate(0.5, 0, 0));
  const drum = geo(new CylinderGeometry(1, 1, 1, 40).rotateX(Math.PI / 2).translate(0, 0, 0.5));
  const disc = geo(new CircleGeometry(1, 40));
  const housingGeo = geo(new RingGeometry(HOUSING_R - 0.75, HOUSING_R + 0.75, 40));
  const signalHousingGeo = geo(new RingGeometry(SIGNAL_HOUSING_R - 0.75, SIGNAL_HOUSING_R + 0.75, 40));
  const shackleGeo = geo(new RingGeometry(3.5, 5.5, 24, 1, 0, Math.PI));
  const glowTex = glowTexture();

  const C = {
    frame: new Color(COLORS.frame),
    sleeper: new Color(over(COLORS.enamel, 0.1)),
    proceed: new Color(COLORS.proceed),
    danger: new Color(COLORS.danger),
  };
  const LAB = {
    off: oklab(COLORS.lampOff),
    lamp: oklab(COLORS.lamp),
    proceed: oklab(COLORS.proceed),
    danger: oklab(COLORS.danger),
    muted: oklab(COLORS.enamelMuted),
    lockOff: oklab(over(COLORS.enamel, 0.35)),
  };
  const lambert = (color: string, emissive?: string) => {
    // Lenses are emissive and stay out of the fog: lamps carry light.
    const m = new MeshLambertMaterial(emissive ? { color: 0, emissive, fog: false } : { color });
    materials.push(m);
    return m;
  };
  const mesh = (g: BufferGeometry, m: Material, parent: Object3D = scene) => {
    const o = new Mesh(g, m);
    parent.add(o);
    return o;
  };
  // Glow on lamps only: an additive sprite drawn last, over its lens.
  const glow = (color: string, size: number, parent: Object3D) => {
    const m = new SpriteMaterial({
      map: glowTex, color, blending: AdditiveBlending, depthTest: false, depthWrite: false, fog: false, opacity: 0,
    });
    materials.push(m);
    const s = new Sprite(m);
    s.scale.set(size, size, 1);
    s.position.z = HOUSING_H + 1;
    s.renderOrder = 1;
    parent.add(s);
    return s;
  };
  const housingMat = lambert(COLORS.frame);
  const rimMat = lambert(over(COLORS.enamel, 0.35));
  // A lamp: a low drum housing with an enamel rim and an emissive lens on top.
  const lamp = (r: number, housingR: number, rim: RingGeometry, glowColor: string) => {
    const g = new Group();
    scene.add(g);
    const lens = lambert("", COLORS.lampOff);
    const halo = glow(glowColor, r * 6, g);
    mesh(drum, housingMat, g).scale.set(housingR, housingR, HOUSING_H);
    mesh(rim, rimMat, g).position.z = HOUSING_H + 0.05;
    const l = mesh(disc, lens, g);
    l.scale.setScalar(r);
    l.position.z = HOUSING_H + 0.1;
    return { g, lens, halo };
  };

  const track = lambert(over(COLORS.enamel, 0.28));
  const lit = lambert(COLORS.lamp);
  const baseMainIn = mesh(box, track);
  const baseMainOut = mesh(box, track);
  const baseDiag = mesh(box, track);
  const baseBranch = mesh(box, track);
  const litMain = [0, 1, 2, 3].map(() => mesh(box, lit));
  const litProbeIn = mesh(box, lit);
  const litProbeOut = mesh(box, lit);
  const litDiag = mesh(box, lit);
  const litBranch = mesh(box, lit);
  const bladeMat = lambert(COLORS.enamelMuted);
  const blade = mesh(bladeGeo, bladeMat);
  blade.scale.set(BLADE, TRACK_W + 0.6, TRACK_H + 0.6);
  // Sleepers give the perspective its rhythm; they fade up with the entrance (the poster has none).
  const sleeperMat = lambert(COLORS.frame);
  const sleepers = new InstancedMesh(box, sleeperMat, SLEEPERS);
  sleepers.frustumCulled = false; // its bounds change with every layout
  scene.add(sleepers);

  const stations = STATIONS.map(() => lamp(LAMP_R, HOUSING_R, housingGeo, COLORS.lamp));
  const signal = lamp(SIGNAL_R, SIGNAL_HOUSING_R, signalHousingGeo, COLORS.proceed);
  const rolled = lamp(LAMP_R, HOUSING_R, housingGeo, COLORS.lamp);

  const lockMat = lambert(over(COLORS.enamel, 0.35));
  const lock = new Group();
  stations[1].g.add(lock);
  const body = mesh(box, lockMat, lock);
  body.scale.set(14, 11, 3);
  body.position.set(0, -LOCK_Y - 5.5, 1.5);
  const shackle = new Group();
  shackle.position.z = 3;
  lock.add(shackle);
  mesh(shackleGeo, lockMat, shackle).position.set(0, -LOCK_Y + 5, 0);
  for (const x of [-4.5, 4.5]) {
    const leg = mesh(box, lockMat, shackle);
    leg.scale.set(2, 5, 1);
    leg.position.set(x, -LOCK_Y + 2.5, 0);
  }

  // The probe pulse: an emissive bead riding the track (no glow: it is not a lamp).
  const pulse = mesh(drum, lambert("", COLORS.lamp));
  pulse.scale.set(4, 4, TRACK_H + 3);

  // Lay a unit box along a→b on the track (three coordinates, y up).
  const span = (m: Mesh, ax: number, ay: number, bx: number, by: number, grow = 0.4) => {
    const len = Math.hypot(bx - ax, by - ay);
    m.visible = len > 0.01;
    m.position.set((ax + bx) / 2, (ay + by) / 2, TRACK_Z);
    m.rotation.z = Math.atan2(by - ay, bx - ax);
    m.scale.set(len, TRACK_W + grow, TRACK_H + grow);
  };

  const angle = Math.atan2(BRANCH_DY, BRANCH_DX);
  const diagLen = Math.hypot(BRANCH_DX, BRANCH_DY);
  let W = 0; // stage, CSS px
  let H = 0;
  let k = 1; // CSS px per rail px (--route-scale)
  let w = 0; // stage, rail px
  let h = 0;
  let y = 0; // track centre line
  let nodes: number[] = []; // edge, review, MFA, apply, probe
  let jx = 0;
  let sx = 0;
  let rake = { x: 0, y: 0, d: 1 }; // fitted raking target and distance
  let yaw = 0; // raking turn, toward the signal
  let fov = 30; // vertical, from a horizontal field of view
  let dollyK = DOLLY; // a narrow stage trucks less

  /** Looks at (tx, ty) on the diagram from d away, turned by yaw and pitch. */
  function aim(fov: number, pitch: number, yaw: number, tx: number, ty: number, d: number) {
    camera.fov = fov;
    camera.aspect = W / H;
    camera.updateProjectionMatrix();
    camera.rotation.set(pitch, yaw, 0, "YXZ");
    const flat = d * Math.cos(pitch);
    camera.position.set(tx + flat * Math.sin(yaw), ty - d * Math.sin(pitch), flat * Math.cos(yaw));
    camera.updateMatrixWorld();
  }

  const v = new Vector3();
  /** Where an NDC point's ray meets the track plane (z = 0). */
  function ground(nx: number, ny: number): [number, number] {
    v.set(nx, ny, 0.5).unproject(camera).sub(camera.position);
    const t = -camera.position.z / v.z;
    return [camera.position.x + v.x * t, camera.position.y + v.y * t];
  }

  // Raking pose: put the route, its labels and the dolly's travel where the
  // poster shows them, as wide as there, in perspective.
  function fit() {
    // A wide stage rakes harder; a narrow one turns less so its far labels stay apart.
    const q = clamp01((W / H - 2) / 3);
    yaw = -(12 + 26 * q) * DEG;
    fov = (2 * Math.atan(Math.tan(((45 + 25 * q) * DEG) / 2) / (W / H))) / DEG;
    dollyK = DOLLY * (0.4 + 0.6 * q);
    const travel = (dollyK * (sx - nodes[1])) / 2;
    // Half a label either side: "Review" left of the first lamp, "Verified" right of the signal.
    const x0 = nodes[1] - 36 - travel;
    const x1 = sx + 40 + travel;
    const y0 = y - LABEL_ROLLED - 20;
    const y1 = y + LABEL_SIGNAL + 20;
    const ndcX = (x: number) => Math.min(0.96, Math.max(-0.96, (2 * x) / w - 1));
    const wantX = (ndcX(x0) + ndcX(x1)) / 2;
    const wantW = ndcX(x1) - ndcX(x0);
    const wantY = 1 + (y0 + y1) / h;
    // The box's projected NDC bounds, or null once a corner reaches the near plane.
    const bounds = () => {
      const xs: number[] = [];
      const ys: number[] = [];
      for (const px of [x0, x1]) for (const py of [y0, y1]) for (const pz of [0, HOUSING_H]) {
        v.set(px, py, pz).project(camera);
        if (Math.abs(v.z) > 1) return null;
        xs.push(v.x);
        ys.push(v.y);
      }
      return [Math.min(...xs), Math.max(...xs), Math.min(...ys), Math.max(...ys)];
    };
    // At distance d, slide the target until the box is centred where the poster has it.
    const centred = (d: number) => {
      let tx = (x0 + x1) / 2;
      let ty = (y0 + y1) / 2;
      for (let i = 0; i < 5; i++) {
        aim(fov, PITCH, yaw, tx, ty, d);
        const b = bounds();
        if (!b) return null;
        const [qx, qy] = ground((b[0] + b[1]) / 2, (b[2] + b[3]) / 2);
        const [ox, oy] = ground(wantX, wantY);
        tx += qx - ox;
        ty += qy - oy;
      }
      aim(fov, PITCH, yaw, tx, ty, d);
      const b = bounds();
      return b && b[1] - b[0] <= wantW && b[3] - b[2] <= 1.8 ? { x: tx, y: ty, d } : null;
    };
    // The closest distance that still fits: bisection, since it fails both too near and too wide.
    let lo = 0;
    let hi = 20 * (x1 - x0);
    rake = { x: (x0 + x1) / 2, y: (y0 + y1) / 2, d: hi };
    for (let i = 0; i < 24; i++) {
      const d = (lo + hi) / 2;
      const fits = centred(d);
      if (fits) [hi, rake] = [d, fits];
      else lo = d;
    }
  }

  function layout() {
    const s = stage.getBoundingClientRect();
    const r = rail!.getBoundingClientRect();
    // The rail is drawn at --route-scale (route-rail, global.css): lay the scene
    // out in unscaled rail px; the start pose then frames it like the poster.
    k = r.width / rail!.offsetWidth || 1;
    W = s.width;
    H = s.height;
    w = W / k;
    h = H / k;
    const lowPower = matchMedia("(pointer: coarse)").matches || innerWidth < 768;
    renderer.setPixelRatio(lowPower ? 1 : Math.min(devicePixelRatio, 1.5));
    renderer.setSize(W, H, false);

    const ox = (r.left - s.left) / k;
    const railW = r.width / k;
    y = -(r.top - s.top) / k;
    nodes = [-w, ...STATIONS.map((st) => ox + st.x * railW)];
    jx = ox + JUNCTION_X * railW;
    sx = ox + SIGNAL_X * railW;
    const by = y - BRANCH_DY;

    span(baseMainIn, -2 * w, y, jx, y, 0);
    span(baseMainOut, jx + BLADE, y, 3 * w, y, 0);
    span(baseDiag, jx + BLADE * Math.cos(angle), y - BLADE * Math.sin(angle), jx + BRANCH_DX, by, 0);
    span(baseBranch, jx + BRANCH_DX, by, 3 * w, by, 0);
    blade.position.set(jx, y, TRACK_Z);
    stations.forEach((st, i) => st.g.position.set(nodes[i + 1], y, 0));
    signal.g.position.set(sx, y, 0);
    rolled.g.position.set(sx, by, 0);

    let n = 0;
    const m = new Matrix4();
    const lay = (from: number, to: number, at: number) => {
      for (let x = from; x < to && n < SLEEPERS; x += SLEEPER_GAP) sleepers.setMatrixAt(n++, m.makeScale(3, 15, 1.5).setPosition(x, at, 0.75));
    };
    lay(-w, 3 * w, y);
    lay(jx + BRANCH_DX + SLEEPER_GAP / 2, 3 * w, by);
    sleepers.count = n;
    sleepers.instanceMatrix.needsUpdate = true;

    fit();
  }

  function apply(s: RouteState) {
    litMain.forEach((m, i) => span(m, nodes[i], y, nodes[i] + (nodes[i + 1] - nodes[i]) * s.wipe[i], y));
    const px = nodes[4];
    const d = s.probe * (sx - px);
    span(litProbeIn, px, y, px + Math.min(d, jx - px), y);
    span(litProbeOut, jx, y, jx + Math.max(0, d - (jx - px)) * s.tail, y);
    const b = s.branch * (diagLen + sx - jx - BRANCH_DX);
    const db = Math.min(b, diagLen) / diagLen;
    span(litDiag, jx, y, jx + BRANCH_DX * db, y - BRANCH_DY * db);
    span(litBranch, jx + BRANCH_DX, y - BRANCH_DY, jx + BRANCH_DX + Math.max(0, b - diagLen), y - BRANCH_DY);
    blade.rotation.z = -angle * (1 - s.points);
    // The blade is a lever, not a lamp: enamel-muted, amber once the route light runs through it.
    const through = d >= jx - px ? s.tail : 0;
    blend(bladeMat.color, LAB.muted, LAB.lamp, Math.max(through, s.branch * 4));

    stations.forEach((st, i) => {
      blend(st.lens.emissive, LAB.off, LAB.lamp, s.lamps[i]);
      st.halo.material.opacity = 0.5 * s.lamps[i] ** 2;
    });
    const red = s.danger > s.proceed;
    const on = Math.max(s.proceed, s.danger);
    blend(signal.lens.emissive, LAB.off, red ? LAB.danger : LAB.proceed, on);
    signal.halo.material.color.copy(red ? C.danger : C.proceed);
    signal.halo.material.opacity = 0.6 * on ** 2;
    blend(rolled.lens.emissive, LAB.off, LAB.lamp, s.rolled);
    rolled.halo.material.opacity = 0.5 * s.rolled ** 2;

    shackle.position.y = LOCK_LIFT * s.lock;
    blend(lockMat.color, LAB.lockOff, LAB.lamp, s.lock);

    pulse.visible = s.pulse >= 0;
    pulse.position.set(px + s.pulse * (sx - px), y, 0);
  }

  /** The route light's leading point: where the camera's attention goes. */
  function focus(s: RouteState): number {
    const px = nodes[4];
    if (s.pulse >= 0) return px + s.pulse * (sx - px);
    if (s.proceed > 0.5 || s.danger > 0.5 || s.branch > 0) return sx;
    for (let i = 3; i >= 0; i--) if (s.lamps[i] > 0.5) return nodes[i + 1];
    return nodes[1];
  }

  // HTML stage labels follow the lamps; touch the DOM only when they change.
  const labelIds = [...STATIONS.map((st) => st.id), "signal", "rolled"];
  const labels = labelIds.map((id) => stage.querySelector<HTMLElement>(`[data-stage="${id}"]`));
  let labelKey = "";
  // probeFailed stays true from the failed probe until the cycle resets, so the
  // signal label never reads "Verified" while the rollback is running.
  function updateLabels(s: RouteState, probeFailed = false) {
    const failed = s.danger > 0.5;
    const lit = [...s.lamps.map((v) => v > 0.5), s.proceed > 0.5 || failed, s.rolled > 0.5];
    const key = `${lit}${probeFailed}`;
    if (key === labelKey) return;
    labelKey = key;
    labels.forEach((el, i) => el?.toggleAttribute("data-lit", lit[i]));
    if (labels[4]) labels[4].textContent = probeFailed ? "Failed" : "Verified";
  }

  const screen = (x: number, yy: number, z = HOUSING_H): [number, number] => {
    v.set(x, yy, z).project(camera);
    return [((v.x + 1) * W) / 2, ((1 - v.y) * H) / 2];
  };
  const shift = (el: HTMLElement | null, dx: number, dy: number) => {
    const t = `translate(${(dx / k).toFixed(1)}px,${(dy / k).toFixed(1)}px)`;
    if (el && el.style.transform !== t) el.style.transform = t;
  };
  // The labels keep their poster layout (CSS) and move by the difference
  // between the projected lamp and the poster's flat one, so they stay attached.
  function placeLabels() {
    [...nodes.slice(1), sx].forEach((x, i) => {
      const R = i < 4 ? HOUSING_R : SIGNAL_HOUSING_R;
      const gap = (i < 4 ? LABEL_STATION : LABEL_SIGNAL) - R;
      const [cx] = screen(x, y);
      const [, top] = screen(x, y + R);
      shift(labels[i], cx - x * k, top - gap * k + (y + gap + R) * k);
    });
    const by = y - BRANCH_DY;
    const [right] = screen(sx + HOUSING_R, by);
    const [, bottom] = screen(sx, by - HOUSING_R);
    shift(labels[5], right - (sx + HOUSING_R) * k, bottom + (y - BRANCH_DY - HOUSING_R) * k);
  }

  let elapsed = 0;
  let last = 0;
  let raf = 0;
  let paused = false;
  let onScreen = true;
  let disposed = false;
  let dolly = NaN; // camera truck along the track, and its velocity
  let dollyV = 0;

  function draw(dt = 0) {
    const s = START + elapsed;
    const cycle = Math.floor(s / CYCLE);
    const t = s - cycle * CYCLE;
    const fail = cycle % 2 === 1;
    const state = routeState(t, fail);
    apply(state);

    const goal = dollyK * (focus(state) - (nodes[1] + sx) / 2);
    if (Number.isNaN(dolly)) dolly = goal;
    dollyV += (SPRING * SPRING * (goal - dolly) - 2 * SPRING * dollyV) * dt;
    dolly += dollyV * dt;
    const e = easeInOut(clamp01((elapsed - ENTER_AT) / ENTER_FOR));
    const d0 = h / 2 / Math.tan((fov * DEG) / 2);
    aim(fov, PITCH * e, yaw * e, lerp(w / 2, rake.x, e) + dolly * e, lerp(-h / 2, rake.y, e), lerp(d0, rake.d, e));
    // Depth fog toward the frame grows with the rake; straight-on there is none.
    fog.near = rake.d * (0.9 + 8 * (1 - e));
    fog.far = rake.d * (2.6 + 8 * (1 - e));
    sleeperMat.color.lerpColors(C.frame, C.sleeper, e);

    renderer.render(scene, camera);
    placeLabels();
    updateLabels(state, fail && t >= 5.8 && t < 10.75);
    if (!("live" in stage.dataset)) {
      stage.dataset.live = "";
      toggle!.hidden = false;
    }
  }

  function frame(now: number) {
    const dt = last ? Math.min((now - last) / 1000, 0.1) : 0;
    elapsed += dt;
    last = now;
    draw(dt);
    raf = requestAnimationFrame(frame);
  }

  function sync() {
    const run = !disposed && !paused && onScreen && !document.hidden;
    if (run && !raf) {
      last = 0;
      raf = requestAnimationFrame(frame);
    } else if (!run && raf) {
      cancelAnimationFrame(raf);
      raf = 0;
    }
  }

  const ac = new AbortController();
  const io = new IntersectionObserver(([e]) => {
    onScreen = e.isIntersecting;
    sync();
  });
  const ro = new ResizeObserver(() => {
    layout();
    if (!raf && !disposed) draw(); // keep a paused frame correct
  });

  // shortcut: a page restored from the back/forward cache keeps the poster, re-mount on pageshow if that matters.
  function dispose() {
    disposed = true;
    sync();
    ac.abort();
    io.disconnect();
    ro.disconnect();
    geometries.forEach((g) => g.dispose());
    materials.forEach((m) => m.dispose());
    sleepers.dispose();
    glowTex.dispose();
    renderer.dispose();
    delete stage.dataset.live;
    toggle!.hidden = true;
    labels.forEach((el) => el?.style.removeProperty("transform"));
    updateLabels(routeState(VERIFIED_HOLD, false));
  }

  toggle.addEventListener(
    "click",
    () => {
      paused = !paused;
      toggleLabel.textContent = paused ? "Play animation" : "Pause animation";
      toggleIcon.setAttribute("d", paused ? "M3 1l8 5-8 5z" : "M2 1h3v10H2zM7 1h3v10H7z");
      sync();
    },
    { signal: ac.signal },
  );
  document.addEventListener("visibilitychange", sync, { signal: ac.signal });
  addEventListener("pagehide", dispose, { signal: ac.signal });
  reduce.addEventListener("change", (e) => e.matches && dispose(), { signal: ac.signal });

  ro.observe(stage);
  io.observe(stage);
}
