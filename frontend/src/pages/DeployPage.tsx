import { useCallback, useEffect, useState } from 'react'
import type { Deployment, DeployPreview } from '../types'
import { deployApply, deployPreview, listDeployments } from '../lib/api'
import { DiffBlock } from '../components/DiffBlock'

type Props = {
  token: string
}

export function DeployPage({ token }: Props) {
  const [preview, setPreview] = useState<DeployPreview | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewError, setPreviewError] = useState('')
  const [unavailable, setUnavailable] = useState(false)
  const [applying, setApplying] = useState(false)
  const [applyResult, setApplyResult] = useState<Deployment | null>(null)
  const [applyError, setApplyError] = useState('')
  const [confirmApply, setConfirmApply] = useState(false)
  const [deployments, setDeployments] = useState<Deployment[]>([])
  const [historyLoading, setHistoryLoading] = useState(true)
  const [historyError, setHistoryError] = useState('')
  const [historyTick, setHistoryTick] = useState(0)

  const fetchHistory = useCallback(() => { setHistoryTick((n) => n + 1) }, [])

  useEffect(() => {
    let cancelled = false
    listDeployments(token)
      .then((body) => {
        if (cancelled) return
        setDeployments(body.deployments ?? [])
        setHistoryError('')
        setHistoryLoading(false)
      })
      .catch((err: Error & { status?: number }) => {
        if (cancelled) return
        if (err.status === 404 || err.status === 422) {
          setUnavailable(true)
          setHistoryLoading(false)
          return
        }
        setHistoryError(err.message)
        setHistoryLoading(false)
      })
    return () => { cancelled = true }
  }, [token, historyTick])

  const handlePreview = async () => {
    setPreviewLoading(true)
    setPreviewError('')
    setPreview(null)
    try {
      const data = await deployPreview(token)
      setPreview(data)
    } catch (err) {
      const typedErr = err as Error & { status?: number }
      if (typedErr.status === 404 || typedErr.status === 422) {
        setUnavailable(true)
        return
      }
      setPreviewError(typedErr.message ?? 'Preview failed.')
    } finally {
      setPreviewLoading(false)
    }
  }

  const handleApply = async () => {
    setApplying(true)
    setApplyError('')
    setApplyResult(null)
    setConfirmApply(false)
    try {
      const result = await deployApply(token)
      setApplyResult(result)
      setPreview(null)
      fetchHistory()
    } catch (err) {
      const typedErr = err as Error & { status?: number }
      if (typedErr.status === 404 || typedErr.status === 422) {
        setUnavailable(true)
        return
      }
      setApplyError(typedErr.message ?? 'Apply failed.')
    } finally {
      setApplying(false)
    }
  }

  if (unavailable) {
    return (
      <section className="mt-8">
        <div className="rounded-2xl border border-dashed border-amber-300/25 bg-amber-400/10 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-amber-200">Not configured</p>
          <p className="mt-2 text-sm text-slate-300">Deploy functionality is not configured on this MCM instance.</p>
        </div>
      </section>
    )
  }

  return (
    <section className="mt-8 space-y-6">
      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Configuration preview</p>
            <p className="mt-1 text-sm text-slate-300">Generate a diff of pending ACL and password file changes before applying.</p>
          </div>
          <div className="flex items-center gap-3">
            {preview?.has_changes ? (
              confirmApply ? (
                <span className="inline-flex items-center gap-2">
                  <span className="text-xs text-slate-300">Apply changes?</span>
                  <button
                    type="button"
                    onClick={() => { void handleApply() }}
                    disabled={applying}
                    className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
                  >
                    {applying ? 'Applying…' : 'Confirm Apply'}
                  </button>
                  <button
                    type="button"
                    onClick={() => setConfirmApply(false)}
                    className="rounded-xl border border-white/10 px-4 py-2 text-sm text-slate-300 transition hover:border-white/20 hover:text-white"
                  >
                    Cancel
                  </button>
                </span>
              ) : (
                <button
                  type="button"
                  onClick={() => setConfirmApply(true)}
                  className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400"
                >
                  Apply
                </button>
              )
            ) : null}
            <button
              type="button"
              onClick={() => { void handlePreview() }}
              disabled={previewLoading}
              className="rounded-xl border border-white/10 px-4 py-2 text-sm font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
            >
              {previewLoading ? 'Generating…' : 'Preview Changes'}
            </button>
          </div>
        </div>

        {previewError ? (
          <div className="mt-5 rounded-2xl border border-dashed border-amber-300/30 bg-amber-400/10 p-5 text-sm text-amber-100">{previewError}</div>
        ) : null}

        {applyError ? (
          <div className="mt-5 rounded-2xl border border-dashed border-rose-300/30 bg-rose-400/10 p-5 text-sm text-rose-100">{applyError}</div>
        ) : null}

        {applyResult ? (
          <div className={`mt-5 rounded-2xl border p-5 ${applyResult.status === 'applied' ? 'border-emerald-300/30 bg-emerald-400/10' : 'border-amber-300/30 bg-amber-400/10'}`}>
            <p className={`text-xs font-semibold uppercase tracking-[0.18em] ${applyResult.status === 'applied' ? 'text-emerald-200' : 'text-amber-200'}`}>
              {applyResult.status}
            </p>
            {applyResult.message ? <p className="mt-2 text-sm text-slate-300">{applyResult.message}</p> : null}
          </div>
        ) : null}

        {preview ? (
          <div className="mt-5 space-y-4">
            {!preview.has_changes ? (
              <div className="rounded-2xl border border-dashed border-white/10 bg-white/[0.03] p-5 text-sm text-slate-300">No pending changes — configuration is up to date.</div>
            ) : null}
            {preview.acl_diff ? (
              <div>
                <p className="mb-2 text-xs font-semibold uppercase tracking-[0.18em] text-cyan-300">ACL diff</p>
                <DiffBlock content={preview.acl_diff} />
              </div>
            ) : null}
            {preview.passwd_diff ? (
              <div>
                <p className="mb-2 text-xs font-semibold uppercase tracking-[0.18em] text-cyan-300">Password file diff</p>
                <DiffBlock content={preview.passwd_diff} />
              </div>
            ) : null}
          </div>
        ) : null}
      </div>

      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Deployment history</p>

        {historyError ? (
          <div className="mt-4 rounded-2xl border border-dashed border-amber-300/30 bg-amber-400/10 p-5 text-sm text-amber-100">{historyError}</div>
        ) : historyLoading ? (
          <p className="mt-4 text-sm text-slate-400">Loading history…</p>
        ) : deployments.length === 0 ? (
          <p className="mt-4 text-sm text-slate-400">No deployments recorded yet.</p>
        ) : (
          <table className="mt-4 w-full text-sm">
            <thead>
              <tr className="border-b border-white/10">
                <th className="px-0 py-2 text-left text-xs uppercase tracking-[0.18em] text-slate-400">ID</th>
                <th className="px-4 py-2 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Status</th>
                <th className="px-4 py-2 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Message</th>
                <th className="px-0 py-2 text-right text-xs uppercase tracking-[0.18em] text-slate-400">Time</th>
              </tr>
            </thead>
            <tbody>
              {deployments.map((dep) => (
                <tr key={dep.id} className="border-b border-white/5 last:border-0">
                  <td className="py-3 pr-4 font-mono text-xs text-slate-400">{dep.id.slice(0, 8)}</td>
                  <td className="px-4 py-3">
                    <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${dep.status === 'applied' ? 'bg-emerald-400/10 text-emerald-200' : 'bg-amber-400/10 text-amber-200'}`}>
                      {dep.status}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-sm text-slate-300">{dep.message ?? '—'}</td>
                  <td className="py-3 pl-4 text-right text-xs text-slate-400">{new Date(dep.created_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </section>
  )
}
