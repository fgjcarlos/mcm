import type { BrokerRatePoint } from '../types'

export function RateChart({ points }: { points: BrokerRatePoint[] }) {
  const visiblePoints = points.slice(-8)
  const maxCount = Math.max(...visiblePoints.map((point) => point.count), 1)

  return (
    <div className="mt-5 flex h-24 items-end gap-1.5 rounded-2xl border border-white/10 bg-slate-950/40 p-3" aria-label="Recent message rate chart">
      {visiblePoints.length === 0 ? (
        <span className="self-center text-sm text-slate-300">No rate samples yet.</span>
      ) : (
        visiblePoints.map((point) => (
          <div key={point.timestamp} className="flex flex-1 flex-col items-center justify-end gap-1">
            <div className="w-full rounded-t bg-cyan-300/80" style={{ height: `${Math.max(8, (point.count / maxCount) * 100)}%` }} title={`${point.count} messages`} />
            <span className="text-[0.6rem] text-slate-400">{new Date(point.timestamp).toLocaleTimeString([], { minute: '2-digit', second: '2-digit' })}</span>
          </div>
        ))
      )}
    </div>
  )
}
