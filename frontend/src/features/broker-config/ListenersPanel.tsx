import { useMemo, useState } from 'react'
import { useListeners } from './useListeners'
import { useRecentMFA } from '../mfa/useRecentMFA'
import RecentMFAConfirmation from '../mfa/RecentMFAConfirmation'
import { deploymentLamp, lampPanel } from '../status/deploymentLamp'
import { DiffBlock, Section } from '../ui/primitives'
import { bodyText, codeBlock, fieldHint, input, leverDanger, leverPrimary, leverSecondary, notice, select, tableHead, tableRow } from '../ui/styles'
import { DraftState, Findings } from './panelParts'
import { useCertificateProfiles } from './useCertificates'
import { composePortConflicts, validateListeners } from './listenerValidation'
import type { ListenerIssue, ListenerSpec } from './types'

const newListener = (index: number): ListenerSpec => ({
  ID: `listener-${index}`,
  Port: 1883,
  Bind: '0.0.0.0',
  Protocols: ['mqtt'],
})

export default function ListenersPanel({
  token,
  onLogout,
  role,
  onApplyStart,
  onApplySuccess,
  composeHostPorts: composeHostPortsOverride,
}: {
  token: string
  onLogout: () => void
  role: string
  onApplyStart?: () => number
  onApplySuccess?: (mutationWatermark: number) => void
  composeHostPorts?: number[]
}) {
  const mfa = useRecentMFA(token, onLogout)
  const { listeners, composeHostPorts: fetchedComposeHostPorts, preview, previewedSpecs, previewError, applyError, isLoading, isApplying, requestPreview, apply, issues: backendIssues, applyResult } = useListeners({ token, onLogout, onMFARejected: mfa.clear })
  const { profiles: certificateProfiles, isDisabled: certificatesDisabled } = useCertificateProfiles({ token, onLogout })
  const composeHostPorts = composeHostPortsOverride ?? fetchedComposeHostPorts
  const [draft, setDraft] = useState<ListenerSpec[]>()
  const specs = draft ?? listeners
  const dirty = draft !== undefined
  const [confirmRestart, setConfirmRestart] = useState(false)
  const canApply = role === 'admin'
  const validation = useMemo(() => validateListeners(specs), [specs])
  const composeConflicts = useMemo(() => composePortConflicts(specs, composeHostPorts), [specs, composeHostPorts])
  const blockingIssues = [...validation.issues, ...backendIssues]
  const warnings = [...validation.warnings, ...(preview?.warnings ?? [])]
  const isPreviewCurrent = Boolean(preview?.revision_id) && JSON.stringify(previewedSpecs) === JSON.stringify(specs)
  const applyEnabled = canApply && Boolean(mfa.proof) && !isApplying && isPreviewCurrent && blockingIssues.length === 0 && (!preview?.needs_restart || confirmRestart)

  const updateListener = (id: string, changes: Partial<ListenerSpec>) => {
    setDraft((current) => (current ?? listeners).map((listener) => listener.ID === id ? { ...listener, ...changes } : listener))
    setConfirmRestart(false)
  }

  const updateListenerProfile = (id: string, profile: string) => {
    setDraft((current) => (current ?? listeners).map((listener) => {
      if (listener.ID !== id) return listener
      const options = { ...(listener.Options ?? {}) }
      if (!profile) {
        // Dropping the profile also drops the mutual TLS request: the
        // backend rejects a client certificate requirement without a CA.
        delete options.require_certificate
      }
      return { ...listener, Options: options, tls_profile: profile || undefined }
    }))
    setConfirmRestart(false)
  }

  const updateListenerOption = (id: string, key: string, value: string | undefined) => {
    setDraft((current) => (current ?? listeners).map((listener) => {
      if (listener.ID !== id) return listener
      const options = { ...(listener.Options ?? {}) }
      if (value === undefined) {
        delete options[key]
      } else {
        options[key] = value
      }
      return { ...listener, Options: options }
    }))
    setConfirmRestart(false)
  }

  const handlePreview = () => {
    setConfirmRestart(false)
    const result = validateListeners(specs)
    if (result.issues.length > 0) {
      return
    }
    void requestPreview(specs, false)
  }

  const handleApply = async () => {
    if (!preview?.revision_id || !applyEnabled) return
    const mutationWatermark = onApplyStart?.() ?? 0
    const result = await apply({ revision_id: preview.revision_id, confirm: confirmRestart }, mfa.proof)
    if (result?.applied) {
      setDraft(undefined)
      onApplySuccess?.(mutationWatermark)
    }
  }

  const addListener = () => {
    const nextIndex = specs.reduce((max, listener) => {
      const match = listener.ID.match(/^listener-(\d+)$/)
      return match ? Math.max(max, Number(match[1])) : max
    }, 0) + 1
    setDraft((current) => [...(current ?? listeners), newListener(nextIndex)])
  }

  const removeListener = (id: string) => {
    setDraft((current) => (current ?? listeners).filter((listener) => listener.ID !== id))
  }

  const listenerIssues = (id: string): ListenerIssue[] => blockingIssues.filter((issue) => issue.listener_id === id)

  return (
    <section className="mt-8 space-y-10">
      <Section
        title="MQTT listeners"
        description="Preview listener configuration before applying the broker restart."
        aside={
          <div className="flex flex-wrap gap-2">
            <button type="button" onClick={addListener} disabled={isLoading || isApplying} className={leverSecondary}>Add listener</button>
            <button
              type="button"
              onClick={handlePreview}
              disabled={isLoading || isApplying || validation.issues.length > 0}
              className={leverSecondary}
            >
              Preview
            </button>
          </div>
        }
      >
        <DraftState dirty={dirty} />

        {previewError ? <div className={`mt-5 ${notice.failed}`}>{previewError}</div> : null}
        {applyError ? <div className={`mt-5 ${notice.failed}`}>{applyError}</div> : null}
        {applyResult?.applied ? <div className={`mt-5 px-4 py-3 text-sm ${lampPanel[deploymentLamp('applied')]}`}>Listener configuration applied.</div> : null}

        <div className="mt-5 overflow-x-auto border-y border-track-strong">
          {isLoading ? (
            <p className={`py-6 ${bodyText}`}>Loading listeners…</p>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-track-strong">
                  <th className={`${tableHead} text-left`}>Port</th>
                  <th className={`${tableHead} text-left`}>Bind</th>
                  <th className={`${tableHead} text-left`}>Protocols</th>
                  <th className={`${tableHead} text-left`}>TLS</th>
                  <th className={`${tableHead} text-left`}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {specs.map((listener) => {
                  const rowIssues = listenerIssues(listener.ID)
                  const conflicts = composeConflicts[listener.ID] ?? []
                  return (
                    <tr key={listener.ID} className={`${tableRow} align-top`}>
                      <td className="py-3 pr-4">
                        <input aria-label={`Port ${listener.ID}`} disabled={isApplying} type="text" inputMode="numeric" value={listener.Port} onChange={(event) => updateListener(listener.ID, { Port: event.target.value === '' ? 0 : Number(event.target.value) })} className={`w-24 font-mono ${input}`} />
                        <span className="sr-only">{listener.Port}</span>
                        {rowIssues.filter((issue) => issue.kind === 'port_range').map((issue) => <p key={issue.message} className="mt-1 text-xs text-danger-text">{issue.message}</p>)}
                        {conflicts.map((port) => <span key={port} className="mt-1 block text-xs text-lamp">Port {port} conflicts with a Compose host port</span>)}
                      </td>
                      <td className="py-3 pr-4">
                        <input aria-label={`Bind ${listener.ID}`} disabled={isApplying} value={listener.Bind} onChange={(event) => updateListener(listener.ID, { Bind: event.target.value })} className={`w-36 font-mono ${input}`} />
                        <span className="sr-only">{listener.Bind}</span>
                        {rowIssues.filter((issue) => issue.kind === 'bind').map((issue) => <p key={issue.message} className="mt-1 text-xs text-danger-text">{issue.message}</p>)}
                      </td>
                      <td className="py-3 pr-4">
                        <input aria-label={`Protocols ${listener.ID}`} disabled={isApplying} value={listener.Protocols.join(', ')} onChange={(event) => updateListener(listener.ID, { Protocols: event.target.value.split(',').map((value) => value.trim()).filter(Boolean) })} className={`w-36 font-mono ${input}`} />
                        <span className="sr-only">{listener.Protocols.join(', ')}</span>
                        {rowIssues.filter((issue) => issue.kind.startsWith('protocols')).map((issue) => <p key={issue.message} className="mt-1 text-xs text-danger-text">{issue.message}</p>)}
                      </td>
                      <td className="py-3 pr-4">
                        {certificatesDisabled ? (
                          <p className={fieldHint}>Certificate profiles are unavailable.</p>
                        ) : (
                          <>
                            <select
                              aria-label={`TLS profile ${listener.ID}`}
                              disabled={isApplying}
                              value={listener.tls_profile ?? ''}
                              onChange={(event) => updateListenerProfile(listener.ID, event.target.value)}
                              className={`w-44 ${select}`}
                            >
                              <option value="">None (plain MQTT)</option>
                              {certificateProfiles.map((profile) => <option key={profile.name} value={profile.name}>{profile.name}</option>)}
                            </select>
                            {listener.tls_profile ? (
                              <label className="mt-2 flex items-center gap-2 text-xs text-enamel">
                                <input
                                  aria-label={`Require client certificate ${listener.ID}`}
                                  type="checkbox"
                                  disabled={isApplying}
                                  checked={listener.Options?.require_certificate === 'true'}
                                  onChange={(event) => updateListenerOption(listener.ID, 'require_certificate', event.target.checked ? 'true' : undefined)}
                                />
                                Require client certificate
                              </label>
                            ) : null}
                          </>
                        )}
                      </td>
                      <td className="py-3"><button type="button" aria-label={`Remove listener ${listener.ID}`} disabled={isApplying} onClick={() => removeListener(listener.ID)} className={leverDanger}>Remove</button></td>
                    </tr>
                  )
                })}
                {specs.length === 0 ? <tr><td colSpan={5} className={`py-6 ${bodyText}`}>No listeners configured.</td></tr> : null}
              </tbody>
            </table>
          )}
        </div>

        {blockingIssues.length > 0 ? (
          <Findings tone="failed" title="Issues">
            {blockingIssues.map((issue, index) => <li key={`${issue.kind}-${issue.listener_id ?? index}`}>{issue.message}</li>)}
          </Findings>
        ) : null}
        {warnings.length > 0 || Object.keys(composeConflicts).length > 0 ? (
          <Findings tone="caution" title="Warnings">
            {warnings.map((warning, index) => <li key={`${warning.kind}-${warning.listener_id ?? index}`}>{warning.message}</li>)}
            {Object.entries(composeConflicts).flatMap(([id, ports]) => ports.map((port) => <li key={`${id}-${port}`}>Listener {id} port {port} conflicts with a Compose host port</li>))}
          </Findings>
        ) : null}

        <div className="mt-5 space-y-4">
          {preview ? (
            <>
              <p className="font-mono text-sm text-enamel-bright">{preview.revision_id}</p>
              {preview.needs_restart ? (
                <label className="flex items-center gap-2 text-sm text-enamel">
                  <input type="checkbox" disabled={isApplying} checked={confirmRestart} onChange={(event) => setConfirmRestart(event.target.checked)} />
                  Confirm restart
                </label>
              ) : null}
              {canApply ? <RecentMFAConfirmation state={mfa} /> : null}
            </>
          ) : null}
          <button
            type="button"
            onClick={() => { void handleApply() }}
            disabled={!applyEnabled}
            className={leverPrimary}
          >
            Apply (restart required)
          </button>
          {preview && canApply && !mfa.proof ? <p className="text-xs text-lamp">Confirm MFA above before applying.</p> : null}
          {preview ? (
            <>
              <div>
                <h4 className="mb-2 text-sm font-semibold text-enamel-bright">Diff</h4>
                <DiffBlock content={preview.diff} />
              </div>
              <div>
                <h4 className="mb-2 text-sm font-semibold text-enamel-bright">Rendered configuration</h4>
                <pre className={`max-h-64 ${codeBlock}`}>{preview.rendered.split('\n').map((line, index) => <span key={index} className="block">{line || ' '}</span>)}</pre>
              </div>
            </>
          ) : null}
        </div>
      </Section>
    </section>
  )
}
