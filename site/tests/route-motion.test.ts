import { describe, it } from "node:test";
import assert from "node:assert/strict";
import {
  CYCLE, DEG, VERIFIED_HOLD, cycleAt, follow, mixOklab, oklab, rakeFor, routeState, signalLabel,
  type Lab, type RouteState,
} from "../src/scripts/route-motion.ts";
import { COLORS } from "../src/scripts/route-geometry.ts";

const STEP = 0.005;
const times = Array.from({ length: Math.round(CYCLE / STEP) + 1 }, (_, i) => i * STEP);
// Everything that carries light; lock and points are levers with a deliberate throw overshoot.
const lights = (s: RouteState) => [...s.wipe, ...s.lamps, s.probe, s.tail, s.proceed, s.danger, s.branch, s.rolled];
const firstTime = (fail: boolean, test: (s: RouteState) => boolean) => times.find((t) => test(routeState(t, fail)));

describe("routeState", () => {
  it("keeps the poster-matching timings", () => {
    const s = routeState(5.9, true);
    assert.ok(s.danger > 0.5 && s.proceed === 0 && s.pulse === -1);
    assert.equal(routeState(4.8, false).pulse, 0);
    assert.equal(routeState(1.05, false).lamps[0], 1);
    assert.equal(routeState(7.3, true).tail, 0);
    assert.ok(routeState(11.2, false).wipe.every((w) => w === 0));
  });

  it("a verified cycle clears the signal to proceed and never shows danger or a rollback", () => {
    const states = times.map((t) => routeState(t, false));
    assert.equal(Math.max(...states.map((s) => s.proceed)), 1);
    for (const s of states) assert.deepEqual([s.danger, s.branch, s.rolled], [0, 0, 0]);
    assert.equal(routeState(VERIFIED_HOLD, false).proceed, 1, "the poster's moment is fully green");
  });

  it("a failed cycle shows danger, then rolls back, and never shows proceed", () => {
    const states = times.map((t) => routeState(t, true));
    for (const s of states) assert.equal(s.proceed, 0);
    assert.equal(Math.max(...states.map((s) => s.danger)), 1);
    assert.equal(Math.max(...states.map((s) => s.rolled)), 1);
    const red = firstTime(true, (s) => s.danger > 0.5)!;
    const rolled = firstTime(true, (s) => s.rolled > 0.5)!;
    assert.ok(red < rolled, `danger at ${red}s before rolled back at ${rolled}s`);
    assert.equal(routeState(rolled, true).danger, 0, "the failed route is dropped once the rollback lights");
  });

  for (const fail of [false, true]) {
    it(`keeps every light within [0, 1] and the levers near it (${fail ? "failed" : "verified"} cycle)`, () => {
      for (const t of times) {
        const s = routeState(t, fail);
        for (const v of lights(s)) assert.ok(v >= 0 && v <= 1, `light ${v} at t=${t}`);
        assert.ok(s.pulse === -1 || (s.pulse >= 0 && s.pulse <= 1), `pulse ${s.pulse} at t=${t}`);
        // The throw overshoots by ~11% and settles (DESIGN.md easing); it never runs away.
        for (const v of [s.lock, s.points]) assert.ok(v >= -0.15 && v <= 1.15, `lever ${v} at t=${t}`);
      }
    });

    it(`starts dark and ends dark, so the loop is seamless (${fail ? "failed" : "verified"} cycle)`, () => {
      for (const t of [0, CYCLE]) {
        const s = routeState(t, fail);
        assert.deepEqual(
          { wipe: s.wipe, lamps: s.lamps, lock: s.lock, points: s.points, probe: s.probe, pulse: s.pulse },
          { wipe: [0, 0, 0, 0], lamps: [0, 0, 0, 0], lock: 0, points: 0, probe: 0, pulse: -1 },
          `t=${t}`,
        );
        assert.deepEqual([s.proceed, s.danger, s.branch, s.rolled], [0, 0, 0, 0], `t=${t}`);
      }
    });
  }
});

describe("cycleAt", () => {
  it("opens on the poster's verified frame, then alternates verified and failed cycles", () => {
    assert.deepEqual(cycleAt(0), { t: VERIFIED_HOLD, fail: false });
    const first = CYCLE - VERIFIED_HOLD; // cycle 0 starts here
    assert.deepEqual(cycleAt(first), { t: 0, fail: false });
    assert.deepEqual(cycleAt(first + CYCLE), { t: 0, fail: true });
    assert.deepEqual(cycleAt(first + 2 * CYCLE), { t: 0, fail: false });
  });
});

describe("signalLabel", () => {
  it("reads Failed from the failed probe until the route resets", () => {
    assert.equal(signalLabel(5.79, true), "Verified");
    assert.equal(signalLabel(5.8, true), "Failed");
    assert.equal(signalLabel(10.74, true), "Failed");
    assert.equal(signalLabel(10.75, true), "Verified");
  });

  it("never reads Verified while a failed cycle shows danger or the lit rollback", () => {
    for (const t of times) {
      const s = routeState(t, true);
      // From the reset the rolled-back lamp fades out (release); the label switches back at the reset.
      if (s.danger > 0.5 || s.rolled === 1) assert.equal(signalLabel(t, true), "Failed", `t=${t}`);
    }
  });

  it("always reads Verified in a verified cycle", () => {
    for (const t of times) assert.equal(signalLabel(t, false), "Verified", `t=${t}`);
  });
});

describe("rakeFor (camera fit)", () => {
  const finite = (W: number, H: number) => {
    const { yaw, fov, dollyK } = rakeFor(W, H);
    for (const v of [yaw, fov, dollyK]) assert.ok(Number.isFinite(v), `${W}x${H}: ${v}`);
    assert.ok(fov > 0 && fov < 180, `${W}x${H}: fov ${fov}`);
    return { yaw, fov, dollyK };
  };

  it("a narrow stage turns less and trucks less than a wide one", () => {
    const narrow = finite(390, 600);
    const wide = finite(3600, 700);
    assert.ok(Math.abs(narrow.yaw - -12 * DEG) < 1e-12);
    assert.ok(Math.abs(wide.yaw - -38 * DEG) < 1e-12);
    assert.ok(narrow.dollyK < wide.dollyK);
  });

  it("stays finite for a zero-size stage", () => {
    finite(0, 0);
    finite(800, 0);
    finite(0, 600);
  });
});

describe("follow (camera truck)", () => {
  const run = (goals: number[], dt = 1 / 60) => {
    let x = NaN;
    let v = 0;
    for (const g of goals) [x, v] = follow(x, v, g, dt);
    return [x, v];
  };

  it("places the camera on the first goal, then settles on a new one without overshoot", () => {
    assert.deepEqual(follow(NaN, 0, 40, 1 / 60), [40, 0]);
    let x = 0;
    let v = 0;
    for (let i = 0; i < 600; i++) {
      [x, v] = follow(x, v, 100, 1 / 60);
      assert.ok(x <= 100, `overshoot ${x}`);
    }
    assert.ok(Math.abs(x - 100) < 1, `settled at ${x}`);
  });

  it("does not latch on a NaN or Infinity goal from a zero-size stage", () => {
    for (const bad of [NaN, Infinity, -Infinity]) {
      const [x, v] = run([bad, bad, ...Array(600).fill(100)]);
      assert.ok(Number.isFinite(x) && Number.isFinite(v), `after ${bad}: ${x}, ${v}`);
      assert.ok(Math.abs(x - 100) < 1, `after ${bad}: settled at ${x}`);
      const [y, w] = run([50, bad, ...Array(600).fill(100)]);
      assert.ok(Number.isFinite(y) && Number.isFinite(w), `mid-flight ${bad}: ${y}, ${w}`);
    }
  });
});

describe("mixOklab (lamp blend)", () => {
  const encode = (c: number) => Math.round(255 * (c <= 0.0031308 ? 12.92 * c : 1.055 * c ** (1 / 2.4) - 0.055));
  const hex = (rgb: Lab) => `#${rgb.map((c) => encode(c).toString(16).padStart(2, "0")).join("")}`;
  const lch = ([L, a, b]: Lab) => ({ L, C: Math.hypot(a, b), h: Math.atan2(b, a) / DEG });
  const pairs = [
    ["lampOff", "lamp"],
    ["lampOff", "proceed"],
    ["lampOff", "danger"],
    ["enamelMuted", "lamp"],
  ] as const;

  it("returns the exact token colours at both ends (and clamps t)", () => {
    for (const [a, b] of pairs) {
      const from = oklab(COLORS[a]);
      const to = oklab(COLORS[b]);
      assert.equal(hex(mixOklab(from, to, 0)), COLORS[a]);
      assert.equal(hex(mixOklab(from, to, 1)), COLORS[b]);
      assert.equal(hex(mixOklab(from, to, -1)), COLORS[a]);
      assert.equal(hex(mixOklab(from, to, 2)), COLORS[b]);
    }
  });

  it("passes a lighting lamp through a darker amber, not an olive grey", () => {
    const lamp = lch(oklab(COLORS.lamp));
    const mid = lch(oklab(hex(mixOklab(oklab(COLORS.lampOff), oklab(COLORS.lamp), 0.5))));
    assert.ok(Math.abs(mid.h - lamp.h) < 3, `hue ${mid.h.toFixed(1)}° vs lamp ${lamp.h.toFixed(1)}°`);
    assert.ok(mid.C > 0.4 * lamp.C, `chroma ${mid.C.toFixed(3)} vs lamp ${lamp.C.toFixed(3)}`);
    assert.ok(mid.L < lamp.L, "darker than the lit lamp");
  });
});
