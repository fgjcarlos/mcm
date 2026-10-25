import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { isForbiddenResponseError, isUnauthorizedResponseError } from '../api/client'
import { adoptACLSource, fetchACLSource } from './api'
import type { ACLSource } from './types'

// ACLSourcePanel brings an existing Mosquitto ACL file under the
// management of MCM.
//
// Adopting keeps the file verbatim: the deploy composes the managed rules
// into it instead of replacing it, so a rule MCM does not model survives.
// The preview of the deploy reports the access the composition would drop,
// and an apply that would drop access MCM can neither reproduce nor
// attribute to a managed MQTT user is refused.
function ACLSourcePanel({ token, onLogout, canWrite }: { token: string; onLogout: () => void; canWrite: boolean }) {
  const [source, setSource] = useState<ACLSource | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [formPath, setFormPath] = useState('')
  const [formBody, setFormBody] = useState('')
  const [formError, setFormError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [refreshTick, setRefreshTick] = useState(0)

  const reload = useCallback(() => setRefreshTick((n) => n + 1), [])

  useEffect(() => {
    let cancelled = false
    fetchACLSource(token, onLogout)
      .then((result) => {
        if (cancelled) return
        setSource(result)
        setError('')
        setLoading(false)
      })
      .catch((err: Error) => {
        if (cancelled) return
        if (isUnauthorizedResponseError(err)) return
        setError(err.message)
        setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [token, refreshTick, onLogout])

  const handleAdopt = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting || !canWrite) return
    setSubmitting(true)
    setFormError('')
    try {
      const result = await adoptACLSource(token, onLogout, {
        path: formPath.trim() || undefined,
        body: formBody.length > 0 ? formBody : undefined,
      })
      setSource(result)
      setFormBody('')
      setFormPath('')
      reload()
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) {
        setFormError((err as Error).message)
        return
      }
      setFormError((err as Error).message || 'Could not reach the server.')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <section className="mt-8 space-y-4">
      <div>
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Existing ACL file</p>
        <p className="mt-1 text-sm text-slate-300">
          Adopt an ACL file that already exists on the broker. MCM keeps its content verbatim and composes the managed
          rules into it, so nothing is lost.
        </p>
      </div>

      {error ? (
        <div className="rounded-2xl border border-dashed border-amber-300/30 bg-amber-400/10 p-5 text-sm text-amber-100">{error}</div>
      ) : null}

      {loading ? (
        <p className="text-sm text-slate-400">Loading the adopted ACL source…</p>
      ) : source?.adopted ? (
        <div className="rounded-2xl border border-emerald-300/20 bg-emerald-400/5 p-4">
          <p className="text-sm text-emerald-100">
            The ACL file <span className="font-mono">{source.path}</span> is adopted.
          </p>
          {source.content_hash ? <p className="mt-1 font-mono text-xs text-slate-400">sha256 {source.content_hash}</p> : null}
          {source.body ? (
            <pre className="mt-3 max-h-64 overflow-auto rounded-xl border border-white/10 bg-slate-950/70 p-3 font-mono text-xs text-slate-300">
              {source.body}
            </pre>
          ) : null}
        </div>
      ) : (
        <div className="rounded-2xl border border-dashed border-white/10 bg-slate-900/40 p-5 text-sm text-slate-400">
          No ACL file was adopted yet. The deploy writes the ACL file MCM manages.
        </div>
      )}

      <form onSubmit={handleAdopt} className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Adopt an ACL file</p>
        <div className="mt-4 space-y-4">
          <label className="block">
            <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Path (optional)</span>
            <input
              type="text"
              value={formPath}
              onChange={(e) => setFormPath(e.target.value)}
              placeholder="/mosquitto/config/acl"
              className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
            />
            <span className="mt-1 block text-xs text-slate-400">
              The file to take over. Leave it empty to adopt the ACL file MCM manages, and leave the content empty to read
              the file from that path.
            </span>
          </label>
          <label className="block">
            <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Content (optional)</span>
            <textarea
              value={formBody}
              onChange={(e) => setFormBody(e.target.value)}
              rows={6}
              placeholder={'user alice\ntopic read sensors/#\npattern readwrite devices/%u/data'}
              className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 font-mono text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
            />
          </label>
          {formError ? (
            <div className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">{formError}</div>
          ) : null}
          <div className="flex gap-3">
            <button
              type="submit"
              disabled={submitting || !canWrite}
              title={!canWrite ? 'Requires the operator role' : undefined}
              className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
            >
              {submitting ? 'Adopting…' : 'Adopt ACL file'}
            </button>
            <button
              type="button"
              onClick={reload}
              className="rounded-xl border border-white/10 px-4 py-2 text-sm text-slate-300 transition hover:border-white/20 hover:text-white"
            >
              Refresh
            </button>
          </div>
        </div>
      </form>
    </section>
  )
}

export default ACLSourcePanel
