import { useState, type ReactNode } from 'react'
import RecentMFAConfirmation from '../mfa/RecentMFAConfirmation'
import type { useRecentMFA } from '../mfa/useRecentMFA'
import { deploymentLamp, lampPanel } from '../status/deploymentLamp'
import { Lamp as LampWord } from '../ui/primitives'
import { bodyText, leverPrimary, leverSecondary, type LampTone } from '../ui/styles'

export type ApplyOutcome = { status: string; message?: string }

type Props = {
  hasChanges: boolean
  children?: ReactNode // the caller's review content (preview, diff)
  mfa: ReturnType<typeof useRecentMFA>
  // Resolves to null when the request failed; the caller shows its own error.
  onApply: (proof: string) => Promise<ApplyOutcome | null>
  applyLabel: string
  disabledReason?: string
}

const resultLabels: Record<string, string> = {
  active_verified: 'Verified',
  applied: 'Applied — activation not proven',
  pending_activation: 'Pending activation — not verified',
  rolled_back: 'Rolled back to the previous configuration',
  rollback_failed: 'Rollback failed',
  failed: 'Failed',
}

// The change as a signal-box route (DESIGN.md): Review → MFA → Apply → Probe → Result.
// Amber lamps mark the set route; only the result lamp may turn green (verified) or red (failed).
export default function ChangeRoute({ hasChanges, children, mfa, onApply, applyLabel, disabledReason }: Props) {
  const [armed, setArmed] = useState(false)
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<ApplyOutcome | null>(null)
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
    ['Probe', running ? 'in progress' : result ? 'done' : 'not reached', running || result ? 'caution' : 'off'],
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
      {armed ? <RecentMFAConfirmation state={mfa} /> : null}
      {hasChanges ? (
        <div className="flex flex-wrap items-center gap-3">
          {running ? <p className="text-sm text-lamp">Applying and verifying activation…</p> : armed ? (
            <>
              <span className={`${bodyText} w-full`}>Apply this change? MCM writes it, activates it on the broker and probes the result.</span>
              <button type="button" onClick={() => { void confirm() }} disabled={!mfa.proof} className={leverPrimary}>Confirm {applyLabel}</button>
              <button type="button" onClick={() => setArmed(false)} className={leverSecondary}>Cancel</button>
              {!mfa.proof ? <p className="w-full text-sm text-lamp">Verify MFA above to confirm.</p> : null}
            </>
          ) : (
            <>
              <button type="button" onClick={() => setArmed(true)} disabled={Boolean(disabledReason)} className={leverPrimary}>{applyLabel}</button>
              {disabledReason ? <p className="text-sm text-lamp">{disabledReason}</p> : null}
            </>
          )}
        </div>
      ) : null}
      <div role="status" aria-live="polite" aria-label="Apply result" className={result && resultLamp ? `px-4 py-3 ${lampPanel[resultLamp]}` : ''}>
        {result ? (
          <>
            <p className="text-sm font-semibold">{resultLabels[result.status] ?? result.status}</p>
            {result.message ? <p className={`mt-2 ${bodyText}`}>{result.message}</p> : null}
          </>
        ) : null}
      </div>
    </div>
  )
}
