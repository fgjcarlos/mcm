import type { ReactNode } from 'react'
import { Plate } from '../ui/primitives'
import { fieldHint, notice } from '../ui/styles'

// Small pieces the broker-config panels share, on the Operate primitives.

// A file picker on the Operate surface: the picker button is a small control.
export const fileInput = 'text-xs font-normal text-enamel-soft file:mr-3 file:h-9 file:cursor-pointer file:rounded-plate file:border file:border-solid file:border-track-bright file:bg-transparent file:px-3 file:font-mono file:text-xs file:uppercase file:tracking-[0.12em] file:text-enamel hover:file:bg-frame-raised disabled:file:cursor-not-allowed disabled:file:text-enamel-muted'

/** A boxed list of findings: caution for warnings, failed for issues that block the change. */
export function Findings({ tone, title, className = 'mt-5', children }: { tone: 'caution' | 'failed'; title: string; className?: string; children: ReactNode }) {
  return (
    <div className={`${className} ${notice[tone]}`}>
      <h4 className={`font-semibold ${tone === 'caution' ? 'text-lamp' : ''}`}>{title}</h4>
      <ul className="mt-2 list-disc space-y-1 pl-5">{children}</ul>
    </div>
  )
}

/** How the broker picks a change up: a reload is neutral, anything that restarts it is a caution. */
export function ReloadKind({ kind }: { kind: string }) {
  return <Plate tone={kind === 'reload' ? 'neutral' : 'caution'}>{kind}</Plate>
}

/** The quiet line under a panel heading that says whether the draft differs from what is stored. */
export function DraftState({ dirty }: { dirty: boolean }) {
  return <p className={`mt-1 ${fieldHint}`}>{dirty ? 'Unsaved changes' : 'Saved'}</p>
}
