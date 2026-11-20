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

/** A deployment status in words, the same on the change route result and in the history (#359). */
export const deploymentStatusLabel: Record<string, string> = {
  active_verified: 'Verified',
  applied: 'Applied — activation not proven',
  pending_activation: 'Pending activation — not verified',
  rolled_back: 'Rolled back to the previous configuration',
  rollback_failed: 'Rollback failed',
  failed: 'Failed',
}
