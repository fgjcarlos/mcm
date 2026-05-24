import { useEffect, useState } from 'react'
import type { AuditEvent } from '../types'
import { listAuditEvents } from '../lib/api'

type Props = {
  token: string
  onLogout: () => void
}

export function AuditPage({ token, onLogout }: Props) {
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    listAuditEvents(token)
      .then((body) => {
        if (cancelled) return
        setEvents(body.events ?? [])
        setError('')
      })
      .catch((err: Error & { status?: number }) => {
        if (cancelled) return
        if (err.status === 401) {
          onLogout()
          return
        }
        setError(err.message)
      })
    return () => { cancelled = true }
  }, [token, onLogout])

  return (
    <section className="mt-8 rounded-[1.75rem] border border-white/10 bg-slate-900/70 p-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Audit history</p>
          <p className="mt-2 text-sm text-slate-300">Recent administrative user and ACL mutations.</p>
        </div>
        <span className="rounded-full bg-cyan-400/10 px-3 py-1 text-xs font-semibold uppercase tracking-[0.18em] text-cyan-200">Read only</span>
      </div>

      <div className="mt-5 space-y-3">
        {error ? (
          <div className="rounded-2xl border border-dashed border-amber-300/20 bg-amber-400/10 p-5 text-sm text-amber-100">{error}</div>
        ) : events.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-white/10 bg-white/[0.03] p-5 text-sm text-slate-300">No audit events recorded yet.</div>
        ) : (
          events.map((event) => (
            <article key={event.id} className="rounded-2xl border border-white/10 bg-white/[0.04] p-4">
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                <div className="flex flex-wrap items-center gap-2">
                  <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${event.result === 'success' ? 'bg-emerald-400/10 text-emerald-200' : 'bg-rose-400/10 text-rose-200'}`}>{event.result}</span>
                  <span className="rounded-full bg-slate-950/70 px-2.5 py-1 font-mono text-xs text-cyan-100">{event.actor}</span>
                  <span className="rounded-full bg-white/[0.06] px-2.5 py-1 font-mono text-xs text-slate-200">{event.action}</span>
                </div>
                <time className="text-xs text-slate-400" dateTime={event.occurred_at}>{new Date(event.occurred_at).toLocaleString()}</time>
              </div>
              <p className="mt-3 break-all text-sm text-slate-100">
                {event.resource_type}{event.resource_id ? ` #${event.resource_id}` : ''}
              </p>
              {event.metadata && Object.keys(event.metadata).length > 0 ? (
                <pre className="mt-3 max-h-32 overflow-auto rounded-2xl border border-white/10 bg-slate-950/70 p-3 text-xs text-slate-200">{JSON.stringify(event.metadata, null, 2)}</pre>
              ) : null}
            </article>
          ))
        )}
      </div>
    </section>
  )
}
