import { useEffect, useState } from 'react'
import { useBrokerConfig } from './useBrokerConfig'
import { useRecentMFA } from '../mfa/useRecentMFA'
import RecentMFAConfirmation from '../mfa/RecentMFAConfirmation'
import ChangeRoute from '../change-route/ChangeRoute'
import { deploymentLamp } from '../status/deploymentLamp'
import { DiffBlock, Fact, Lamp, Section } from '../ui/primitives'
import { bodyText, codeBlock, fieldHint, fieldLabel, leverSecondary, notice, textarea } from '../ui/styles'
import { fileInput } from './panelParts'

const summary = 'cursor-pointer text-sm font-semibold text-enamel-bright transition-colors duration-150 hover:text-enamel'

type Props = {
  token: string
  onLogout: () => void
  role?: string
  view?: 'configuration' | 'changes'
  onReview?: () => void
}

export default function BrokerConfigPanel({ token, onLogout, role = '', view = 'configuration', onReview }: Props) {
  const mfa = useRecentMFA(token, onLogout)
  const state = useBrokerConfig(token, onLogout, mfa.clear)
  const [adopt, setAdopt] = useState(false)
  const { config, draft, preview, catalog, status } = state
  const isAdmin = role === 'admin'
  const canEdit = isAdmin || role === 'operator'
  const busy = status !== 'idle'
  const drift = Boolean(config && draft && draft.baseHash !== config.hash)
  const disabledReason = !isAdmin ? 'Requires admin role to apply.'
    : busy ? 'Wait for the preview to finish.'
    : config?.capabilities?.write === false ? 'This adapter cannot write the configuration.'
      : preview?.requires_restart && config?.capabilities?.restart !== true ? 'Apply is unavailable: this adapter cannot restart the broker.'
        : drift ? 'The file on disk changed since this preview. Refresh and validate again.'
          : !adopt ? 'Tick “I adopt this reviewed configuration” to apply.' : undefined

  const { load, loadCatalog, loadHistory } = state
  useEffect(() => {
    void load()
    void loadCatalog()
    void loadHistory()
  }, [load, loadCatalog, loadHistory])

  function changed() {
    setAdopt(false)
  }

  if (state.loading && !config) return <section className="mt-8"><p className={bodyText}>Loading broker configuration…</p></section>

  return (
    <section className="mt-8 space-y-10" aria-label="Broker configuration workspace">
      {state.error ? <p role="alert" className={notice.failed}>{state.error}</p> : null}
      {state.previewConsumed ? <p className="text-sm text-lamp">Generate a new preview before applying again.</p> : null}
      {view === 'configuration' ? (
        <>
          <Section title="Broker configuration">
            {!config ? <p className={`mt-4 ${bodyText}`}>Broker config is not configured on this MCM instance.</p> : (
              <div className="mt-4 space-y-4">
                <p className="break-all font-mono text-sm text-enamel-bright">{config.path}</p>
                <dl className="grid gap-x-6 gap-y-3 border-y border-track py-3 sm:grid-cols-2">
                  <Fact label="Broker version" value={config.broker_version || 'unknown — not observed'} />
                  <Fact label="Catalog version" value={`${config.catalog_version || catalog?.version || 'unavailable'} (not broker detection)`} />
                  <Fact label="On-disk hash" value={<span className="break-all">{config.hash}</span>} />
                  <Fact label="Management capabilities (configured)" value={config.capabilities ? Object.entries(config.capabilities).map(([key, enabled]) => `${key}: ${enabled ? 'yes' : 'no'}`).join(' · ') : 'Not reported'} />
                </dl>
                {config.external_drift || drift ? <p role="status" className={notice.caution}>External drift detected. Refresh disk state and explicitly reset or reconcile the draft before generating a new preview.</p> : null}
                <div className="flex flex-wrap items-center gap-3">
                  <button className={leverSecondary} onClick={() => { void state.load() }} disabled={state.loading || busy}>Refresh disk state</button>
                  <p className={fieldHint}>Refreshing never overwrites your draft. Disk state is not proof of active broker configuration.</p>
                </div>
                <details><summary className={summary}>On-disk configuration</summary><pre className={`mt-3 max-h-80 whitespace-pre-wrap break-all ${codeBlock}`}>{config.body}</pre></details>
                <details><summary className={summary}>Last verified active configuration</summary>{config.last_verified ? <><p className="mt-3 font-mono text-xs text-enamel-muted">{config.last_verified.verified_at} · {config.last_verified.revision_id}</p><pre className={`mt-2 max-h-80 whitespace-pre-wrap break-all ${codeBlock}`}>{config.last_verified.body}</pre></> : <p className={`mt-3 ${bodyText}`}>No verified configuration activation recorded. Importing a file is not activation.</p>}</details>
                {config.include_snapshot?.files?.length ? <details><summary className={summary}>Included files (read-only)</summary><ul className="mt-3 space-y-1">{config.include_snapshot.files.map((file) => <li className="break-all font-mono text-xs text-enamel-soft" key={file.path}>{file.path} · {file.sha256}</li>)}</ul></details> : null}
              </div>
            )}
          </Section>
          <Section title="Draft">
            <label className="mt-4 block">
              <span className={fieldLabel}>Draft configuration</span>
              <textarea aria-label="Draft configuration" value={draft?.body ?? ''} onChange={(event) => { changed(); state.editDraft(event.target.value) }} disabled={!config || !canEdit || status === 'applying'} spellCheck={false} rows={14} className={`mt-2 block w-full ${textarea}`} />
            </label>
            <p className={`mt-2 ${fieldHint}`}>Advanced root-file editor. Includes stay separate; unknown directives are preserved, not silently removed. Catalog checks are not complete Mosquitto syntax validation.</p>
            <div className="mt-4 flex flex-wrap gap-3">
              <button className={leverSecondary} disabled={!config || !canEdit || busy || draft?.body !== config.body} onClick={() => { changed(); void state.importPreview() }}>Import / Refresh preview</button>
              <button className={leverSecondary} disabled={!config || !canEdit || busy || drift} onClick={() => { changed(); void state.previewTyped() }}>Validate draft</button>
              <button className={leverSecondary} disabled={!config || busy} onClick={() => { changed(); state.resetDraft() }}>Discard draft and use disk</button>
              {onReview ? <button className={leverSecondary} disabled={!preview} onClick={onReview}>Review changes</button> : null}
            </div>
          </Section>
        </>
      ) : null}

      {/* The apply asks for MFA in its route; recovery and copies ask here. */}
      {isAdmin && view === 'changes' && !preview ? <RecentMFAConfirmation state={mfa} /> : null}
      <div>
        <ChangeRoute hasChanges={Boolean(preview)} reviewKey={preview?.revision_id} mfa={mfa} applyLabel={preview?.requires_restart ? 'Apply and restart broker' : 'Apply'} disabledReason={disabledReason}
          onApply={(proof) => preview ? state.apply({ revision_id: preview.revision_id, force: preview.requires_restart, adopt: true }, proof) : Promise.resolve(null)}>
          {preview ? <div className="space-y-4">
          <h3 className="text-lg font-semibold text-enamel-bright">Preview</h3>
          <p className="break-all font-mono text-xs text-enamel-muted">revision {preview.revision_id}</p>
          <p className={bodyText}>Affected file: {config?.path}. Operational impact: <strong className="font-semibold text-enamel-bright">{preview.reload_kind || 'reload'}</strong>.</p>
          {preview.requires_restart && !config?.capabilities?.restart ? <p className="text-sm text-lamp">Restart required, but this adapter cannot restart the broker. Apply is unavailable; force never substitutes a reload for a restart.</p> : null}
          {preview.conf_diff ? <DiffBlock content={preview.conf_diff} /> : <p className={bodyText}>No root-file changes.</p>}
          <label className="flex items-start gap-2 text-sm text-enamel"><input type="checkbox" className="mt-1" checked={adopt} disabled={!isAdmin || busy} onChange={(event) => setAdopt(event.target.checked)} /> I adopt this reviewed configuration and authorize replacing the root file.</label>
          </div> : null}
        </ChangeRoute>
      </div>
      {(preview?.validation_issues ?? state.issues).length ? <Section title="Validation"><ul className="mt-3 space-y-1">{(preview?.validation_issues ?? state.issues).map((issue, index) => <li key={index} className="text-sm text-enamel">Line {issue.line} · {issue.kind}: {issue.message}</li>)}</ul></Section> : null}

      {view === 'changes' ? (
        <>
          <Section title="Configuration revisions" aside={<button className={leverSecondary} onClick={() => { void state.load(); void state.loadHistory() }} disabled={busy}>Refresh revisions</button>}>
            {!state.revisions.length ? <p className={`mt-4 ${bodyText}`}>No configuration revisions recorded.</p> : <ul className="mt-4 border-y border-track">{state.revisions.map((revision) => (
              <li key={revision.id} className="space-y-2 border-b border-track py-4 last:border-0">
                <p className="break-all font-mono text-xs text-enamel-muted">{revision.id}</p>
                <p className={bodyText}>{revision.actor} · {revision.created_at} · <Lamp tone={deploymentLamp(revision.status)}>{revision.status}</Lamp>{revision.deployment_id ? ` · deployment ${revision.deployment_id}` : ''}</p>
                {revision.message ? <p className={bodyText}>{revision.message}</p> : null}
                <button className={leverSecondary} disabled={!isAdmin || !mfa.proof || busy} onClick={() => { changed(); void state.recover(revision.id, mfa.proof) }}>Recover revision {revision.id.slice(0, 8)}</button>
              </li>
            ))}</ul>}
          </Section>
          <Section title="Configuration copies">
            <p className={`mt-4 ${notice.caution}`}>Root configuration only — not a complete backup. Includes, credentials, ACLs, certificates, SQLite and broker data are excluded. Import prepares a preview; it does not apply automatically.</p>
            <div className="mt-4 flex flex-wrap items-end gap-6">
              <button className={leverSecondary} disabled={!isAdmin || !mfa.proof || busy} onClick={() => { void state.exportCopy(mfa.proof) }}>Export configuration copy</button>
              <label className={`flex flex-col gap-2 ${fieldLabel}`}>Import configuration copy<input type="file" accept="application/json,.json" className={fileInput} disabled={!isAdmin || !mfa.proof || busy} onChange={(event) => {
                const file = event.target.files?.[0]
                if (file) { changed(); void state.importCopyFile(file, mfa.proof) }
                event.target.value = ''
              }} /></label>
            </div>
          </Section>
        </>
      ) : null}
      <details className="border-t border-track pt-6"><summary className={summary}>Directive catalog</summary><p className={`mt-3 ${bodyText}`}>Catalog {catalog?.version || 'unavailable'}; not a detected broker version.</p><ul className="mt-3 space-y-3">{catalog?.directives.map((directive) => <li key={directive.name} className="text-sm text-enamel"><code className="font-mono text-enamel-bright">{directive.name}</code> · {directive.type} · {directive.reload_kind}<p className={fieldHint}>{directive.description}</p></li>)}</ul></details>
    </section>
  )
}
