---
name: Mosquitto Control Manager (MCM)
description: Dark telemetry console for administering one Mosquitto broker — cyan signal, mono technicals, verified states.
colors:
  signal-cyan: "#22d3ee"
  signal-cyan-bright: "#67e8f9"
  signal-cyan-pale: "#cffafe"
  caution-amber: "#fcd34d"
  console-black: "#020617"
  console-deep: "#0f172a"
  console-horizon: "#1d4e89"
  text-primary: "#f1f5f9"
  text-secondary: "#cbd5e1"
  text-muted: "#94a3b8"
  surface-line: "rgba(255,255,255,0.10)"
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
    backgroundColor: "{colors.signal-cyan}"
    textColor: "{colors.console-black}"
    rounded: "{rounded.pill}"
    padding: "12px 20px"
  button-ghost:
    backgroundColor: "rgba(255,255,255,0.04)"
    textColor: "{colors.text-primary}"
    rounded: "{rounded.pill}"
    padding: "12px 20px"
  card-feature:
    backgroundColor: "rgba(255,255,255,0.04)"
    textColor: "{colors.text-secondary}"
    rounded: "{rounded.card}"
    padding: "24px"
  panel-console:
    backgroundColor: "rgba(2,6,23,0.55)"
    rounded: "{rounded.card}"
    padding: "32px"
  panel-caution:
    backgroundColor: "rgba(251,191,36,0.10)"
    rounded: "{rounded.card}"
    padding: "24px 32px"
  chip-stack:
    backgroundColor: "rgba(2,6,23,0.40)"
    textColor: "{colors.signal-cyan-pale}"
    rounded: "{rounded.pill}"
    padding: "4px 12px"
---

# Design System: Mosquitto Control Manager (MCM)

## Overview

**Creative North Star: "The Control Room"**

MCM's site is a dark telemetry console: a calm operations room where the state of one broker is read before anything is changed. Depth comes from tonal layers of translucent surfaces over a deep radial field — no shadows, no drama. Cyan is the signal color of the console; JetBrains Mono carries everything technical (stack, commands, numbers), and Manrope carries the human layer of headlines and body copy.

The console never sells: it reports. Status labels (Alpha, Apache 2.0, CI), section eyebrows in wide uppercase tracking, and the amber caution panel all speak the operator's language of states and evidence. Motion is limited to quiet border and background shifts on hover; nothing blinks, nothing parades.

**Implementation alignment:** `site/src/styles/global.css` defines the palette as named `@theme` color tokens (hex and rgba values above), and `.site-backdrop` applies the radial field using the horizon, deep, and black tokens. Landing-page components use Tailwind cyan and slate utility classes alongside these semantic tokens.

**Key Characteristics:**

- Dark tonal console: translucent surfaces (`bg-white/[0.04]`, `bg-slate-950/55`) with `backdrop-blur` over a radial deep-blue field; zero box-shadows.
- Cyan as the single signal accent; amber strictly for caution (production-readiness warnings).
- Uppercase wide-tracked eyebrows (`text-xs`, `tracking-[0.3em]`) label every section like a console readout.
- Pill geometry for everything interactive or meta (buttons, badges, chips); `rounded-3xl` for content containers.
- Mono for stack chips, command blocks, and step numbers — the technical layer is typographically distinct.

## Colors

One signal accent (cyan) over a deep blue-black field, with amber reserved for caution; neutrals are Tailwind slate.

### Primary

- **Signal Cyan** (#22d3ee, `cyan-400`): the primary action and the strongest live signal — primary button background, stack-panel tint. Used sparingly; its rarity is its voice.
- **Signal Cyan Bright** (#67e8f9, `cyan-300`): section eyebrows, hover state of the primary button.
- **Signal Cyan Pale** (#cffafe, `cyan-100`): text sitting on cyan-tinted surfaces (stack chips, alpha badge, code text).

### Neutral

- **Console Black** (#020617, `slate-950`): page floor and inner panels.
- **Console Deep** (#0f172a, `slate-900`): mid field of the radial gradient, dark panels (`bg-slate-950/55`).
- **Console Horizon** (#1d4e89, `--color-console-horizon`): the glow at the top of `.site-backdrop`'s radial field.
- **Text Primary** (#f1f5f9, `slate-100`): headlines and default text.
- **Text Secondary** (#cbd5e1, `slate-300`): body copy.
- **Text Muted** (#94a3b8, `slate-400`): captions and footer.
- **Surface Line** (rgba(255,255,255,0.10), `border-white/10`): hairline borders that define surfaces without shadows.

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
- **Body** (400, `text-sm`–`text-lg`, `leading-relaxed`): explanatory copy; secondary slate-300 on dark surfaces.
- **Label / eyebrow** (600, `text-xs`, uppercase, `tracking-[0.3em]`–`[0.4em]`, cyan-300): console-style section markers.
- **Mono** (400, `text-xs`–`text-sm`): stack chips, command blocks, step numerals.

### Named Rules

**The Technical Layer Rule.** Anything a terminal could say — commands, stack names, versions, numerals — is set in JetBrains Mono. Anything a person says in prose is Manrope.

## Layout

Single-page vertical flow inside `max-w-6xl` with `px-6`; sections separated by `mt-20` (5rem). Hero: stacked badges → eyebrow → display claim → explanatory lede → alpha status → action row (pill buttons wrap). Availability uses three clearly labeled categories in a three-column card grid at desktop; evidence pairs the full-width deploy capture with two supporting captures in a two-column grid. The production caution panel precedes quick start. Quick start is a numbered vertical command list with a success checkpoint and recovery branches. Technical stack stays in a collapsed disclosure; footer has a top hairline and two-row flex. Density is low: generous padding (`p-6`–`p-8`), no more than three cards per row.

## Elevation & Depth

Flat. No box-shadows anywhere; depth is conveyed by tonal layering: translucent white tints (`bg-white/[0.04]`), dark panels (`bg-slate-950/55` + `backdrop-blur`), cyan-tinted panels for signal areas, and hairline borders (`border-white/10`, `border-cyan-300/20`). The only ambient effect is `.site-backdrop`'s radial gradient, using `--color-console-horizon` at the top and fading through `--color-console-deep` to `--color-console-black`.

## Shapes

Pills (`rounded-full`) for all interactive and meta elements: buttons, badges, stack chips, step numerals. Content containers use `rounded-3xl` (1.5rem); inline code blocks `rounded-2xl`. Corners are generous but never card-nested-in-card.

## Components

### Buttons

- **Shape:** pill (`rounded-full`), `px-5 py-3`, `text-sm font-semibold uppercase tracking-[0.18em]`.
- **Primary:** solid Signal Cyan (#22d3ee) on Console Black text; hover brightens to #67e8f9. One per view.
- **Ghost:** `border-white/15 bg-white/[0.04]` text slate-100; hover border-white/40 + white text.
- **Hover/Focus:** color/border transitions only; no transforms.

### Chips

- **Stack chip:** pill, `border-white/10 bg-slate-950/40`, mono `text-xs` Signal Cyan Pale.

### Cards / Containers

- **Feature card:** `rounded-3xl border-white/10 bg-white/[0.04] p-6`; hover shifts border to `cyan-300/30` and bg to `white/[0.07]`.
- **Console panel:** `rounded-3xl border-white/10 bg-slate-950/55 p-8 backdrop-blur` (Quick start).
- **Signal panel:** `border-cyan-300/20 bg-cyan-400/10` (success checkpoint).
- **Caution panel:** `border-amber-300/25 bg-amber-400/10`, amber-100 title (production warning).

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
