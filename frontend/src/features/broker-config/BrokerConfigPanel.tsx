import { useEffect, useState } from 'react'
import { useBrokerConfig } from './useBrokerConfig'
import { useRecentMFA } from '../mfa/useRecentMFA'
import RecentMFAConfirmation from '../mfa/RecentMFAConfirmation'

const card = 'rounded-2xl border border-white/10 bg-slate-900/60 p-6 space-y-4'
const button = 'rounded-xl border border-white/20 px-4 py-2 text-sm text-slate-100 disabled:opacity-40'
const codeBlock = 'max-h-80 overflow-auto whitespace-pre-wrap break-all rounded-xl bg-slate-950 p-4 font-mono text-xs text-slate-200'

type Props = {
  token: string
  onLogout: () => void
  role?: string
  view?: 'configuration' | 'changes'
  onReview?: () => void
}

export default function BrokerConfigPanel({ token, onLogout, role = '', view = 'configuration', onReview }: Props) {
  const state = useBrokerConfig(token, onLogout)
  const mfa = useRecentMFA(token, onLogout)
  const [adopt, setAdopt] = useState(false)
  const [confirmRestart, setConfirmRestart] = useState(false)
  const { config, draft, preview, catalog, status } = state
  const isAdmin = role === 'admin'
  const canEdit = isAdmin || role === 'operator'
  const busy = status !== 'idle'
  const drift = Boolean(config && draft && draft.baseHash !== config.hash)
  const canApply = isAdmin && Boolean(preview && mfa.proof && adopt) && !busy && !drift && config?.capabilities?.write !== false && (!preview?.requires_restart || config?.capabilities?.restart === true)

  const { load, loadCatalog, loadHistory } = state
  useEffect(() => {
    void load()
    void loadCatalog()
    void loadHistory()
  }, [load, loadCatalog, loadHistory])

  function changed() {
    setAdopt(false)
    setConfirmRestart(false)
  }

  function apply() {
    if (!preview || !canApply) return
    if (preview.requires_restart && !confirmRestart) { setConfirmRestart(true); return }
    setConfirmRestart(false)
    void state.apply({ revision_id: preview.revision_id, force: preview.requires_restart, adopt: true }, mfa.proof)
  }

  if (state.loading && !config) return <section className="mt-8"><p>Loading broker configuration…</p></section>

  return (
    <section className="mt-8 space-y-6" aria-label="Broker configuration workspace">
      {state.error ? <p role="alert" className="rounded-xl border border-rose-300/30 bg-rose-400/10 p-4 text-rose-100">{state.error}</p> : null}
      {state.result ? <div role="status" className={card}><strong>Revision result: {state.result.status}</strong><p>{state.result.message}</p><p className="font-mono text-xs">{state.result.revision_id}</p></div> : null}
      {view === 'configuration' ? (
        <>
          <article className={card}>
            <h3 className="text-xl font-semibold">Broker configuration</h3>
            {!config ? <p>Broker config is not configured on this MCM instance.</p> : (
              <>
                <p className="break-all font-mono text-sm">{config.path}</p>
                <dl className="grid gap-3 text-sm sm:grid-cols-2">
                  <div><dt>Broker version</dt><dd>{config.broker_version || 'unknown — not observed'}</dd></div>
                  <div><dt>Catalog version</dt><dd>{config.catalog_version || catalog?.version || 'unavailable'} (not broker detection)</dd></div>
                  <div><dt>On-disk hash</dt><dd className="break-all font-mono">{config.hash}</dd></div>
                  <div><dt>Management capabilities (configured)</dt><dd>{config.capabilities ? Object.entries(config.capabilities).map(([key, enabled]) => `${key}: ${enabled ? 'yes' : 'no'}`).join(' · ') : 'Not reported'}</dd></div>
                </dl>
                {config.external_drift || drift ? <p role="status" className="text-amber-200">External drift detected. Refresh disk state and explicitly reset or reconcile the draft before generating a new preview.</p> : null}
                <button className={button} onClick={() => { void state.load() }} disabled={state.loading || busy}>Refresh disk state</button>
                <p className="text-xs text-slate-400">Refreshing never overwrites your draft. Disk state is not proof of active broker configuration.</p>
                <details><summary>On-disk configuration</summary><pre className={codeBlock}>{config.body}</pre></details>
                <details><summary>Last verified active configuration</summary>{config.last_verified ? <><p>{config.last_verified.verified_at} · {config.last_verified.revision_id}</p><pre className={codeBlock}>{config.last_verified.body}</pre></> : <p>No verified configuration activation recorded. Importing a file is not activation.</p>}</details>
                {config.include_snapshot?.files?.length ? <details><summary>Included files (read-only)</summary><ul>{config.include_snapshot.files.map((file) => <li className="break-all font-mono text-xs" key={file.path}>{file.path} · {file.sha256}</li>)}</ul></details> : null}
              </>
            )}
          </article>
          <article className={card}>
            <h3 className="text-xl font-semibold">Draft</h3>
            <label className="block text-sm">Draft configuration
              <textarea aria-label="Draft configuration" value={draft?.body ?? ''} onChange={(event) => { changed(); state.editDraft(event.target.value) }} disabled={!config || !canEdit || status === 'applying'} spellCheck={false} rows={14} className="mt-2 w-full rounded-xl border border-white/20 bg-slate-950 p-4 font-mono text-sm" />
            </label>
            <p className="text-xs text-slate-400">Advanced root-file editor. Includes stay separate; unknown directives are preserved, not silently removed. Catalog checks are not complete Mosquitto syntax validation.</p>
            <div className="flex flex-wrap gap-3">
              <button className={button} disabled={!config || !canEdit || busy || draft?.body !== config.body} onClick={() => { changed(); void state.importPreview() }}>Import / Refresh preview</button>
              <button className={button} disabled={!config || !canEdit || busy || drift} onClick={() => { changed(); void state.previewTyped() }}>Validate draft</button>
              <button className={button} disabled={!config || busy} onClick={() => { changed(); state.resetDraft() }}>Discard draft and use disk</button>
              {onReview ? <button className={button} disabled={!preview} onClick={onReview}>Review changes</button> : null}
            </div>
          </article>
        </>
      ) : null}

      {isAdmin ? <RecentMFAConfirmation state={mfa} /> : null}
      {preview ? (
        <article className={card}>
          <h3 className="text-xl font-semibold">Preview</h3>
          <p className="break-all font-mono text-xs">revision {preview.revision_id}</p>
          <p>Affected file: {config?.path}. Operational impact: <strong>{preview.reload_kind || 'reload'}</strong>.</p>
          {preview.requires_restart && !config?.capabilities?.restart ? <p className="text-amber-200">Restart required, but this adapter cannot restart the broker. Apply is unavailable; force never substitutes a reload for a restart.</p> : null}
          {preview.conf_diff ? <pre className={codeBlock}>{preview.conf_diff.split('\n').map((line, index) => <span key={index} className={`block ${line.startsWith('+') ? 'text-emerald-200' : line.startsWith('-') ? 'text-rose-200' : ''}`}>{line || ' '}</span>)}</pre> : <p>No root-file changes.</p>}
          <label className="block text-sm"><input type="checkbox" checked={adopt} disabled={!isAdmin || busy} onChange={(event) => setAdopt(event.target.checked)} /> I adopt this reviewed configuration and authorize replacing the root file.</label>
          <div className="flex gap-3">
            <button className={button} disabled={!canApply} onClick={apply}>{confirmRestart ? 'Force Apply' : 'Apply'}</button>
            {confirmRestart ? <button className={button} onClick={() => setConfirmRestart(false)}>Cancel</button> : null}
          </div>
        </article>
      ) : null}
      {(preview?.validation_issues ?? state.issues).length ? <article className={card}><h3>Validation</h3><ul>{(preview?.validation_issues ?? state.issues).map((issue, index) => <li key={index} className="text-sm">Line {issue.line} · {issue.kind}: {issue.message}</li>)}</ul></article> : null}

      {view === 'changes' ? (
        <>
          <article className={card}>
            <h3 className="text-xl font-semibold">Configuration revisions</h3>
            <button className={button} onClick={() => { void state.load(); void state.loadHistory() }} disabled={busy}>Refresh revisions</button>
            {!state.revisions.length ? <p>No configuration revisions recorded.</p> : <ul className="space-y-4">{state.revisions.map((revision) => (
              <li key={revision.id} className="space-y-2 border-b border-white/10 pb-4">
                <p className="break-all font-mono text-xs">{revision.id}</p>
                <p>{revision.actor} · {revision.created_at} · <strong>{revision.status}</strong>{revision.deployment_id ? ` · deployment ${revision.deployment_id}` : ''}</p>
                {revision.message ? <p>{revision.message}</p> : null}
                <button className={button} disabled={!isAdmin || !mfa.proof || busy} onClick={() => { changed(); void state.recover(revision.id, mfa.proof) }}>Recover revision {revision.id.slice(0, 8)}</button>
              </li>
            ))}</ul>}
          </article>
          <article className={card}>
            <h3 className="text-xl font-semibold">Configuration copies</h3>
            <p className="text-sm text-amber-100">Root configuration only — not a complete backup. Includes, credentials, ACLs, certificates, SQLite and broker data are excluded. Import prepares a preview; it does not apply automatically.</p>
            <button className={button} disabled={!isAdmin || !mfa.proof || busy} onClick={() => { void state.exportCopy(mfa.proof) }}>Export configuration copy</button>
            <label className="block text-sm">Import configuration copy<input type="file" accept="application/json,.json" disabled={!isAdmin || !mfa.proof || busy} onChange={(event) => {
              const file = event.target.files?.[0]
              if (file) { changed(); void state.importCopyFile(file, mfa.proof) }
              event.target.value = ''
            }} /></label>
          </article>
        </>
      ) : null}
      <details className={card}><summary>Directive catalog</summary><p>Catalog {catalog?.version || 'unavailable'}; not a detected broker version.</p><ul>{catalog?.directives.map((directive) => <li key={directive.name} className="text-sm"><code>{directive.name}</code> · {directive.type} · {directive.reload_kind}<p>{directive.description}</p></li>)}</ul></details>
    </section>
  )
}
