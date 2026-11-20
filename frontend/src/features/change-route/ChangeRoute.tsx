import { useEffect, useRef, useState, type ReactNode } from 'react'
import RecentMFAConfirmation from '../mfa/RecentMFAConfirmation'
import type { useRecentMFA } from '../mfa/useRecentMFA'
import { deploymentLamp, deploymentStatusLabel, lampPanel } from '../status/deploymentLamp'
import { Lamp as LampWord } from '../ui/primitives'
import { bodyText, leverPrimary, leverSecondary, type LampTone } from '../ui/styles'

export type ApplyOutcome = { status: string; message?: string }

type Props = {
  hasChanges: boolean
  reviewKey?: string // identity of the reviewed content (preview revision); a new one restarts the route
  children?: ReactNode // the caller's review content (preview, diff)
  mfa: ReturnType<typeof useRecentMFA>
  // Resolves to null when the request failed; the caller shows its own error.
  onApply: (proof: string) => Promise<ApplyOutcome | null>
  applyLabel: string
  disabledReason?: string
}


// The change as a signal-box route (DESIGN.md): Review → MFA → Apply → Probe → Result.
// Amber lamps mark the set route; only the result lamp may turn green (verified) or red (failed).
export default function ChangeRoute({ hasChanges, reviewKey, children, mfa, onApply, applyLabel, disabledReason }: Props) {
  const [armed, setArmed] = useState(false)
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<ApplyOutcome | null>(null)
  const [reviewed, setReviewed] = useState(reviewKey)
  // A previous apply's result must not survive into the review of new content. Only a new key
  // resets: a panel clears its preview in the same batch as the outcome of its apply arrives.
  if (reviewKey !== reviewed) {
    setReviewed(reviewKey)
    if (reviewKey) { setResult(null); setArmed(false) }
  }
  // Arming and cancelling swap the focused button for another, and a verified MFA form turns
  // into text: move focus to the control that replaced it (#359).
  const confirmation = useRef<HTMLDivElement>(null)
  const confirmButton = useRef<HTMLButtonElement>(null)
  const applyButton = useRef<HTMLButtonElement>(null)
  const cancelled = useRef(false)
  useEffect(() => {
    if (armed && mfa.proof) confirmButton.current?.focus()
    else if (armed) confirmation.current?.querySelector<HTMLElement>('input, button:not(:disabled)')?.focus()
    else if (cancelled.current) applyButton.current?.focus()
    cancelled.current = false
  }, [armed, mfa.proof])
  // A new preview or a new blocking reason drops a started apply back to Review.
  if (armed && !running && (!hasChanges || disabledReason)) setArmed(false)

  async function confirm() {
    setRunning(true)
    setResult(null)
    try {
      const outcome = await onApply(mfa.proof).catch(() => null)
      if (outcome) { setResult(outcome); setArmed(false) }
    } finally {
      setRunning(false)
    }
  }

  const resultLamp = result ? deploymentLamp(result.status) : null
  // A set stage is lit amber (caution); only the result lamp may go green or red.
  const stages: [string, string, LampTone][] = [
    ['Review', hasChanges ? 'done' : 'waiting', hasChanges ? 'caution' : 'off'],
    ['MFA', mfa.proof ? 'done' : armed ? 'waiting' : 'not reached', mfa.proof ? 'caution' : 'off'],
    ['Apply', running ? 'in progress' : result ? 'done' : armed ? 'waiting' : 'not reached', running || result ? 'caution' : 'off'],
    // Only a verified result proves the probe; otherwise say what the result lamp says.
    ['Probe', running ? 'in progress' : !result ? 'not reached' : resultLamp === 'verified' ? 'done' : resultLamp === 'failed' ? 'failed' : 'not proven', running || result ? 'caution' : 'off'],
    ['Result', resultLamp ?? 'not reached', resultLamp ?? 'off'],
  ]

  return (
    <div className="space-y-4">
      {/* The route as a track: each stage a lamp on one hairline, stage name in Overpass, state in the lamp's word. */}
      <ol aria-label="Change route" className="flex flex-wrap gap-x-6 gap-y-2 border-y border-track py-3">
        {stages.map(([name, state, lamp]) => (
          <li key={name} aria-label={`${name}: ${state}`} className="inline-flex items-baseline gap-2">
            <span className="text-sm font-semibold text-enamel-bright">{name}</span>
            <LampWord tone={lamp}>{state}</LampWord>
          </li>
        ))}
      </ol>
      {children}
      {hasChanges ? (
        <div ref={confirmation} className="space-y-4">
          {armed ? <RecentMFAConfirmation state={mfa} /> : null}
          <div className="flex flex-wrap items-center gap-3">
            {running ? <p className="text-sm text-lamp">Applying and verifying activation…</p> : armed ? (
              <>
                <span className={`${bodyText} w-full`}>Apply this change? MCM writes it, activates it on the broker and probes the result.</span>
                <button ref={confirmButton} type="button" onClick={() => { void confirm() }} disabled={!mfa.proof} className={leverPrimary}>Confirm {applyLabel}</button>
                <button type="button" onClick={() => { cancelled.current = true; setArmed(false) }} className={leverSecondary}>Cancel</button>
                {!mfa.proof ? <p className="w-full text-sm text-lamp">Verify MFA above to confirm.</p> : null}
              </>
            ) : (
              <>
                <button ref={applyButton} type="button" onClick={() => setArmed(true)} disabled={Boolean(disabledReason)} className={leverPrimary}>{applyLabel}</button>
                {disabledReason ? <p className="text-sm text-lamp">{disabledReason}</p> : null}
              </>
            )}
          </div>
        </div>
      ) : null}
      <div role="status" aria-live="polite" aria-label="Apply result" className={result && resultLamp ? `px-4 py-3 ${lampPanel[resultLamp]}` : ''}>
        {result ? (
          <>
            <p className="text-sm font-semibold">{deploymentStatusLabel[result.status] ?? result.status}</p>
            {result.message ? <p className={`mt-2 ${bodyText}`}>{result.message}</p> : null}
          </>
        ) : null}
      </div>
    </div>
  )
}
