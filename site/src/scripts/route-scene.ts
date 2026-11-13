// The live route diagram. Loaded only through dynamic import() from
// signal-hero.ts; nothing else may import this module or three.
import {
  AdditiveBlending,
  CanvasTexture,
  CircleGeometry,
  Color,
  Group,
  Mesh,
  MeshBasicMaterial,
  OrthographicCamera,
  PlaneGeometry,
  RingGeometry,
  Scene,
  Sprite,
  SpriteMaterial,
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

// ---- Scene

function glowTexture(): CanvasTexture {
  const c = document.createElement("canvas");
  c.width = c.height = 64;
  const g = c.getContext("2d")!;
  const grad = g.createRadialGradient(32, 32, 0, 32, 32, 32);
  grad.addColorStop(0, "rgb(255 255 255 / 1)");
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
  const camera = new OrthographicCamera(0, 1, 0, -1, -10, 10);
  const geometries: BufferGeometry[] = [];
  const materials: Material[] = [];
  const geo = <T extends BufferGeometry>(g: T) => (geometries.push(g), g);
  const plane = geo(new PlaneGeometry(1, 1));
  const bladeGeo = geo(new PlaneGeometry(1, 1).translate(0.5, 0, 0));
  const disc = geo(new CircleGeometry(1, 48));
  const housingGeo = geo(new RingGeometry(HOUSING_R - 0.75, HOUSING_R + 0.75, 48));
  const signalHousingGeo = geo(new RingGeometry(SIGNAL_HOUSING_R - 0.75, SIGNAL_HOUSING_R + 0.75, 48));
  const shackleGeo = geo(new RingGeometry(3.5, 5.5, 24, 1, 0, Math.PI));
  const glowTex = glowTexture();

  const C = {
    enamel: new Color(COLORS.enamel),
    lamp: new Color(COLORS.lamp),
    proceed: new Color(COLORS.proceed),
    danger: new Color(COLORS.danger),
    off: new Color(COLORS.lampOff),
  };
  // All transparent: three draws opaque before transparent, and the glows (sprites)
  // must stay under the lamp housings, so one list ordered by renderOrder.
  const basic = (color: string, opacity = 1) => {
    const m = new MeshBasicMaterial({ color, opacity, transparent: true, depthTest: false, depthWrite: false });
    materials.push(m);
    return m;
  };
  const mesh = (g: BufferGeometry, m: Material, order: number, parent: Object3D = scene) => {
    const o = new Mesh(g, m);
    o.renderOrder = order;
    parent.add(o);
    return o;
  };
  const glow = (color: string, size: number, parent: Object3D) => {
    const m = new SpriteMaterial({ map: glowTex, color, blending: AdditiveBlending, depthTest: false, depthWrite: false });
    materials.push(m);
    const s = new Sprite(m);
    s.scale.set(size, size, 1);
    s.renderOrder = 3;
    parent.add(s);
    return s;
  };
  // A lamp: glow, frame-coloured backing that hides the track, housing ring, lens.
  const lamp = (r: number, housingR: number, housing: RingGeometry, glowColor: string) => {
    const g = new Group();
    scene.add(g);
    const lens = basic(COLORS.lampOff);
    const halo = glow(glowColor, r * 6, g);
    mesh(disc, basic(COLORS.frame), 4, g).scale.setScalar(housingR);
    mesh(housing, basic(COLORS.enamel, 0.35), 5, g);
    mesh(disc, lens, 6, g).scale.setScalar(r);
    return { g, lens, halo };
  };

  const track = basic(COLORS.enamel, 0.28);
  const lit = basic(COLORS.lamp);
  const baseMainIn = mesh(plane, track, 0);
  const baseMainOut = mesh(plane, track, 0);
  const baseDiag = mesh(plane, track, 0);
  const baseBranch = mesh(plane, track, 0);
  const litMain = [0, 1, 2, 3].map(() => mesh(plane, lit, 1));
  const litProbeIn = mesh(plane, lit, 1);
  const litProbeOut = mesh(plane, lit, 1);
  const litDiag = mesh(plane, lit, 1);
  const litBranch = mesh(plane, lit, 1);
  const blade = mesh(bladeGeo, basic(COLORS.enamel), 2);
  blade.scale.set(BLADE, TRACK_W, 1);

  const stations = STATIONS.map(() => lamp(LAMP_R, HOUSING_R, housingGeo, COLORS.lamp));
  const signal = lamp(SIGNAL_R, SIGNAL_HOUSING_R, signalHousingGeo, COLORS.proceed);
  const rolled = lamp(LAMP_R, HOUSING_R, housingGeo, COLORS.lamp);

  const lockMat = basic(COLORS.enamel, 0.6);
  const lock = new Group();
  stations[1].g.add(lock);
  const body = mesh(plane, lockMat, 7, lock);
  body.scale.set(14, 11, 1);
  body.position.set(0, -LOCK_Y - 5.5, 0);
  const shackle = new Group();
  lock.add(shackle);
  mesh(shackleGeo, lockMat, 7, shackle).position.set(0, -LOCK_Y + 5, 0);
  for (const x of [-4.5, 4.5]) {
    const leg = mesh(plane, lockMat, 7, shackle);
    leg.scale.set(2, 5, 1);
    leg.position.set(x, -LOCK_Y + 2.5, 0);
  }

  const pulse = new Group();
  scene.add(pulse);
  glow(COLORS.lamp, 28, pulse);
  mesh(disc, lit, 8, pulse).scale.setScalar(4);

  // Lay a unit plane along a→b (three coordinates, y up).
  const span = (m: Mesh, ax: number, ay: number, bx: number, by: number) => {
    const len = Math.hypot(bx - ax, by - ay);
    m.visible = len > 0.01;
    m.position.set((ax + bx) / 2, (ay + by) / 2, 0);
    m.rotation.z = Math.atan2(by - ay, bx - ax);
    m.scale.set(len, TRACK_W, 1);
  };

  const angle = Math.atan2(BRANCH_DY, BRANCH_DX);
  const diagLen = Math.hypot(BRANCH_DX, BRANCH_DY);
  let w = 0;
  let y = 0; // track centre line
  let nodes: number[] = []; // edge, review, MFA, apply, probe
  let jx = 0;
  let sx = 0;

  function layout() {
    const s = stage.getBoundingClientRect();
    const r = rail!.getBoundingClientRect();
    // The rail is drawn at --route-scale (route-rail, global.css): lay the scene
    // out in unscaled rail px and let the camera scale it like the poster.
    const k = r.width / rail!.offsetWidth || 1;
    w = s.width / k;
    const h = s.height / k;
    const lowPower = matchMedia("(pointer: coarse)").matches || innerWidth < 768;
    renderer.setPixelRatio(lowPower ? 1 : Math.min(devicePixelRatio, 1.5));
    renderer.setSize(s.width, s.height, false);
    camera.right = w;
    camera.bottom = -h;
    camera.updateProjectionMatrix();

    const ox = (r.left - s.left) / k;
    const railW = r.width / k;
    y = -(r.top - s.top) / k;
    nodes = [0, ...STATIONS.map((st) => ox + st.x * railW)];
    jx = ox + JUNCTION_X * railW;
    sx = ox + SIGNAL_X * railW;
    const by = y - BRANCH_DY;

    span(baseMainIn, 0, y, jx, y);
    span(baseMainOut, jx + BLADE, y, w, y);
    span(baseDiag, jx + BLADE * Math.cos(angle), y - BLADE * Math.sin(angle), jx + BRANCH_DX, by);
    span(baseBranch, jx + BRANCH_DX, by, w, by);
    blade.position.set(jx, y, 0);
    stations.forEach((st, i) => st.g.position.set(nodes[i + 1], y, 0));
    signal.g.position.set(sx, y, 0);
    rolled.g.position.set(sx, by, 0);
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

    stations.forEach((st, i) => {
      st.lens.color.copy(C.off).lerp(C.lamp, s.lamps[i]);
      st.halo.material.opacity = 0.5 * s.lamps[i];
    });
    signal.lens.color.copy(C.off).lerp(C.proceed, s.proceed).lerp(C.danger, s.danger);
    signal.halo.material.color.copy(s.danger > s.proceed ? C.danger : C.proceed);
    signal.halo.material.opacity = 0.6 * Math.max(s.proceed, s.danger);
    rolled.lens.color.copy(C.off).lerp(C.lamp, s.rolled);
    rolled.halo.material.opacity = 0.5 * s.rolled;

    const released = clamp01(s.lock);
    shackle.position.y = LOCK_LIFT * s.lock;
    lockMat.color.copy(C.enamel).lerp(C.lamp, released);
    lockMat.opacity = 0.6 + 0.4 * released;

    pulse.visible = s.pulse >= 0;
    pulse.position.set(px + s.pulse * (sx - px), y, 0);
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

  let elapsed = 0;
  let last = 0;
  let raf = 0;
  let paused = false;
  let onScreen = true;
  let disposed = false;

  function draw() {
    const s = START + elapsed;
    const cycle = Math.floor(s / CYCLE);
    const t = s - cycle * CYCLE;
    const fail = cycle % 2 === 1;
    const state = routeState(t, fail);
    apply(state);
    renderer.render(scene, camera);
    updateLabels(state, fail && t >= 5.8 && t < 10.75);
    if (!("live" in stage.dataset)) {
      stage.dataset.live = "";
      toggle!.hidden = false;
    }
  }

  function frame(now: number) {
    if (last) elapsed += Math.min((now - last) / 1000, 0.1);
    last = now;
    draw();
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
    glowTex.dispose();
    renderer.dispose();
    delete stage.dataset.live;
    toggle!.hidden = true;
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
