import type { BrokerLog } from '../types'
import { SeverityBadge } from '../components/SeverityBadge'

type StreamState = 'connecting' | 'connected' | 'disconnected'

type Props = {
  logs: BrokerLog[]
  streamState: StreamState
}

export function LogsPage({ logs, streamState }: Props) {
  return (
    <section className="mt-8 rounded-[1.75rem] border border-white/10 bg-slate-900/70 p-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Log feed</p>
          <p className="mt-2 text-sm text-slate-300">WebSocket state: <span className="capitalize text-white">{streamState}</span></p>
        </div>
        <span className={`rounded-full px-3 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${streamState === 'connected' ? 'bg-emerald-400/10 text-emerald-200' : 'bg-rose-400/10 text-rose-200'}`}>
          {streamState === 'connected' ? 'Live' : streamState}
        </span>
      </div>

      <div className="mt-5 space-y-3">
        {logs.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-white/10 bg-white/[0.03] p-5 text-sm text-slate-300">
            {streamState === 'connected' ? 'No broker log events received yet.' : 'Connect to the event stream to receive broker logs.'}
          </div>
        ) : (
          logs.map((log, index) => (
            <article key={`${log.observed_at}-${log.source}-${index}`} className="rounded-2xl border border-white/10 bg-white/[0.04] p-4">
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                <div className="flex flex-wrap items-center gap-2">
                  <SeverityBadge severity={log.severity} />
                  <span className="rounded-full bg-slate-950/70 px-2.5 py-1 font-mono text-xs text-cyan-100">{log.source}</span>
                </div>
                <time className="text-xs text-slate-400" dateTime={log.observed_at}>{new Date(log.observed_at).toLocaleString()}</time>
              </div>
              <p className="mt-3 whitespace-pre-wrap break-words text-sm leading-6 text-slate-100">{log.message}</p>
            </article>
          ))
        )}
      </div>
    </section>
  )
}
