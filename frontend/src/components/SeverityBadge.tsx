import type { BrokerLog } from '../types'

export function SeverityBadge({ severity }: { severity: BrokerLog['severity'] }) {
  const className = {
    debug: 'bg-slate-400/10 text-slate-200',
    info: 'bg-cyan-400/10 text-cyan-200',
    warning: 'bg-amber-400/10 text-amber-200',
    error: 'bg-rose-400/10 text-rose-200',
  }[severity]

  return <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${className}`}>{severity}</span>
}
