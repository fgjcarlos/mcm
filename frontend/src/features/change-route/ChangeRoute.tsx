import { useState, type ReactNode } from 'react'
import RecentMFAConfirmation from '../mfa/RecentMFAConfirmation'
import type { useRecentMFA } from '../mfa/useRecentMFA'
import { deploymentLamp, lampBox, lampText, type Lamp } from '../status/deploymentLamp'

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

const lampDot: Record<Lamp | 'set' | 'off', string> = {
  verified: 'bg-emerald-400', caution: 'bg-amber-400', failed: 'bg-rose-400', set: 'bg-amber-400', off: 'border border-slate-500',
}
const button = 'rounded-xl px-4 py-2 text-sm font-semibold transition disabled:opacity-50'

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
  const stages: [string, string, Lamp | 'set' | 'off'][] = [
    ['Review', hasChanges ? 'done' : 'waiting', hasChanges ? 'set' : 'off'],
    ['MFA', mfa.proof ? 'done' : armed ? 'waiting' : 'not reached', mfa.proof ? 'set' : 'off'],
    ['Apply', running ? 'in progress' : result ? 'done' : armed ? 'waiting' : 'not reached', running || result ? 'set' : 'off'],
    ['Probe', running ? 'in progress' : result ? 'done' : 'not reached', running || result ? 'set' : 'off'],
    ['Result', resultLamp ?? 'not reached', resultLamp ?? 'off'],
  ]

  return (
    <div className="space-y-4">
      <ol aria-label="Change route" className="flex flex-wrap gap-x-5 gap-y-2 text-xs">
        {stages.map(([name, state, lamp]) => (
          <li key={name} aria-label={`${name}: ${state}`} className="inline-flex items-center gap-2">
            <span aria-hidden="true" className={`h-2.5 w-2.5 rounded-full ${lampDot[lamp]}`} />
            <span className="font-semibold uppercase tracking-[0.18em] text-slate-200">{name}</span>
            <span className={lamp === 'off' ? 'text-slate-400' : lamp === 'set' ? 'text-amber-200' : lampText[lamp]}>{state}</span>
          </li>
        ))}
      </ol>
      {children}
      {armed ? <RecentMFAConfirmation state={mfa} /> : null}
      {hasChanges ? (
        <div className="flex flex-wrap items-center gap-3">
          {running ? <p className="text-sm text-amber-200">Applying and verifying activation…</p> : armed ? (
            <>
              <span className="text-sm text-slate-300">Apply this change? MCM writes it, activates it on the broker and probes the result.</span>
              <button type="button" onClick={() => { void confirm() }} disabled={!mfa.proof} className={`${button} bg-cyan-500 text-white hover:bg-cyan-400`}>Confirm {applyLabel}</button>
              <button type="button" onClick={() => setArmed(false)} className={`${button} border border-white/10 text-slate-300 hover:text-white`}>Cancel</button>
              {!mfa.proof ? <p className="w-full text-sm text-amber-200">Verify MFA above to confirm.</p> : null}
            </>
          ) : (
            <>
              <button type="button" onClick={() => setArmed(true)} disabled={Boolean(disabledReason)} className={`${button} bg-cyan-500 text-white hover:bg-cyan-400`}>{applyLabel}</button>
              {disabledReason ? <p className="text-sm text-amber-200">{disabledReason}</p> : null}
            </>
          )}
        </div>
      ) : null}
      <div role="status" aria-live="polite" aria-label="Apply result" className={result && resultLamp ? `rounded-2xl border p-5 ${lampBox[resultLamp]} ${lampText[resultLamp]}` : ''}>
        {result ? (
          <>
            <p className="text-sm font-semibold">{resultLabels[result.status] ?? result.status}</p>
            {result.message ? <p className="mt-2 text-sm text-slate-300">{result.message}</p> : null}
          </>
        ) : null}
      </div>
    </div>
  )
}
