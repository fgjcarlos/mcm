import { useEffect, useRef, useState } from 'react'
import { useListeners } from './useListeners'

export default function ListenersPanel({
  token,
  onLogout,
  role,
  onApplyStart,
  onApplySuccess,
}: {
  token: string
  onLogout: () => void
  role: string
  onApplyStart?: () => number
  onApplySuccess?: (mutationWatermark: number) => void
}) {
  const { listeners, preview, previewError, applyError, isLoading, requestPreview, apply, issues, applyResult } = useListeners({ token, onLogout })
  const [confirmRestart, setConfirmRestart] = useState(false)
  const mutationWatermark = useRef<number | null>(null)
  const canApply = role === 'admin'
  const applyEnabled = canApply && Boolean(preview?.revision_id) && (!preview?.needs_restart || confirmRestart)

  useEffect(() => {
    if (!applyResult?.applied || mutationWatermark.current === null) return
    onApplySuccess?.(mutationWatermark.current)
    mutationWatermark.current = null
  }, [applyResult, onApplySuccess])

  const handlePreview = () => {
    setConfirmRestart(false)
    void requestPreview(listeners, false)
  }

  const handleApply = async () => {
    if (!preview?.revision_id || !applyEnabled) return
    mutationWatermark.current = onApplyStart?.() ?? 0
    await apply({ revision_id: preview.revision_id, confirm: confirmRestart })
  }

  return (
    <section className="mt-8 space-y-6">
      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">MQTT listeners</p>
            <p className="mt-1 text-sm text-slate-300">Preview listener configuration before applying the broker restart.</p>
          </div>
          <button
            type="button"
            onClick={handlePreview}
            disabled={isLoading}
            className="rounded-xl border border-white/10 px-4 py-2 text-sm font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
          >
            Preview
          </button>
        </div>

        {previewError ? <div className="mt-5 rounded-2xl border border-dashed border-amber-300/30 bg-amber-400/10 p-5 text-sm text-amber-100">{previewError}</div> : null}
        {applyError ? <div className="mt-5 rounded-2xl border border-dashed border-rose-300/30 bg-rose-400/10 p-5 text-sm text-rose-100">{applyError}</div> : null}
        {applyResult?.applied ? <div className="mt-5 rounded-2xl border border-emerald-300/30 bg-emerald-400/10 p-5 text-sm text-emerald-100">Listener configuration applied.</div> : null}

        <div className="mt-5 overflow-x-auto rounded-2xl border border-white/10 bg-slate-950/40">
          {isLoading ? (
            <p className="p-5 text-sm text-slate-400">Loading listeners…</p>
          ) : listeners.length === 0 ? (
            <p className="p-5 text-sm text-slate-400">No listeners configured.</p>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-white/10">
                  <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Port</th>
                  <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Bind</th>
                  <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Protocols</th>
                </tr>
              </thead>
              <tbody>
                {listeners.map((listener) => (
                  <tr key={listener.id} className="border-b border-white/5 last:border-0">
                    <td className="px-4 py-3 font-mono text-slate-200">{listener.port}</td>
                    <td className="px-4 py-3 font-mono text-cyan-100">{listener.bind}</td>
                    <td className="px-4 py-3 text-slate-300">{listener.protocols.join(', ')}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        {issues.length > 0 ? (
          <div className="mt-5 rounded-2xl border border-rose-300/30 bg-rose-400/10 p-5">
            <p className="text-xs font-semibold uppercase tracking-[0.18em] text-rose-200">Issues</p>
            <ul className="mt-3 list-disc space-y-1 pl-5 text-sm text-rose-100">
              {issues.map((issue, index) => <li key={`${issue.kind}-${issue.listener_id ?? index}`}>{issue.message}</li>)}
            </ul>
          </div>
        ) : null}

        <div className="mt-5 space-y-4">
          {preview ? (
            <>
              <p className="font-mono text-sm text-cyan-100">{preview.revision_id}</p>
              {preview.needs_restart ? (
                <label className="flex items-center gap-2 text-sm text-slate-200">
                  <input type="checkbox" checked={confirmRestart} onChange={(event) => setConfirmRestart(event.target.checked)} />
                  Confirm restart
                </label>
              ) : null}
            </>
          ) : null}
          <button
            type="button"
            onClick={() => { void handleApply() }}
            disabled={!applyEnabled}
            className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
          >
            Apply (restart required)
          </button>
          {preview ? (
            <>
              <div>
                <p className="mb-2 text-xs font-semibold uppercase tracking-[0.18em] text-cyan-300">Diff</p>
                <pre className="max-h-64 overflow-auto rounded-2xl border border-white/10 bg-slate-950/70 p-4 font-mono text-xs leading-5 text-slate-200">{preview.diff.split('\n').map((line, index) => <span key={index} className="block">{line || ' '}</span>)}</pre>
              </div>
              <div>
                <p className="mb-2 text-xs font-semibold uppercase tracking-[0.18em] text-cyan-300">Rendered configuration</p>
                <pre className="max-h-64 overflow-auto rounded-2xl border border-white/10 bg-slate-950/70 p-4 font-mono text-xs leading-5 text-slate-200">{preview.rendered.split('\n').map((line, index) => <span key={index} className="block">{line || ' '}</span>)}</pre>
              </div>
            </>
          ) : null}
        </div>
      </div>
    </section>
  )
}
