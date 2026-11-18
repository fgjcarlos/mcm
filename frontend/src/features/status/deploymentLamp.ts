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
