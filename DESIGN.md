---
name: Mosquitto Control Manager (MCM)
description: A night signal box for one Mosquitto broker. A configuration change is a route that cannot clear until it is proven.
colors:
  frame: "#111214"
  frame-raised: "#191a1d"
  frame-high: "#212327"
  enamel-bright: "#f2ede2"
  enamel: "#d8d2c4"
  enamel-soft: "#b9b3a6"
  enamel-muted: "#9a9488"
  lamp: "#e0a526"
  proceed: "#2f9e5b"
  danger: "#c8341f"
  danger-text: "#ec6b55"
  lever-blue: "#1f5fa8"
  track: "rgb(216 210 196 / 0.14)"
  track-strong: "rgb(216 210 196 / 0.28)"
  track-bright: "rgb(216 210 196 / 0.5)"
  status-verified: "#2f9e5b"
  status-rolled-back: "#e0a526"
  status-failed: "#c8341f"
  status-failed-text: "#ec6b55"
typography:
  display:
    fontFamily: "Big Shoulders Variable, Arial Narrow, ui-sans-serif, sans-serif"
    fontSize: "5.5rem"
    fontWeight: 700
    lineHeight: 0.92
  headline:
    fontFamily: "Big Shoulders Variable, Arial Narrow, ui-sans-serif, sans-serif"
    fontSize: "3rem"
    fontWeight: 700
    lineHeight: 0.95
  title-step:
    fontFamily: "Big Shoulders Variable, Arial Narrow, ui-sans-serif, sans-serif"
    fontSize: "2.25rem"
    fontWeight: 700
    lineHeight: 1
  title:
    fontFamily: "Big Shoulders Variable, Arial Narrow, ui-sans-serif, sans-serif"
    fontSize: "1.5rem"
    fontWeight: 600
    letterSpacing: "0.04em"
  label-plate:
    fontFamily: "Big Shoulders Variable, Arial Narrow, ui-sans-serif, sans-serif"
    fontSize: "1rem"
    fontWeight: 700
    lineHeight: 1
    letterSpacing: "0.14em"
  label-step:
    fontFamily: "Big Shoulders Variable, Arial Narrow, ui-sans-serif, sans-serif"
    fontSize: "1.125rem"
    fontWeight: 600
    letterSpacing: "0.08em"
  lede:
    fontFamily: "Overpass Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1.125rem"
    fontWeight: 400
    lineHeight: 1.625
  body:
    fontFamily: "Overpass Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1rem"
    fontWeight: 400
    lineHeight: 1.625
  caption:
    fontFamily: "Overpass Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.625
  mono:
    fontFamily: "Overpass Mono Variable, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.625
  mono-label:
    fontFamily: "Overpass Mono Variable, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace"
    fontSize: "0.75rem"
    fontWeight: 400
    letterSpacing: "0.16em"
rounded:
  plate: "2px"
  lever: "3px"
  lamp: "9999px"
spacing:
  gutter: "1.5rem"
  container: "72rem"
  heading-to-content: "3rem"
  step: "4rem"
  section: "7rem"
  section-wide: "9rem"
components:
  lever-primary:
    backgroundColor: "{colors.enamel}"
    textColor: "{colors.frame}"
    typography: "{typography.label-plate}"
    rounded: "{rounded.lever}"
    padding: "0 24px"
    height: "48px"
  lever-primary-hover:
    backgroundColor: "{colors.enamel-bright}"
    textColor: "{colors.frame}"
  lever-primary-active:
    backgroundColor: "{colors.enamel-soft}"
    textColor: "{colors.frame}"
  lever-ghost:
    backgroundColor: "{colors.frame}"
    textColor: "{colors.enamel}"
    typography: "{typography.label-plate}"
    rounded: "{rounded.lever}"
    padding: "0 24px"
    height: "48px"
  lever-ghost-hover:
    backgroundColor: "{colors.frame-raised}"
    textColor: "{colors.enamel-bright}"
  plate-neutral:
    backgroundColor: "{colors.frame-high}"
    textColor: "{colors.enamel}"
    rounded: "{rounded.plate}"
    padding: "5px 10px"
  plate-lamp:
    backgroundColor: "{colors.lamp}"
    textColor: "{colors.frame}"
    rounded: "{rounded.plate}"
    padding: "5px 10px"
  plate-blue:
    backgroundColor: "{colors.lever-blue}"
    textColor: "{colors.enamel-bright}"
    rounded: "{rounded.plate}"
    padding: "5px 10px"
  command-number:
    backgroundColor: "{colors.enamel}"
    textColor: "{colors.frame}"
    rounded: "{rounded.plate}"
    size: "48px"
  command-block:
    backgroundColor: "{colors.frame-raised}"
    textColor: "{colors.enamel-bright}"
    typography: "{typography.mono}"
    padding: "12px 16px"
  control-small:
    backgroundColor: "{colors.frame}"
    textColor: "{colors.enamel}"
    typography: "{typography.mono-label}"
    rounded: "{rounded.plate}"
    padding: "0 12px"
    height: "36px"
  status-lamp:
    textColor: "{colors.enamel-bright}"
    rounded: "{rounded.lamp}"
    size: "10px"
  clearance-lamp:
    backgroundColor: "{colors.frame-high}"
    rounded: "{rounded.lamp}"
    size: "12px"
---

# Design System: Mosquitto Control Manager (MCM)

## Overview

**Creative North Star: "La Cabina de Enclavamiento" (the interlocking signal box)**

MCM is drawn as a signal box at night. In an interlocking, a route cannot clear until every lever and point along it is proven, and that is how MCM treats a configuration change. Review, MFA, apply and probe light lamp by lamp along a track. The route then holds green (verified) or falls back to the previous one (rolled back). The page is the frame of the box: near-black ground, enamel lever plates with engraved condensed capitals, hairline track diagrams as the structural line, and lamps that light only when something is true.

The tone is a control room, cinematic and precise. It is dark because a signal box works in the dark, so the lamps are the brightest things in the room. Density is low and every section reads like a panel of the frame: a track rule under the heading, the content set out along it. Nothing is a card and nothing is glass. Real captures of the running app are the only imagery.

Motion and 3D are part of the world, and they are allowed freely, with one condition: they report a state. The hero runs the route as a live WebGL scene, and the story section relights the same route as you scroll. Every animated surface has a static twin, so the page reads the same with motion off, without WebGL and before any script runs. `/system` (linked from the footer as "Brand system") is the living storybook: every token, component and state on this page is rendered there from the same source.

**Key Characteristics:**

- Near-black frame ground with enamel text and plates. There are no gradients on surfaces and no glass.
- Lamps carry meaning: amber means a route is set (or caution, or rolled back), green means verified, red means failed.
- Big Shoulders engraved capitals for display and plates, Overpass for reading, Overpass Mono for commands, data and diagram labels.
- Hairline track rules and route diagrams are the structure. Sections never sit in cards, and no label sits above a heading.
- One route geometry (`site/src/scripts/route-geometry.ts`) drives the SVG poster, the WebGL scene, the hero labels and the story stage.
- Motion is free, but it always reports a state and always has a poster, reduced-motion and no-WebGL equivalent.

## Colors

Source of truth: the `@theme` block in `site/src/styles/global.css`. Contrast on the frame (WCAG 2.x) is computed live on `/system` from that file.

### Primary

- **Enamel** (`enamel`): body text on the frame (12.4:1) and the face of the primary lever plate and the command-step numbers.
- **Enamel Bright** (`enamel-bright`): headings, the hover state of the primary lever, code text and the focus ring.

### Secondary: the lamps

- **Route Amber** (`lamp`): a set route. The lit track and station lamps of the diagram, the mark's set route, the Alpha plate, the CautionPanel (hazard stripe, 45% border, 6% tint), the rolled-back status and the text selection. 8.6:1 on the frame.
- **Proceed Green** (`proceed`, aliased by `status-verified`): verified, and nothing else. The verified signal lamp and the verified StatusLamp. 5.5:1 on the frame.
- **Signal Red** (`danger`, aliased by `status-failed`): failed, and nothing else. The failed signal lamp and the failed StatusLamp. At 3.5:1 it is a lamp and fill colour only.
- **Signal Red Text** (`danger-text`, aliased by `status-failed-text`): the readable form of red for text under 24px, such as the story's "Failed" label. 6.1:1.

### Tertiary

- **Lever Blue** (`lever-blue`): the blue member of the lever-colour set, used only as the fill of the blue Plate with enamel-bright text (5.5:1). At 2.9:1 on the frame it is never text.

### Neutral

- **Frame** (`frame`): the page ground, the browser `theme-color`, the WebGL clear colour, and the text on enamel and amber plates.
- **Frame Raised** (`frame-raised`): command blocks and ghost-lever hover.
- **Frame High** (`frame-high`): the neutral plate, ghost-lever active, and unlit lamp lenses.
- **Enamel Soft** (`enamel-soft`): ledes, body copy and captions (9.0:1).
- **Enamel Muted** (`enamel-muted`): secondary notes, footer, unlit diagram labels, the scrollbar thumb (6.2:1).
- **Track** (`track`): hairline dividers and section rules.
- **Track Strong** (`track-strong`): borders of command blocks, figures and small controls; the unset rail of the diagram.
- **Track Bright** (`track-bright`): link underlines and hover borders.

### Named Rules

**The Proven Green Rule.** Green appears only where a change has been verified. It is never a "success" accent, a CTA colour or decoration. If nothing was verified, nothing is green.

**The Red Means Failed Rule.** Red appears only for a failed apply or probe. It never signals emphasis, deletion or a warning. Use `danger-text` for any red text under 24px.

**The Amber Route Rule.** Amber says "a route is set": the lit track, a caution that applies before you act, and the route back after a rollback. It is never a generic highlight.

**The Colour Is Never Alone Rule.** Every lamp state also has a word: StatusLamp always renders its label, pending is a hollow ring, and the diagram labels say Verified, Failed or Rolled back.

**The Not Yet Cleared Rule.** Every limit, planned piece and deferred area is said once, as an unlit ClearanceRow in the one Not yet cleared list; the CautionPanel keeps only the before-you-apply warning and its links.

## Typography

**Display Font:** Big Shoulders Variable, with an optical-size axis (opsz) that reaches its Display cut at 72px (fallback Arial Narrow, then the system sans)
**Body Font:** Overpass Variable (fallback: system sans)
**Label/Mono Font:** Overpass Mono Variable (fallback: system monospace)

All three are self-hosted through Fontsource; nothing loads from a third party.

**Character:** Big Shoulders is the engraved lettering of a lever plate: condensed capitals, tight and loud. Overpass, from highway signage, does the reading at a relaxed 1.625 leading. Overpass Mono is reserved for what a terminal or an instrument would print.

### Hierarchy

- **Display** (700, 3rem → 4.5rem at 40rem → 5.5rem at 64rem, line-height 0.92, uppercase): the hero claim, one per page. The /system page title uses the same face at 3rem → 4.5rem.
- **Headline** (700, 2.25rem → 3rem at 40rem, line-height 0.95, uppercase): the TrackSection heading. The CautionPanel heading is one step down (1.875rem → 2.25rem, line-height 1).
- **Title, story step** (700, 2.25rem, line-height 1, uppercase): Review / Apply / Verify / Recover.
- **Title** (600, 1.5rem, +0.04em, uppercase): column headings such as Available controls. The quick-start sub-headings are 1.25rem.
- **Plate and step labels** (Big Shoulders 700 1rem +0.14em on levers; 700 0.9375rem +0.12em on plates; 600 1.125rem +0.08em on command-step labels, disclosure summaries and the lockup at +0.1em; uppercase): engraved labels.
- **Lede** (Overpass 400, 1.125rem, 1.25rem in the hero from 40rem up, line-height 1.625, at most 62–68ch): the opening paragraph of a section.
- **Body** (Overpass 400, 1rem, 1.625, at most 68–72ch): running text. Paragraphs use `text-wrap: pretty`, and headings use `balance`.
- **Caption** (Overpass 400, 0.875rem, 1.625): figure captions, troubleshooting notes, footer.
- **Mono** (Overpass Mono 400, 0.875rem, 1.625): commands in command blocks; inline `code` at 0.875em in enamel-bright.
- **Mono label** (Overpass Mono, 0.75rem, +0.16em, uppercase): diagram station labels; small controls (Pause, Copy, Close) at +0.12em; StatusLamp at 0.8125rem 500 in lower case.

### Named Rules

**The Engraved Plate Rule.** Big Shoulders capitals are for things engraved on the frame: the claim, section names, levers, plates and step labels. Running text is never set in capitals.

**The Instrument Rule.** Mono is for commands, code, file names, tabular data and diagram labels only. It is never a costume to make prose look technical.

**The No Eyebrow Rule.** No kicker or label sits above a heading. The heading carries its own weight, and the TrackSection rule beneath it does the framing.

## Layout

The page is one vertical flow inside a 72rem container with 1.5rem gutters. The hero is the exception: it is a header at least `min(100svh, 64rem)` tall. The lockup, plates and CI badge sit on a hairline bar, then the claim, lede, alpha line and the two levers sit at the left. The route diagram runs full-bleed across the bottom of the header, its track crossing the whole viewport while the stations stay aligned to the content column.

Sections are separated by 7rem (9rem from 40rem up). Each one opens with a TrackSection heading, an optional lede at most 68ch wide, and the content 3rem below. Within a section, related items sit 1–1.5rem apart and distinct groups sit 3–4rem apart. Columns are divided by track hairlines, never boxed.

The route diagram scales as one unit through `--route-scale` (the `.route-rail` rule in `global.css`). The hero uses 1 below 64rem, 1.25 at 64rem, 1.5 at 80rem and 2 at 120rem. The story stage uses 1.25, then 1.5 at 80rem. Lamp radii, track weight and labels grow with the diagram, so it never thins out on wide screens.

The story section, from 64rem up with motion allowed, is a 12-column grid: steps in columns 1–5 and a sticky 24rem stage in columns 6–12, centred on the viewport. The first step is as tall as the stage, so it starts level with it. The others are 55svh tall, so each crosses the viewport centre in turn. Below 64rem or with reduced motion, each step carries its own static diagram strip. Breakpoints are Tailwind's (40, 48, 64, 80rem) plus 120rem for the hero scale. At 390px the page has no horizontal overflow.

## Elevation & Depth

The frame is flat, and the whole page sits on one plane. Depth is physical and small: enamel plates and levers carry an inner bevel, as if pressed from metal, and lamps carry light, not shadow. There are no drop shadows, no glass, no blur and no gradients on surfaces. The lamp glows (radial gradients in the SVG poster, additive sprites in the WebGL scene) and the CautionPanel's hazard stripe are the only gradients. The one place with real depth is the hero scene: a perspective view of the route diagram with volume, light and fog (SignalHero below).

### Shadow Vocabulary

- **Lever bevel** (`box-shadow: inset 0 1px 0 rgb(255 255 255 / 0.4), inset 0 -2px 0 rgb(0 0 0 / 0.22)`): the primary lever and the command-step numbers. It is removed while the lever is pressed.
- **Plate bevel** (`box-shadow: inset 0 1px 0 rgb(255 255 255 / 0.2), inset 0 -1px 0 rgb(0 0 0 / 0.4)`): Plates.
- **Lamp glow** (a radial lamp-colour gradient to transparent at 3× the lamp radius, 0.5–0.6 peak opacity): lit lamps in the route diagram only.

### Named Rules

**The Lamps Carry Light Rule.** Only a lit lamp glows. Nothing else on the page emits light or casts a shadow.

## Shapes

Hard, machined edges. Plates, step numbers and small controls have 2px corners, and levers have 3px. Lamps and status dots are the only circles. Lines are the main form: 1px track hairlines, a 3px set-route segment at the start of every section rule ending in a small signal ring, and the 3px track of the route diagram with its points blade and branch. Figures have a 1px track-strong border and no radius. The mark is drawn on a 32-unit grid with one 3-unit stroke.

## Components

Every component lives in `site/src/components/` and is rendered in each of its states on `/system`.

### Buttons: LeverButton

The call to action is a lever plate, and pulling it throws it.

- **Shape:** 3px corners, at least 48px tall, 24px side padding, Big Shoulders 700 1rem +0.14em capitals.
- **Primary:** an enamel plate with frame text and the lever bevel. Hover goes to enamel-bright, and pressed goes to enamel-soft with no bevel and a 1px drop. "Try locally" is the only primary in a view.
- **Ghost:** a track-strong outline on the frame with enamel text. Hover brightens the border to track-bright, fills with frame-raised and turns the text enamel-bright. Pressed fills with frame-high.
- **Focus:** the global 2px enamel-bright outline, offset 4px.
- **States:** colours change over 150ms. There is no disabled state: the site never shows a control that cannot act.

### Chips: Plate and StatusLamp

- **Plate:** a static engraved fact (Alpha, Apache 2.0, a version), with 2px corners, Big Shoulders 700 0.9375rem capitals and the plate bevel. Neutral is frame-high with enamel text (also Limit and Deferred), lamp is amber with frame text (Alpha only), and blue is lever-blue with enamel-bright text (also Planned). It has no interactive states.
- **StatusLamp:** a 10px round lamp plus its label in Overpass Mono 0.8125rem: verified (green), rolled back (amber), failed (red), pending (a hollow enamel-muted ring). The label always renders.

### Cards / Containers: TrackSection and CautionPanel

- **TrackSection:** the section container. A Big Shoulders headline, then the track rule (3px enamel set segment, 1px track-strong hairline, signal ring), an optional lede and the content. It has no background, border or padding.
- **CautionPanel:** the one boxed region on the page, used for "before you apply, read this". A 1px amber border at 45%, a 6% amber tint, an 8px 45° amber hazard stripe on top and 24–40px padding. It is never nested and never repeated on a page.
- **NotYetCleared and ClearanceRow:** the one list of what is not done, beside Available controls. A Title heading, a caption-size legend for the plates, then ClearanceRows between track hairlines: an unlit 12px lamp (frame-high lens, 2px enamel-muted ring), a Big Shoulders step-label title, a Plate (Planned blue; Limit or Deferred neutral) and one plain sentence below. An optional note carries the single link to the full list. It is not a box, nothing in it is lit, and Available controls next to it stays neutral, never green.
- **Rule:** nothing is nested inside a box. Columns are divided by track hairlines.

### Inputs / Fields

None. The site has no forms.

### Navigation: Lockup and footer

- **Lockup:** the mark (28px) plus "Mosquitto Control Manager" in Big Shoulders 600 1.125rem +0.1em capitals, in enamel-bright. It is a link on `/system` and plain text on the landing page.
- **Footer:** a track hairline, then caption-size enamel-muted links that turn enamel-bright on hover. "Brand system" links to `/system`.
- **Text link:** inherits its colour, with a track-bright underline 4px below the text. Hover brightens both the text and the underline.

### CommandStep (signature)

A numbered lever with its command. The number is a 44–48px enamel plate with the lever bevel and tabular numerals ("01"). A track-strong hairline connects it to the next step. The label is a Big Shoulders step label. The command sits in a frame-raised block with a track-strong border, scrolls sideways and takes keyboard focus. The Copy control (small control, 36px) appears only when JavaScript runs, and a polite live region announces "Copied", or tells you to copy manually when the clipboard is unavailable. The last step can carry a success checkpoint and recovery notes.

### EvidenceFigure and CaptureViewer

- **EvidenceFigure:** a real capture of the running app with a 1px track-strong border (enamel-muted on hover), lazy loading and intrinsic dimensions, so it causes no layout shift. A caption-size caption sits below with an "Open full-size file" link. Never fabricate or mock a capture.
- **CaptureViewer:** a single full-screen `<dialog>` per page. Selecting a figure opens the capture at its natural size in a scroll box, which pans by scrolling, dragging or arrow keys. The page behind is inert and does not scroll. Escape or Close returns focus to the figure. Modified clicks keep their browser meaning. Without JavaScript, the links open the file.

### SignalHero and RoutePoster (signature)

The hero's route diagram. **RoutePoster** is the build-time SVG of the route at one story step (review, apply, verify, recover). Every part that changes between steps carries `data-on`, so CSS can relight it. **SignalHero** paints the poster at the verify step as its first paint, and labels it in the HTML. When the conditions below hold, it fades a WebGL scene in over the poster over 700ms. The scene draws the same geometry from `route-geometry.ts` and the same `.route-rail` box. It loops an 11.5s cycle that alternates a verified run with a failed probe that rolls back. The signal label reads "Failed" through the whole rollback.

- **Volume.** The track is a 3×3px bar on sleepers, the points blade a box, the lamps low drum housings with an enamel rim and an emissive lens, the MFA lock a block. The scene is lit (ambient plus one key light from the front left), so top faces read at their poster colours and sides fall darker.
- **Camera.** A perspective camera starts at the poster's straight-on framing, so the crossfade does not jump. After the crossfade it swings once, over 2.4s, to rake along the track from low on the left (the route recedes toward the signal): this is the "route opening" entrance. The rake is fitted on every resize so the route and its labels sit where the poster had them; a wide stage rakes up to 38°, a narrow one about 12°, so far labels stay apart. After that the camera only trucks along the track after the route light (critically damped, never ambient).
- **Light.** Only the lamps glow: an additive sprite with a clear centre, so a lens keeps its own colour. Lamps blend off to on in OKLab, so they pass through a darker amber, green or red, never an olive grey. The points blade is enamel-muted, amber once the route light runs through it. The probe pulse is an emissive bead with no glow. Depth fog toward the frame colour fades the far track; lenses and glows ignore the fog.
- **Labels.** The HTML labels keep their poster layout and move each frame by the difference between the projected lamp and the poster's flat one, so they stay on their lamps. They stay real, aria-hidden text, and the `sr-only` figcaption describes the story.
- **Sleepers** fade up with the entrance; the poster has none.

### RouteStory

"How a change travels": four steps (Review, Apply, Verify, Recover) beside a sticky stage that shows a RoutePoster. An IntersectionObserver on the viewport's centre line picks the active step; there is no scroll listener and scrolling stays native. Lamps and the MFA lock light over 500ms: a lens grows from its centre at full colour (a cross-fade would pass through olive), the lock fades. A lighting label decodes in a 360ms glyph scramble on the aria-hidden copy, and the text stays in an `sr-only` twin. Inactive steps dim to enamel-muted. With reduced motion or without JavaScript, the stage is hidden and every step shows its own static diagram.

- **Scroll-driven light.** Where CSS scroll-driven animations and `timeline-scope` exist, amber travels along the stage's track with the scroll instead of snapping: each step names a view timeline (`--story-N`), and a lit segment draws through `stroke-dashoffset` while that step crosses the viewport centre line, the same line the observer uses. Review → apply runs over the Review step, apply → signal over the Apply step, split at the points so Recover can drop the part past them. Lamps, lock, labels and the Recover switch to the branch still step on the observer, so the amber arrives as the lamp lights.
- **Fallback.** Without scroll-driven animations, the per-step lit lines (`data-route-light`) step on the observer as before.
- **Not done.** The hero scene is not handed into the story stage as one continuous shot: that would keep a WebGL context alive across the scroll and drive its camera from scroll position, against the off-screen pause and the native-scroll rules, with a second fallback path for the stage.

### Motion contract

Motion and 3D are allowed freely, under these conditions, all of which the site implements:

- **It reports a state.** Every animation shows the route being set, proven, failing or rolling back. There is no ambient or decorative motion and no identical entrance on every section.
- **Poster first paint.** Any animated scene paints a static, truthful frame first: the SVG poster at the verified step. The live scene fades in only after it has drawn.
- **Lazy, gated 3D.** `three` and the scene are imported only after the page has loaded and gone idle, with the hero on screen, motion allowed and WebGL2 available. The first load carries a ~1 KB bootstrap; the ~138 KB gzipped scene is loaded on demand. There is no post-processing: the lamp glow is a sprite, not bloom.
- **One entrance, then state.** The hero camera opens once from the poster's framing into its rake and then only follows the route light. The story's amber is driven by the scroll itself, so it moves only when the reader does.
- **Reduced motion.** With `prefers-reduced-motion: reduce`, the scene is never requested. Switching to reduced motion mid-visit disposes the scene and returns to the poster. The story keeps its static diagrams and never runs the scroll-driven light.
- **No WebGL.** The poster stays, and the page is complete without the scene.
- **Lost GPU context.** On `webglcontextlost` the scene stops, disposes and fades back to the poster, and the pause button hides; it never leaves a blank canvas.
- **Scene chunk fails to load.** A rejected `import()` of the scene (network, stale deploy) is caught: the poster stays, with no unhandled rejection.
- **Back/forward cache.** The scene disposed on `pagehide` boots again on a persisted `pageshow`, through the same gates (motion allowed, WebGL2, on screen).
- **Zero-size stage.** A hidden or not-yet-laid-out stage cannot latch a NaN or infinite value into the camera's rake or follow.
- **No scroll-driven animations.** The story steps its lit track on the observer.
- **Pause control.** A visible Pause/Play animation button appears once the scene is live.
- **Off-screen pause.** The render loop stops when the hero leaves the viewport or the tab is hidden, and the scene is disposed on `pagehide`.
- **DPR cap.** The pixel ratio is 1 on coarse pointers or viewports under 768px, otherwise at most 1.5.
- **Easing.** Exponential ease-out (`cubic-bezier(0.16, 1, 0.3, 1)`) for fades and relights; a fast throw with a small overshoot for points and the lock; ease-in-out over 2.4s for the hero camera's one entrance, then a critically damped follow; linear (scroll-bound) for the story's amber; 150ms colour transitions on controls.

## Do's and Don'ts

### Do:

- **Do** use green only for verified, red only for failed, and amber for a set route, a caution or a rollback.
- **Do** give every lamp colour a word next to it (StatusLamp label, diagram label).
- **Do** draw any route diagram from `route-geometry.ts` inside a `.route-rail`, so the poster, labels and scene stay aligned.
- **Do** give every animated or 3D surface a poster first paint, a reduced-motion frame, a no-WebGL fallback, a pause control, an off-screen pause and a DPR cap.
- **Do** open sections with a TrackSection and keep running text at 68–72ch or narrower.
- **Do** theme the browser surfaces from the palette: amber selection, amber caret, enamel-muted on frame scrollbar, 2px enamel-bright focus ring, 4px underline offset, tabular numerals for numbers that line up.
- **Do** add every new component or state to `/system` in the same change.
- **Do** use only real captures of the running app as imagery, with their alt text and intrinsic size.

### Don't:

- **Don't** put a kicker or eyebrow label above a heading.
- **Don't** wrap sections or columns in cards, nest a box in a box, or use glass, blur or surface gradients.
- **Don't** use green, red or amber as decoration, emphasis or a brand accent.
- **Don't** set red text under 24px in `danger`; use `danger-text`. Never set text in `lever-blue`.
- **Don't** animate anything that does not report a state, or ship an animation without its static twin.
- **Don't** draw message traffic, packets or anything else MCM does not route. The scene shows the change route only.
- **Don't** use mono for prose, or Big Shoulders capitals for running text.
- **Don't** add drop shadows or hard offset shadows. Depth is the bevel on plates and the light of lamps.
- **Don't** use the Eclipse or Mosquitto logos or their motifs. The mark is MCM's own.
