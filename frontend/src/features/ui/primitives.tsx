import { useId, type ReactNode } from 'react'
import { dataLabel, dataValue, lampDot, lampWord, type LampTone } from './styles'

// Shared components of the Operate surface (DESIGN.md "Operate surface (app)").
// Class strings live in ./styles.

/**
 * A hairline-ruled section named by its heading (no card, radius or shadow).
 * The first section of a stack drops its rule, since the page rule sits above it.
 * `level` 3 for page sections, 4 for a section inside one; `aside` sits at the right of the heading;
 * `ruled={false}` drops the rule where a grid's hairlines already divide the sections.
 */
export function Section({ title, description, aside, level = 3, ruled = true, className = '', children }: {
  title: ReactNode
  description?: ReactNode
  aside?: ReactNode
  level?: 3 | 4
  ruled?: boolean
  className?: string
  children?: ReactNode
}) {
  const id = useId()
  const Heading = level === 3 ? 'h3' : 'h4'
  const rule = ruled ? `border-t border-track first:border-t-0 ${level === 3 ? 'pt-6' : 'pt-4'} first:pt-0` : ''
  return (
    <section aria-labelledby={id} className={`min-w-0 ${rule} ${className}`}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <Heading id={id} className={level === 3 ? 'text-lg font-semibold text-enamel-bright' : 'text-sm font-semibold text-enamel-bright'}>{title}</Heading>
          {description ? <p className="mt-1 max-w-[68ch] text-sm leading-6 text-enamel-soft">{description}</p> : null}
        </div>
        {aside}
      </div>
      {children}
    </section>
  )
}

/** A lamp and its word: colour from the tone, meaning always in the word. */
export function Lamp({ tone, children, className = '' }: { tone: LampTone; children: ReactNode; className?: string }) {
  return (
    <span className={`inline-flex items-center gap-2 font-mono text-[0.8125rem] leading-5 ${lampWord[tone]} ${className}`}>
      <span aria-hidden="true" className={`size-2.5 shrink-0 rounded-full ${lampDot[tone]}`} />
      {children}
    </span>
  )
}

/** A static fact plate (role, count, category): neutral frame-high, or caution amber. */
export function Plate({ tone = 'neutral', children }: { tone?: 'neutral' | 'caution'; children: ReactNode }) {
  const toneClass = tone === 'caution' ? 'bg-lamp text-frame' : 'bg-frame-high text-enamel ring-1 ring-inset ring-track-strong'
  return <span className={`inline-flex rounded-plate px-2 pb-[0.15rem] pt-[0.25rem] font-mono text-xs leading-4 ${toneClass}`}>{children}</span>
}

/** One key/value pair; place it inside a <dl>. */
export function Fact({ label, value }: { label: ReactNode; value: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className={dataLabel}>{label}</dt>
      <dd className={dataValue}>{value}</dd>
    </div>
  )
}

/** A unified diff: additions bright on a raised row, removals muted and struck; green and red stay reserved for lamps. */
export function DiffBlock({ content }: { content: string }) {
  return (
    // Focusable so a long diff scrolls from the keyboard.
    <pre tabIndex={0} className="max-h-64 overflow-auto rounded-plate border border-track-strong bg-frame-raised py-3 font-mono text-xs leading-5">
      {content.split('\n').map((line, idx) => (
        <span
          key={idx}
          // The ---/+++ file headers name the two sides; they are not a removal or an addition.
          className={`block px-4 ${/^(---|\+\+\+)( |$)/.test(line) ? 'text-enamel-muted' : line.startsWith('+') ? 'bg-frame-high text-enamel-bright' : line.startsWith('-') ? 'text-enamel-muted line-through decoration-enamel-muted/70' : 'text-enamel-soft'}`}
        >
          {line || ' '}
        </span>
      ))}
    </pre>
  )
}
