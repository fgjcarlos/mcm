// The one mapping from a deployment/activation status to a lamp (DESIGN.md:
// green only for verified, red only for failed, amber for everything else,
// including `applied` without activation proof, rollbacks and unknown values).
export type Lamp = 'verified' | 'caution' | 'failed'

export function deploymentLamp(status: string | undefined): Lamp {
  if (status === 'active_verified') return 'verified'
  if (status === 'failed' || status === 'rollback_failed') return 'failed'
  return 'caution'
}

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
