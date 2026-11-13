# Brand asset provenance

All brand assets are original work for MCM. Nothing below uses or derives from the Eclipse Foundation or Eclipse Mosquitto logos or their motifs.

## Vector sources (hand-authored)

| File | What it is |
| --- | --- |
| `mark.svg` | The mark. It shows a route set through a set of points, cleared by a lit signal lamp. It uses one 3-unit stroke on a 32-unit grid. |
| `lockup.svg` | The mark plus "MCM" letterforms drawn as paths at the same stroke weight, so it needs no font. |
| `../../../public/favicon.svg` | The mark on a `#111214` rounded square, so it stays visible on light browser chrome. |
| `og.html` | The source of the social card. Its fonts come from the self-hosted Fontsource packages in `node_modules`. |

## Shipped rasters

| File | Size | Produced from | How |
| --- | --- | --- | --- |
| `site/public/og.png` | 1200×630 | `og.html` | Screenshot with headless Chromium. |
| `site/public/favicon-32.png` | 32×32 (RGBA) | `public/favicon.svg` | Screenshot with a transparent background, as a fallback for browsers without SVG favicons. |

To render, install `playwright` into a throwaway `mktemp -d` directory, not into the project. Then launch `chrome-headless-shell` (Playwright Chromium build 1248) and run these steps:

1. Set the viewport to the target size.
2. Open `file://…/site/src/assets/brand/og.html`, or for the favicon, an HTML page whose only content is `favicon.svg` at 32×32.
3. Wait for `document.fonts.ready`.
4. Take the screenshot with `page.screenshot`. For the favicon, pass `omitBackground: true`.

The rasters were first rendered on 2026-11-12. `og.png` was rendered again on 2026-11-13, after `og.html` changed its brand name to the binding "Mosquitto Control Manager (MCM)". If you change any source, render its PNG again.

Status colours in the og diagram: the green lamp marks the verified outcome, and the amber ring marks the rolled-back outcome.
