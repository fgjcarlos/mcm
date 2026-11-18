// The one mapping from a deployment/activation status to a lamp (DESIGN.md:
// green only for verified, red only for failed, amber for everything else,
// including `applied` without activation proof, rollbacks and unknown values).
// Render it with <Lamp tone={deploymentLamp(status)}> from features/ui.
export type Lamp = 'verified' | 'caution' | 'failed'

export function deploymentLamp(status: string | undefined): Lamp {
  if (status === 'active_verified') return 'verified'
  if (status === 'failed' || status === 'rollback_failed') return 'failed'
  return 'caution'
}

/** The boxed result of an apply, on tokens: a 1px lamp-coloured border, no tint for verified or failed. */
export const lampPanel: Record<Lamp, string> = {
  verified: 'border border-proceed/70 text-enamel-bright',
  caution: 'border border-lamp/45 bg-lamp/[0.06] text-lamp',
  failed: 'border border-danger/60 text-danger-text',
}

// shortcut: legacy pre-token classes, kept only because broker-config/listeners.test.tsx:206
// and mqtt-users/UserOnboardingFlow.test.tsx:159 (T3b/T3c surfaces) assert /amber/ on them;
// T3b/T3c switch their callers to lampPanel / <Lamp> and delete these two maps.
export const lampBox: Record<Lamp, string> = {
  verified: 'border-emerald-300/30 bg-emerald-400/10',
  caution: 'border-amber-300/30 bg-amber-400/10',
  failed: 'border-rose-300/30 bg-rose-400/10',
}

export const lampText: Record<Lamp, string> = {
  verified: 'text-emerald-200',
  caution: 'text-amber-200',
  failed: 'text-rose-200',
}
