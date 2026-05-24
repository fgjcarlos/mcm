import type { BrokerTrafficItem } from '../types'

export function TrafficBars({ items }: { items: BrokerTrafficItem[] }) {
  const maxCount = Math.max(...items.map((item) => item.count), 1)

  return (
    <div className="mt-4 space-y-4">
      {items.map((item) => (
        <div key={item.name}>
          <div className="flex items-center justify-between gap-3 text-sm">
            <span className="break-all font-mono text-cyan-100">{item.name}</span>
            <span className="text-slate-300">{item.count}</span>
          </div>
          <div className="mt-2 h-2 overflow-hidden rounded-full bg-slate-800">
            <div className="h-full rounded-full bg-cyan-300" style={{ width: `${Math.max(6, (item.count / maxCount) * 100)}%` }} />
          </div>
          <p className="mt-1 text-xs text-slate-400">{item.percentage.toFixed(0)}% of recent messages</p>
        </div>
      ))}
    </div>
  )
}
