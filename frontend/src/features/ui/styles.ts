// Shared class strings of the Operate surface (DESIGN.md "Operate surface (app)").
// Tokens only: no new colours, no rounded cards, no eyebrows. Components live in ./primitives.

/** Lamp states: verified (green, verified activation only), caution (amber), failed (red, failed only), neutral (enamel), off (unlit ring). */
export type LampTone = 'verified' | 'caution' | 'failed' | 'neutral' | 'off'

/** The lit lens of a lamp, by tone. */
export const lampDot: Record<LampTone, string> = {
  verified: 'bg-proceed',
  caution: 'bg-lamp',
  failed: 'bg-danger',
  neutral: 'bg-enamel',
  off: 'ring-1 ring-inset ring-enamel-muted',
}

/** The word next to a lamp, by tone (green is never text; red text is danger-text). */
export const lampWord: Record<LampTone, string> = {
  verified: 'text-enamel-bright',
  caution: 'text-lamp',
  failed: 'text-danger-text',
  neutral: 'text-enamel-bright',
  off: 'text-enamel-muted',
}

/** The primary lever: enamel plate, Big Shoulders capitals, bevel that drops when pressed. One per view. */
export const leverPrimary = 'inline-flex h-12 items-center justify-center rounded-lever bg-enamel px-6 font-display text-base font-bold uppercase tracking-[0.14em] text-frame shadow-[inset_0_1px_0_rgb(255_255_255/0.4),inset_0_-2px_0_rgb(0_0_0/0.22)] transition-colors duration-150 hover:bg-enamel-bright active:translate-y-px active:bg-enamel-soft active:shadow-none disabled:cursor-not-allowed disabled:bg-enamel-muted disabled:shadow-none'

/** A secondary (small) control: track-bright outline, Overpass Mono capitals, 36px. */
export const leverSecondary = 'inline-flex h-10 lg:h-9 shrink-0 items-center justify-center gap-2 rounded-plate border border-track-bright px-3 font-mono text-xs uppercase tracking-[0.12em] text-enamel transition-colors duration-150 hover:bg-frame-raised hover:text-enamel-bright disabled:cursor-not-allowed disabled:border-track disabled:text-enamel-muted disabled:hover:bg-transparent'

/** A small control for destructive actions only (delete, revoke): danger outline and danger-text. */
export const leverDanger = 'inline-flex h-10 lg:h-9 shrink-0 items-center justify-center gap-2 rounded-plate border border-danger px-3 font-mono text-xs uppercase tracking-[0.12em] text-danger-text transition-colors duration-150 hover:bg-danger/15 disabled:cursor-not-allowed disabled:border-track disabled:text-enamel-muted disabled:hover:bg-transparent'

/** A pressed-state toggle on top of leverSecondary (aria-pressed lights it). */
export const leverToggle = `${leverSecondary} aria-pressed:border-enamel aria-pressed:bg-frame-high aria-pressed:text-enamel-bright`

/** An inline text link or link-styled button: track-bright underline 4px below. */
export const textLink = 'text-enamel-bright underline decoration-track-bright underline-offset-4 transition-colors duration-150 hover:decoration-enamel-bright'

/** A text input. */
export const input = 'rounded-plate border border-track-bright bg-frame-raised px-3 py-2 text-sm text-enamel-bright placeholder:text-enamel-muted transition-colors duration-150 hover:border-enamel-muted focus-visible:border-enamel-bright disabled:cursor-not-allowed disabled:text-enamel-muted'

/** A select (same box as input). */
export const select = `${input} pr-8`

/** A textarea for configuration or code: input box in Overpass Mono. */
export const textarea = `${input} font-mono leading-6`

/** A form field's label text. */
export const fieldLabel = 'text-sm font-semibold text-enamel'

/** A field's help or a quiet note under a control. */
export const fieldHint = 'text-xs leading-5 text-enamel-muted'

/** Running text inside a section. */
export const bodyText = 'text-sm leading-6 text-enamel-soft'

/** A preformatted block (payloads, metadata, file bodies). */
export const codeBlock = 'overflow-auto rounded-plate border border-track-strong bg-frame-raised p-4 font-mono text-xs leading-5 text-enamel-bright'

/** Notices: caution (amber, the one boxed warning), failed (red, failed requests only), neutral (empty or informational). */
export const notice = {
  caution: 'border border-lamp/45 bg-lamp/[0.06] px-4 py-3 text-sm leading-6 text-enamel',
  failed: 'border border-danger/60 px-4 py-3 text-sm leading-6 text-danger-text',
  neutral: 'border border-track-strong px-4 py-3 text-sm leading-6 text-enamel-soft',
} as const

/** Key/value pairs: the label and the value of a fact. */
export const dataLabel = 'text-xs text-enamel-muted'
export const dataValue = 'mt-1 break-words font-mono text-sm text-enamel-bright'

/** Table header cell (add text-left/text-right: th centres by default) and body row. */
export const tableHead = 'py-2 pr-4 text-xs font-semibold text-enamel-muted'
export const tableRow = 'border-b border-track last:border-0'
