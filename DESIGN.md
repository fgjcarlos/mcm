---
name: Mosquitto Control Manager (MCM)
description: Dark telemetry console for administering one Mosquitto broker — cyan signal, mono technicals, verified states.
colors:
  signal: "#00d3f3"
  signal-bright: "#53eafd"
  signal-outline: "#a2f4fd"
  signal-pale: "#cefafe"
  caution: "#ffd230"
  caution-soft: "#fee685"
  caution-bright: "#fef3c6"
  caution-panel: "#ffb900"
  floor: "#020617"
  field: "#0f172a"
  horizon: "#1d4e89"
  ink: "#f1f5f9"
  ink-strong: "#e2e8f0"
  ink-soft: "#cad5e2"
  ink-muted: "#90a1b9"
  ink-bright: "#ffffff"
  surface: "rgba(255,255,255,0.04)"
  surface-strong: "rgba(255,255,255,0.07)"
  surface-faint: "rgba(255,255,255,0.03)"
  surface-panel: "rgba(2,6,23,0.55)"
  surface-code: "rgba(2,6,23,0.7)"
  surface-chip: "rgba(2,6,23,0.4)"
  hairline: "rgba(255,255,255,0.10)"
  hairline-strong: "rgba(255,255,255,0.15)"
  hairline-bright: "rgba(255,255,255,0.40)"
  hairline-soft: "rgba(255,255,255,0.30)"
typography:
  display:
    fontFamily: "Manrope Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "clamp(2.25rem, 5vw, 3.75rem)"
    fontWeight: 600
    letterSpacing: "-0.025em"
    lineHeight: 1.1
  headline:
    fontFamily: "Manrope Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1.875rem"
    fontWeight: 600
    letterSpacing: "-0.025em"
    lineHeight: 1.2
  title:
    fontFamily: "Manrope Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1.125rem"
    fontWeight: 600
  body:
    fontFamily: "Manrope Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1rem"
    fontWeight: 400
    lineHeight: 1.625
  label:
    fontFamily: "Manrope Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.75rem"
    fontWeight: 600
    letterSpacing: "0.3em"
    lineHeight: 1.4
  mono:
    fontFamily: "JetBrains Mono Variable, ui-monospace, SFMono-Regular, Menlo, monospace"
    fontSize: "0.75rem"
    lineHeight: 1.5
rounded:
  pill: "9999px"
  card: "1.5rem"
  code: "1rem"
spacing:
  section: "5rem"
  card-inner: "1.5rem"
  grid-gap: "1rem"
components:
  button-primary:
    backgroundColor: "{colors.signal}"
    textColor: "{colors.floor}"
    rounded: "{rounded.pill}"
    padding: "12px 20px"
  button-ghost:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.pill}"
    padding: "12px 20px"
  card-feature:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink-soft}"
    rounded: "{rounded.card}"
    padding: "24px"
  panel-console:
    backgroundColor: "{colors.surface-panel}"
    rounded: "{rounded.card}"
    padding: "32px"
  panel-caution:
    backgroundColor: "{colors.caution-panel} at 10% opacity"
    rounded: "{rounded.card}"
    padding: "24px 32px"
  chip-stack:
    backgroundColor: "{colors.surface-chip}"
    textColor: "{colors.signal-pale}"
    rounded: "{rounded.pill}"
    padding: "4px 12px"
---

# Design System: Mosquitto Control Manager (MCM)

## Overview

**Creative North Star: "The Control Room"**

MCM's site is a dark telemetry console: a calm operations room where the state of one broker is read before anything is changed. Depth comes from tonal layers of translucent surfaces over a deep radial field — no shadows, no drama. Cyan is the signal color of the console; JetBrains Mono carries everything technical (stack, commands, numbers), and Manrope carries the human layer of headlines and body copy.

The console never sells: it reports. Status labels (Alpha, Apache 2.0, CI), section eyebrows in wide uppercase tracking, and the amber caution panel all speak the operator's language of states and evidence. Motion is limited to quiet border and background shifts on hover; nothing blinks, nothing parades.

**Implementation alignment:** `site/src/styles/global.css` defines the page palette as named `@theme` color tokens. The landing page uses the corresponding token utilities throughout, and `.site-backdrop` applies the radial field through `horizon`, `field`, and `floor`.

**Key Characteristics:**

- Dark tonal console: translucent surfaces (`bg-surface`, `bg-surface-panel`) with `backdrop-blur` over a radial deep-blue field; zero box-shadows.
- Cyan as the single signal accent; amber strictly for caution (production-readiness warnings).
- Uppercase wide-tracked eyebrows (`text-xs`, `tracking-[0.3em]`) label every section like a console readout.
- Pill geometry for everything interactive or meta (buttons, badges, chips); `rounded-3xl` for content containers.
- Mono for stack chips, command blocks, and step numbers — the technical layer is typographically distinct.

## Colors

The named `@theme` tokens in `site/src/styles/global.css` are the single source of truth for the page palette. Cyan is the signal accent over a deep blue-black field, with amber reserved for caution.

### Primary

- **Signal** (#00d3f3, `text-signal` / `bg-signal`): the primary action and strongest live signal — primary button background. Used sparingly; its rarity is its voice.
- **Signal Bright** (#53eafd, `text-signal-bright` / `bg-signal-bright`): section eyebrows, hover state of the primary button.
- **Signal Outline** (#a2f4fd, `outline-signal-outline`): keyboard focus outlines.
- **Signal Pale** (#cefafe, `text-signal-pale`): text sitting on signal-tinted surfaces (stack chips, alpha badge, code text).
- **Caution** (#ffd230, `border-caution`): caution-panel border.
- **Caution Soft** (#fee685, `text-caution-soft`): caution label.
- **Caution Bright** (#fef3c6, `text-caution-bright`): caution heading.
- **Caution Panel** (#ffb900, `bg-caution-panel/10`): amber caution-panel tint.

### Neutral

- **Floor** (#020617, `bg-floor` / `text-floor`): page floor.
- **Field** (#0f172a): middle of the radial backdrop and selection text.
- **Horizon** (#1d4e89): glow at the top of `.site-backdrop`'s radial field.
- **Ink** (#f1f5f9, `text-ink`): default text.
- **Ink Strong** (#e2e8f0, `text-ink-strong`): disclosure summary.
- **Ink Soft** (#cad5e2, `text-ink-soft`): body copy.
- **Ink Muted** (#90a1b9, `text-ink-muted`): captions and footer.
- **Ink Bright** (#ffffff, `text-ink-bright`): headlines and hover text.

### Surfaces and lines

- **Surface** (rgba(255,255,255,0.04), `bg-surface`): quiet translucent surface.
- **Surface Strong** (rgba(255,255,255,0.07), `bg-surface-strong`): hover surface.
- **Surface Faint** (rgba(255,255,255,0.03), `bg-surface-faint`): disclosure surface.
- **Surface Panel** (rgba(2,6,23,0.55), `bg-surface-panel`): console and evidence panels.
- **Surface Code** (rgba(2,6,23,0.7), `bg-surface-code`): command blocks.
- **Surface Chip** (rgba(2,6,23,0.4), `bg-surface-chip`): stack chips.
- **Hairline** (rgba(255,255,255,0.10), `border-hairline`): surface separators.
- **Hairline Strong** (rgba(255,255,255,0.15), `border-hairline-strong`): ghost button border.
- **Hairline Bright** (rgba(255,255,255,0.40), `border-hairline-bright`): hover border.
- **Hairline Soft** (rgba(255,255,255,0.30), `decoration-hairline-soft`): link decoration.

### Named Rules

**The Signal Rule.** Cyan marks signal and action: the primary button, eyebrows, active tints. It never fills large quiet areas; the largest cyan field is the stack panel's 10% tint. Amber appears only in the caution panel.

## Typography

**Display Font:** Manrope Variable (ui-sans-serif, system-ui fallback)
**Body Font:** Manrope Variable
**Label/Mono Font:** JetBrains Mono Variable (ui-monospace, SFMono-Regular, Menlo fallback)

**Character:** A geometric humanist sans with tight, confident display tracking, paired with a technical mono — the pairing says "console that reads like an instrument, not a brochure."

### Hierarchy

- **Display** (600, `text-4xl` → `sm:text-6xl`, tight): the hero claim — one per page.
- **Headline** (600, `text-3xl`, tight): section titles under an eyebrow.
- **Title** (600, `text-lg`): card titles.
- **Body** (400, `text-sm`–`text-lg`, `leading-relaxed`): explanatory copy; secondary ink-soft on dark surfaces.
- **Label / eyebrow** (600, `text-xs`, uppercase, `tracking-[0.3em]`–`[0.4em]`, signal-bright): console-style section markers.
- **Mono** (400, `text-xs`–`text-sm`): stack chips, command blocks, step numerals.

### Named Rules

**The Technical Layer Rule.** Anything a terminal could say — commands, stack names, versions, numerals — is set in JetBrains Mono. Anything a person says in prose is Manrope.

## Layout

Single-page vertical flow inside `max-w-6xl` with `px-6`; sections separated by `mt-20` (5rem). Hero: stacked badges → eyebrow → display claim → explanatory lede → alpha status → action row (pill buttons wrap). Availability uses three clearly labeled categories in a three-column card grid at desktop; evidence pairs the full-width deploy capture with two supporting captures in a two-column grid. The production caution panel precedes quick start. Quick start is a numbered vertical command list with a success checkpoint and recovery branches. Technical stack stays in a collapsed disclosure; footer has a top hairline and two-row flex. Density is low: generous padding (`p-6`–`p-8`), no more than three cards per row.

## Elevation & Depth

Flat. No box-shadows anywhere; depth is conveyed by tonal layering: translucent white tints (`bg-surface`), dark panels (`bg-surface-panel` + `backdrop-blur`), cyan-tinted panels for signal areas, and hairline borders (`border-hairline`, `border-signal-bright/20`). The only ambient effect is `.site-backdrop`'s radial gradient, using `--color-horizon` at the top and fading through `--color-field` to `--color-floor`.

## Shapes

Pills (`rounded-full`) for all interactive and meta elements: buttons, badges, stack chips, step numerals. Content containers use `rounded-3xl` (1.5rem); inline code blocks `rounded-2xl`. Corners are generous but never card-nested-in-card.

## Components

### Buttons

- **Shape:** pill (`rounded-full`), `px-5 py-3`, `text-sm font-semibold uppercase tracking-[0.18em]`.
- **Primary:** solid Signal (#00d3f3) on Floor text; hover brightens to Signal Bright (#53eafd). One per view.
- **Ghost:** `border-hairline-strong bg-surface text-ink`; hover `border-hairline-bright text-ink-bright`.
- **Hover/Focus:** color/border transitions only; no transforms.

### Chips

- **Stack chip:** pill, `border-hairline bg-surface-chip`, mono `text-xs` Signal Cyan Pale.

### Cards / Containers

- **Feature card:** `rounded-3xl border-hairline bg-surface p-6`; hover shifts border to `border-signal-bright/30` and bg to `bg-surface-strong`.
- **Console panel:** `rounded-3xl border-hairline bg-surface-panel p-8 backdrop-blur` (Quick start).
- **Signal panel:** `border-signal-bright/20 bg-signal/10` (success checkpoint).
- **Caution panel:** `border-caution/25 bg-caution-panel/10`, `text-caution-bright` title (production warning).

### Inputs / Fields

Not present on the public site (single landing page, no forms).

### Navigation

None — single page; header badges act as status readouts. Footer link row (GitHub, Issues, API spec, Security) in text-muted, hover white.

### Command block (signature)

The quickstart's numbered command list is the signature element: mono circle numeral (`h-9 w-9`, cyan hairline ring, cyan tint) + uppercase micro-label + `rounded-2xl` dark command block with horizontal-scroll code in Signal Cyan Pale. It reads as a checklist an operator follows top to bottom.

## Do's and Don'ts

### Do:

- **Do** keep every section under a cyan uppercase eyebrow — the console's readout rhythm.
- **Do** set all commands, stack names and numerals in JetBrains Mono.
- **Do** preserve the amber panel as the only place amber is allowed.
- **Do** state status honestly where it is claimed (Alpha badge, "planned, not available" language).
- **Do** separate surfaces with hairlines and tonal tints, never shadows.

### Don't:

- **Don't** add a second accent color; cyan is the signal, amber is caution.
- **Don't** use gradients other than the fixed body radial field.
- **Don't** animate anything beyond hover color/border transitions.
- **Don't** fill large areas with solid cyan; its largest legal field is a 10% tint.
- **Don't** nest translucent cards inside translucent cards.
