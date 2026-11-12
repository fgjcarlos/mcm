// Hero bootstrap: keeps three out of the initial load. The scene module (and
// three with it) is imported only when motion is allowed, WebGL exists, the
// page has loaded and gone idle, and the hero is on screen.

function hasWebGL(): boolean {
  try {
    const gl = document.createElement("canvas").getContext("webgl2");
    gl?.getExtension("WEBGL_lose_context")?.loseContext();
    return !!gl;
  } catch {
    return false;
  }
}

const loadedAndIdle = () =>
  new Promise<void>((resolve) => {
    const idle = () =>
      "requestIdleCallback" in window ? requestIdleCallback(() => resolve(), { timeout: 2000 }) : setTimeout(resolve, 300);
    if (document.readyState === "complete") idle();
    else addEventListener("load", idle, { once: true });
  });

const onScreen = (el: Element) =>
  new Promise<void>((resolve) => {
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) {
        io.disconnect();
        resolve();
      }
    });
    io.observe(el);
  });

export async function bootSignalHero(): Promise<void> {
  const stage = document.querySelector<HTMLElement>("[data-signal-hero]");
  const reduce = matchMedia("(prefers-reduced-motion: reduce)");
  if (!stage || reduce.matches || !hasWebGL()) return;

  await Promise.all([loadedAndIdle(), onScreen(stage)]);
  if (reduce.matches) return;
  const { mountRouteScene } = await import("./route-scene");
  mountRouteScene(stage, reduce);
}
