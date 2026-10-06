import { useEffect, useMemo, useRef, useState } from 'react'
import { useListeners } from './useListeners'
import { composePortConflicts, validateListeners } from './listenerValidation'
import type { ListenerIssue, ListenerSpec } from './types'

const newListener = (index: number): ListenerSpec => ({
  id: `listener-${index}`,
  port: 1883,
  bind: '0.0.0.0',
  protocols: ['mqtt'],
})

export default function ListenersPanel({
  token,
  onLogout,
  role,
  onApplyStart,
  onApplySuccess,
  composeHostPorts = [],
}: {
  token: string
  onLogout: () => void
  role: string
  onApplyStart?: () => number
  onApplySuccess?: (mutationWatermark: number) => void
  composeHostPorts?: number[]
}) {
  const { listeners, preview, previewError, applyError, isLoading, requestPreview, apply, issues: backendIssues, applyResult } = useListeners({ token, onLogout })
  const [specs, setSpecs] = useState<ListenerSpec[]>([])
  const [dirty, setDirty] = useState(false)
  const [confirmRestart, setConfirmRestart] = useState(false)
  const [previewedSpecs, setPreviewedSpecs] = useState('')
  const mutationWatermark = useRef<number | null>(null)
  const canApply = role === 'admin'
  const validation = useMemo(() => validateListeners(specs), [specs])
  const composeConflicts = useMemo(() => composePortConflicts(specs, composeHostPorts), [specs, composeHostPorts])
  const blockingIssues = [...validation.issues, ...backendIssues]
  const warnings = [...validation.warnings, ...(preview?.warnings ?? [])]
  const isPreviewCurrent = Boolean(preview?.revision_id) && previewedSpecs === JSON.stringify(specs)
  const applyEnabled = canApply && isPreviewCurrent && blockingIssues.length === 0 && (!preview?.needs_restart || confirmRestart)

  useEffect(() => {
    if (!dirty) setSpecs(listeners)
  }, [listeners, dirty])

  useEffect(() => {
    if (!applyResult?.applied || mutationWatermark.current === null) return
    onApplySuccess?.(mutationWatermark.current)
    mutationWatermark.current = null
    setDirty(false)
  }, [applyResult, onApplySuccess])

  const updateListener = (id: string, changes: Partial<ListenerSpec>) => {
    setSpecs((current) => current.map((listener) => listener.id === id ? { ...listener, ...changes } : listener))
    setDirty(true)
    setConfirmRestart(false)
  }

  const handlePreview = () => {
    setConfirmRestart(false)
    const result = validateListeners(specs)
    if (result.issues.length > 0) {
      setPreviewedSpecs('')
      return
    }
    setPreviewedSpecs(JSON.stringify(specs))
    void requestPreview(specs, false)
  }

  const handleApply = async () => {
    if (!preview?.revision_id || !applyEnabled) return
    mutationWatermark.current = onApplyStart?.() ?? 0
    await apply({ revision_id: preview.revision_id, confirm: confirmRestart })
  }

  const addListener = () => {
    const nextIndex = specs.reduce((max, listener) => {
      const match = listener.id.match(/^listener-(\d+)$/)
      return match ? Math.max(max, Number(match[1])) : max
    }, 0) + 1
    setSpecs((current) => [...current, newListener(nextIndex)])
    setDirty(true)
  }

  const removeListener = (id: string) => {
    setSpecs((current) => current.filter((listener) => listener.id !== id))
    setDirty(true)
  }

  const listenerIssues = (id: string): ListenerIssue[] => blockingIssues.filter((issue) => issue.listener_id === id)

  return (
    <section className="mt-8 space-y-6">
      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">MQTT listeners</p>
            <p className="mt-1 text-sm text-slate-300">Preview listener configuration before applying the broker restart.</p>
            <p className="mt-1 text-xs text-slate-400">{dirty ? 'Unsaved changes' : 'Saved'}</p>
          </div>
          <div className="flex gap-2">
            <button type="button" onClick={addListener} className="rounded-xl border border-white/10 px-4 py-2 text-sm font-semibold text-slate-200">Add listener</button>
            <button
              type="button"
              onClick={handlePreview}
              disabled={isLoading || validation.issues.length > 0}
              className="rounded-xl border border-white/10 px-4 py-2 text-sm font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
            >
              Preview
            </button>
          </div>
        </div>

        {previewError ? <div className="mt-5 rounded-2xl border border-dashed border-amber-300/30 bg-amber-400/10 p-5 text-sm text-amber-100">{previewError}</div> : null}
        {applyError ? <div className="mt-5 rounded-2xl border border-dashed border-rose-300/30 bg-rose-400/10 p-5 text-sm text-rose-100">{applyError}</div> : null}
        {applyResult?.applied ? <div className="mt-5 rounded-2xl border border-emerald-300/30 bg-emerald-400/10 p-5 text-sm text-emerald-100">Listener configuration applied.</div> : null}

        <div className="mt-5 overflow-x-auto rounded-2xl border border-white/10 bg-slate-950/40">
          {isLoading ? (
            <p className="p-5 text-sm text-slate-400">Loading listeners…</p>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-white/10">
                  <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Port</th>
                  <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Bind</th>
                  <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Protocols</th>
                  <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Actions</th>
                </tr>
              </thead>
              <tbody>
                {specs.map((listener) => {
                  const rowIssues = listenerIssues(listener.id)
                  const conflicts = composeConflicts[listener.id] ?? []
                  return (
                    <tr key={listener.id} className="border-b border-white/5 last:border-0 align-top">
                      <td className="px-4 py-3 font-mono text-slate-200">
                        <input aria-label={`Port ${listener.id}`} type="text" inputMode="numeric" value={listener.port} onChange={(event) => updateListener(listener.id, { port: event.target.value === '' ? 0 : Number(event.target.value) })} className="w-24 rounded bg-slate-800 px-2 py-1" />
                        <span className="sr-only">{listener.port}</span>
                        {rowIssues.filter((issue) => issue.kind === 'port_range').map((issue) => <p key={issue.message} className="mt-1 text-xs text-rose-200">{issue.message}</p>)}
                        {conflicts.map((port) => <span key={port} className="mt-1 block text-xs font-sans text-amber-200">Port {port} conflicts with a Compose host port</span>)}
                      </td>
                      <td className="px-4 py-3 font-mono text-cyan-100">
                        <input aria-label={`Bind ${listener.id}`} value={listener.bind} onChange={(event) => updateListener(listener.id, { bind: event.target.value })} className="w-36 rounded bg-slate-800 px-2 py-1" />
                        <span className="sr-only">{listener.bind}</span>
                        {rowIssues.filter((issue) => issue.kind === 'bind').map((issue) => <p key={issue.message} className="mt-1 text-xs text-rose-200">{issue.message}</p>)}
                      </td>
                      <td className="px-4 py-3 text-slate-300">
                        <input aria-label={`Protocols ${listener.id}`} value={listener.protocols.join(', ')} onChange={(event) => updateListener(listener.id, { protocols: event.target.value.split(',').map((value) => value.trim()).filter(Boolean) })} className="w-36 rounded bg-slate-800 px-2 py-1" />
                        <span className="sr-only">{listener.protocols.join(', ')}</span>
                        {rowIssues.filter((issue) => issue.kind.startsWith('protocols')).map((issue) => <p key={issue.message} className="mt-1 text-xs text-rose-200">{issue.message}</p>)}
                      </td>
                      <td className="px-4 py-3"><button type="button" aria-label={`Remove listener ${listener.id}`} onClick={() => removeListener(listener.id)} className="text-rose-200">Remove</button></td>
                    </tr>
                  )
                })}
                {specs.length === 0 ? <tr><td colSpan={4} className="p-5 text-sm text-slate-400">No listeners configured.</td></tr> : null}
              </tbody>
            </table>
          )}
        </div>

        {blockingIssues.length > 0 ? (
          <div className="mt-5 rounded-2xl border border-rose-300/30 bg-rose-400/10 p-5">
            <p className="text-xs font-semibold uppercase tracking-[0.18em] text-rose-200">Issues</p>
            <ul className="mt-3 list-disc space-y-1 pl-5 text-sm text-rose-100">
              {blockingIssues.map((issue, index) => <li key={`${issue.kind}-${issue.listener_id ?? index}`}>{issue.message}</li>)}
            </ul>
          </div>
        ) : null}
        {warnings.length > 0 || Object.keys(composeConflicts).length > 0 ? (
          <div className="mt-5 rounded-2xl border border-amber-300/30 bg-amber-400/10 p-5">
            <p className="text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">Warnings</p>
            <ul className="mt-3 list-disc space-y-1 pl-5 text-sm text-amber-100">
              {warnings.map((warning, index) => <li key={`${warning.kind}-${warning.listener_id ?? index}`}>{warning.message}</li>)}
              {Object.entries(composeConflicts).flatMap(([id, ports]) => ports.map((port) => <li key={`${id}-${port}`}>Listener {id} port {port} conflicts with a Compose host port</li>))}
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
