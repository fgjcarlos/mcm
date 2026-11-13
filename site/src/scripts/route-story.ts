// Scroll story: lights the sticky route stage for the step crossing the
// viewport centre. IntersectionObserver only; native scrolling is untouched.
// The amber travelling along the track between steps is CSS scroll-driven
// animation (global.css, data-scrub); without it, the lit track steps here too.
// With reduced motion (or no JS) the stage stays hidden and every step keeps
// its own static diagram.

const SCRAMBLE_MS = 360;
const GLYPHS = "ABCDEFHKLMNPRSTUVXYZ0123456789#/+=";
const runs = new WeakMap<Element, number>();

/** Decodes a lighting label into its word; only the aria-hidden glyph span changes. */
function scramble(el: Element, word: string): void {
  const start = performance.now();
  runs.set(el, start);
  const frame = (now: number) => {
    if (runs.get(el) !== start) return;
    const t = Math.min(1, (now - start) / SCRAMBLE_MS);
    const settled = Math.floor(t * word.length);
    el.textContent =
      word.slice(0, settled) + word.slice(settled).replace(/\S/g, () => GLYPHS[(Math.random() * GLYPHS.length) | 0]);
    if (t < 1) requestAnimationFrame(frame);
  };
  requestAnimationFrame(frame);
}

/** The step to light when step `crossing` (an index, -1 if none) reaches the centre line, or null to keep `current`. */
export function pickStep(current: number, crossing: number, count: number): number | null {
  return Number.isInteger(crossing) && crossing >= 0 && crossing < count && crossing !== current ? crossing : null;
}

/** Whether a stage label lights in a step; `on` lists its steps as digits (data-label-on, STATIONS.lit). */
export const labelLit = (on: string, step: number) => on.includes(String(step));

export function bootRouteStory(): void {
  const story = document.querySelector<HTMLElement>("[data-route-story]");
  const stage = story?.querySelector<HTMLElement>("[data-route-stage]");
  if (!story || !stage) return;

  const reduce = matchMedia("(prefers-reduced-motion: reduce)");
  const wide = matchMedia("(min-width: 64rem)");
  const steps = [...story.querySelectorAll<HTMLElement>("[data-story-step]")];
  const labels = [...stage.querySelectorAll<HTMLElement>("[data-label-on]")].map((li) => {
    const glyphs = li.querySelector("[data-glyphs]")!;
    return { li, glyphs, word: glyphs.textContent ?? "" };
  });
  let current = 0;
  steps[0]?.setAttribute("data-active", "");

  const show = (crossing: number) => {
    const i = pickStep(current, crossing, steps.length);
    if (i === null) return;
    current = i;
    stage.dataset.routeStep = String(i);
    steps.forEach((s, k) => s.toggleAttribute("data-active", k === i));
    const animate = wide.matches && !reduce.matches;
    for (const { li, glyphs, word } of labels) {
      const lit = labelLit(li.dataset.labelOn!, i);
      if (lit && animate && !li.hasAttribute("data-lit")) scramble(glyphs, word);
      li.toggleAttribute("data-lit", lit);
    }
  };

  const live = () => story.toggleAttribute("data-live", !reduce.matches);
  live();
  reduce.addEventListener("change", live);

  // A zero-height root on the centre line: exactly one step crosses it.
  const io = new IntersectionObserver(
    (entries) => {
      for (const e of entries) if (e.isIntersecting) show(steps.indexOf(e.target as HTMLElement));
    },
    { rootMargin: "-50% 0px -50% 0px" },
  );
  for (const s of steps) io.observe(s);
}
