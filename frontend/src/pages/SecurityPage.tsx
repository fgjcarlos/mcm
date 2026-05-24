import { useEffect, useState } from 'react'
import type { SecurityEvent } from '../types'
import { listSecurityEvents } from '../lib/api'

type Props = {
  token: string
  onLogout: () => void
}

export function SecurityPage({ token, onLogout }: Props) {
  const [events, setEvents] = useState<SecurityEvent[]>([])
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    listSecurityEvents(token)
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
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Audit feed</p>
          <p className="mt-2 text-sm text-slate-300">Sanitized security events only; passwords, JWTs, and request bodies are never displayed.</p>
        </div>
        <span className="rounded-full bg-amber-400/10 px-3 py-1 text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">Recent</span>
      </div>

      {error ? (
        <div className="mt-5 rounded-2xl border border-dashed border-amber-300/30 bg-amber-400/10 p-5 text-sm text-amber-100">{error}</div>
      ) : events.length === 0 ? (
        <div className="mt-5 rounded-2xl border border-dashed border-white/10 bg-white/[0.03] p-5 text-sm text-slate-300">No security events recorded yet.</div>
      ) : (
        <div className="mt-5 space-y-3">
          {events.map((event) => (
            <article key={event.id} className="rounded-2xl border border-white/10 bg-white/[0.04] p-4">
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="rounded-full bg-rose-400/10 px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] text-rose-200">{event.category}</span>
                  <span className="rounded-full bg-slate-950/70 px-2.5 py-1 font-mono text-xs text-cyan-100">{event.reason}</span>
                </div>
                <time className="text-xs text-slate-400" dateTime={event.observed_at}>{new Date(event.observed_at).toLocaleString()}</time>
              </div>
              <dl className="mt-3 grid gap-2 text-sm text-slate-200 sm:grid-cols-2">
                <div><dt className="text-xs uppercase tracking-[0.18em] text-slate-400">User</dt><dd className="mt-1 font-mono">{event.username || 'n/a'}</dd></div>
                <div><dt className="text-xs uppercase tracking-[0.18em] text-slate-400">Source IP</dt><dd className="mt-1 font-mono">{event.source_ip || 'unknown'}</dd></div>
                <div><dt className="text-xs uppercase tracking-[0.18em] text-slate-400">Endpoint</dt><dd className="mt-1 break-all font-mono">{event.method} {event.path}</dd></div>
              </dl>
            </article>
          ))}
        </div>
      )}
    </section>
  )
}
