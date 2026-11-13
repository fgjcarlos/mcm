// The hero scene's motion without three or the DOM: the story cycle, the
// camera's rake and follow, and the OKLab lamp blend. route-scene.ts draws
// with it; tests/ check it. Keep this module free of imports and globals.

// ---- Story: one cycle, in seconds. Odd cycles fail the probe and roll back.

export const CYCLE = 11.5;
export const VERIFIED_HOLD = 8; // a moment in a verified cycle that matches the poster
const START = VERIFIED_HOLD - CYCLE; // cycle -1 = the poster's state; cycle 0 runs the full story
const PROBE_DONE = 5.8; // the signal clears (or goes red) here
const RESET = 10.75; // the route is released and the points thrown back

export const clamp01 = (p: number) => Math.min(1, Math.max(0, p));
const easeOut = (p: number) => 1 - (1 - p) ** 3;
export const easeInOut = (p: number) => (p < 0.5 ? 4 * p ** 3 : 1 - (-2 * p + 2) ** 3 / 2);
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
  const reset = 1 - at(t, RESET, 0.5, thrown);
  const probe = at(t, 4.8, 1, easeInOut);
  const s: RouteState = {
    wipe: [at(t, 0.3, 0.5), at(t, 1.2, 0.5), at(t, 2.5, 0.5), at(t, 3.8, 0.5)].map((w) => w * release),
    lamps: [0.8, 2.15, 3.0, 4.3].map((start) => at(t, start, 0.15) * release),
    lock: at(t, 1.8, 0.45, thrown) * reset,
    points: at(t, 3.0, 0.5, thrown) * reset,
    probe: probe * release,
    tail: 1,
    pulse: t >= 4.8 && t < PROBE_DONE ? probe : -1,
    proceed: 0,
    danger: 0,
    branch: 0,
    rolled: 0,
  };
  if (fail) {
    const drop = at(t, 7.0, 0.3);
    s.danger = at(t, PROBE_DONE, 0.12) * (1 - drop);
    s.tail = 1 - drop;
    s.points = at(t, 3.0, 0.5, thrown) * (1 - at(t, 7.2, 0.5, thrown));
    s.branch = at(t, 7.7, 0.5) * release;
    s.rolled = at(t, 8.2, 0.15) * release;
  } else {
    s.proceed = at(t, PROBE_DONE, 0.2) * release;
  }
  return s;
}

/** Where the story is `elapsed` seconds after mount: it starts on the poster's verified frame. */
export function cycleAt(elapsed: number): { t: number; fail: boolean } {
  const s = START + elapsed;
  const cycle = Math.floor(s / CYCLE);
  return { t: s - cycle * CYCLE, fail: cycle % 2 === 1 };
}

/** The signal label: "Failed" from the failed probe until the route resets, so it never reads "Verified" during a rollback. */
export function signalLabel(t: number, fail: boolean): "Failed" | "Verified" {
  return fail && t >= PROBE_DONE && t < RESET ? "Failed" : "Verified";
}

// ---- Camera

export const DEG = Math.PI / 180;
export const DOLLY = 0.16; // truck as a fraction of the route light's distance from the route's middle
const SPRING = 1.6; // rad/s, critically damped: slow, no overshoot

/** The rake for a W×H stage (CSS px): a wide stage turns harder; a narrow one turns less and trucks less. */
export function rakeFor(W: number, H: number): { yaw: number; fov: number; dollyK: number } {
  // A zero-size stage (hidden, not laid out yet) gets the narrow rake instead of NaN or a 0°/180° field of view.
  const aspect = W > 0 && H > 0 ? W / H : 2;
  const q = clamp01((aspect - 2) / 3);
  return {
    yaw: -(12 + 26 * q) * DEG,
    // vertical, from a horizontal field of view
    fov: (2 * Math.atan(Math.tan(((45 + 25 * q) * DEG) / 2) / aspect)) / DEG,
    dollyK: DOLLY * (0.4 + 0.6 * q),
  };
}

/** One critically damped step of the camera truck (x, velocity v) toward goal; NaN x means "not placed yet". */
export function follow(x: number, v: number, goal: number, dt: number): [number, number] {
  // A goal from a zero-size stage is NaN or infinite: hold, so it cannot latch into x or v.
  if (!Number.isFinite(goal)) return [x, v];
  if (!Number.isFinite(x) || !Number.isFinite(v)) [x, v] = [goal, 0];
  v += (SPRING * SPRING * (goal - x) - 2 * SPRING * v) * dt;
  return [x + v * dt, v];
}

// ---- Colour

export type Lab = [number, number, number];
/** "#rrggbb" → gamma-encoded sRGB channels in [0, 1]. */
export const srgb = (hex: string) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255) as Lab;

export function oklab(hex: string): Lab {
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

/** Blends in OKLab and returns linear sRGB: a lighting lamp passes through a darker amber (or green, red), never an olive grey. */
export function mixOklab(from: Lab, to: Lab, t: number): Lab {
  const [L, A, B] = from.map((v, i) => v + (to[i] - v) * clamp01(t));
  const l = (L + 0.3963377774 * A + 0.2158037573 * B) ** 3;
  const m = (L - 0.1055613458 * A - 0.0638541728 * B) ** 3;
  const s = (L - 0.0894841775 * A - 1.291485548 * B) ** 3;
  return [
    Math.max(0, 4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s),
    Math.max(0, -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s),
    Math.max(0, -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s),
  ];
}
