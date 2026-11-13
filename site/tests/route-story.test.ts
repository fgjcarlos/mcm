import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { labelLit, pickStep } from "../src/scripts/route-story.ts";
import { STATIONS, STORY_STEPS } from "../src/scripts/route-geometry.ts";

const COUNT = STORY_STEPS.length;

describe("pickStep", () => {
  it("lights the step that crosses the centre line, first to last and back", () => {
    let current = 0;
    for (const crossing of [1, 2, 3, 2, 1, 0]) {
      const next = pickStep(current, crossing, COUNT);
      assert.equal(next, crossing);
      current = next!;
    }
  });

  it("selects the first and the last step", () => {
    assert.equal(pickStep(2, 0, COUNT), 0);
    assert.equal(pickStep(0, COUNT - 1, COUNT), COUNT - 1);
  });

  it("keeps the current step when it crosses again", () => {
    assert.equal(pickStep(2, 2, COUNT), null);
  });

  it("keeps the current step for out-of-range input", () => {
    for (const crossing of [-1, COUNT, 99, 1.5, NaN]) assert.equal(pickStep(1, crossing, COUNT), null, `${crossing}`);
  });
});

describe("labelLit", () => {
  it("lights each station label in the steps its lamp is on", () => {
    const lit = (id: string) => {
      const on = STATIONS.find((s) => s.id === id)!.lit;
      return STORY_STEPS.map((_, i) => labelLit(on, i));
    };
    assert.deepEqual(lit("review"), [true, true, true, true]);
    assert.deepEqual(lit("mfa"), [false, true, true, true]);
    assert.deepEqual(lit("probe"), [false, false, true, true]);
  });

  it("lights Verified only at verify and Failed / Rolled back only at recover", () => {
    assert.deepEqual(STORY_STEPS.map((_, i) => labelLit("2", i)), [false, false, true, false]);
    assert.deepEqual(STORY_STEPS.map((_, i) => labelLit("3", i)), [false, false, false, true]);
  });
});
