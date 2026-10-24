import { useState } from 'react'
import { can, permissionTitle } from '../auth/permissions'
import { useCertificates } from './useCertificates'
import type { CertificateProfile, CertificateRevision } from './certificateTypes'

function readFileAsText(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(typeof reader.result === 'string' ? reader.result : '')
    reader.onerror = () => reject(new Error(`The file ${file.name} could not be read.`))
    reader.readAsText(file)
  })
}

function expiryLabel(revision: CertificateRevision): string {
  if (revision.expiry_status) return revision.expiry_status
  return revision.not_after ? `until ${revision.not_after.slice(0, 10)}` : 'unknown expiry'
}

function ProfileRow({
  profile,
  canWrite,
  canApply,
  isBusy,
  onPreview,
  onActivate,
}: {
  profile: CertificateProfile
  canWrite: boolean
  canApply: boolean
  isBusy: boolean
  onPreview: (profile: string, revision: string) => void
  onActivate: (profile: string, revision: string) => void
}) {
  const revisions = profile.revisions ?? []
  return (
    <div className="rounded-2xl border border-white/10 bg-slate-950/40 p-4">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <p className="font-mono text-sm text-cyan-100">{profile.name}</p>
          <p className="text-xs text-slate-400">
            active revision: {profile.active_revision || 'none'} · expiry: {profile.expiry_status || 'unknown'}
          </p>
          {profile.description ? <p className="mt-1 text-xs text-slate-400">{profile.description}</p> : null}
        </div>
      </div>
      {revisions.length === 0 ? <p className="mt-3 text-sm text-slate-400">No revisions uploaded.</p> : null}
      <ul className="mt-3 space-y-2">
        {revisions.map((revision) => (
          <li key={revision.revision_id} className="rounded-xl border border-white/5 bg-slate-900/60 p-3">
            <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <p className="font-mono text-xs text-slate-200">{revision.revision_id}</p>
                <p className="mt-1 text-xs text-slate-400">
                  state: {revision.state} · {expiryLabel(revision)}
                  {revision.subject ? ` · ${revision.subject}` : ''}
                </p>
                {revision.fingerprint_sha256 ? (
                  <p className="mt-1 break-all font-mono text-[11px] text-slate-500">sha256:{revision.fingerprint_sha256}</p>
                ) : null}
                {revision.failure_reason ? <p className="mt-1 text-xs text-rose-200">{revision.failure_reason}</p> : null}
              </div>
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={() => onPreview(profile.name, revision.revision_id)}
                  disabled={!canWrite || isBusy}
                  title={canWrite ? undefined : permissionTitle('certificate.write')}
                  className="rounded-xl border border-white/10 px-3 py-1.5 text-xs font-semibold text-slate-200 disabled:opacity-50"
                >
                  Preview
                </button>
                <button
                  type="button"
                  onClick={() => onActivate(profile.name, revision.revision_id)}
                  disabled={!canApply || isBusy || revision.state === 'active'}
                  title={canApply ? undefined : permissionTitle('certificate.apply')}
                  className="rounded-xl bg-cyan-500 px-3 py-1.5 text-xs font-semibold text-white disabled:opacity-50"
                >
                  Activate
                </button>
              </div>
            </div>
          </li>
        ))}
      </ul>
    </div>
  )
}

export default function CertificatesPanel({
  token,
  onLogout,
  role,
}: {
  token: string
  onLogout: () => void
  role: string
}) {
  const {
    profiles,
    listError,
    isLoading,
    isDisabled,
    uploadError,
    uploadIssues,
    uploadedRevision,
    isUploading,
    preview,
    previewError,
    isPreviewing,
    applyResult,
    applyError,
    isApplying,
    uploadRevision,
    requestPreview,
    applyRevision,
  } = useCertificates({ token, onLogout })

  const [profileName, setProfileName] = useState('')
  const [description, setDescription] = useState('')
  const [expectedNames, setExpectedNames] = useState('')
  const [requireCa, setRequireCa] = useState(false)
  const [caFile, setCaFile] = useState<File | null>(null)
  const [certFile, setCertFile] = useState<File | null>(null)
  const [keyFile, setKeyFile] = useState<File | null>(null)
  const [chainFile, setChainFile] = useState<File | null>(null)

  const canWrite = can(role, 'certificate.write')
  const canApply = can(role, 'certificate.apply')
  const busy = isUploading || isPreviewing || isApplying

  const handleUpload = async () => {
    if (!profileName.trim()) {
      return
    }
    if (!certFile || !keyFile) {
      return
    }
    // The key material is read at submit time only: it never lands in the
    // component state, so it cannot leak into a re-render or a devtools
    // inspection of the form.
    const request = {
      ca_cert_pem: caFile ? await readFileAsText(caFile) : '',
      server_cert_pem: await readFileAsText(certFile),
      server_key_pem: await readFileAsText(keyFile),
      chain_pem: chainFile ? await readFileAsText(chainFile) : '',
      expected_names: expectedNames.split(',').map((value) => value.trim()).filter(Boolean),
      description: description.trim(),
      require_ca: requireCa,
    }
    await uploadRevision(profileName.trim(), request)
  }

  const uploadDisabled = busy || !canWrite || !profileName.trim() || !certFile || !keyFile

  return (
    <section className="mt-8 space-y-6">
      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Broker TLS certificates</p>
            <p className="mt-1 text-sm text-slate-300">
              Upload certificate material, activate a revision and bind it to a listener.
            </p>
          </div>
        </div>

        {isDisabled ? (
          <div className="mt-5 rounded-2xl border border-dashed border-amber-300/30 bg-amber-400/10 p-5 text-sm text-amber-100">
            The certificate surface is disabled. Configure a broker config directory to manage certificates.
          </div>
        ) : null}
        {listError ? <div className="mt-5 rounded-2xl border border-dashed border-rose-300/30 bg-rose-400/10 p-5 text-sm text-rose-100">{listError}</div> : null}

        <div className="mt-5 grid gap-4 md:grid-cols-2">
          <label className="flex flex-col gap-1 text-xs uppercase tracking-[0.18em] text-slate-400">
            Profile name
            <input
              aria-label="Profile name"
              type="text"
              value={profileName}
              disabled={!canWrite || busy}
              onChange={(event) => setProfileName(event.target.value)}
              placeholder="production"
              className="rounded bg-slate-800 px-3 py-2 font-mono text-sm normal-case text-slate-100"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs uppercase tracking-[0.18em] text-slate-400">
            Description
            <input
              aria-label="Description"
              type="text"
              value={description}
              disabled={!canWrite || busy}
              onChange={(event) => setDescription(event.target.value)}
              className="rounded bg-slate-800 px-3 py-2 text-sm normal-case text-slate-100"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs uppercase tracking-[0.18em] text-slate-400">
            CA certificate (PEM)
            <input
              aria-label="CA certificate"
              type="file"
              accept=".pem,.crt,.cer"
              disabled={!canWrite || busy}
              onChange={(event) => setCaFile(event.target.files?.[0] ?? null)}
              className="text-xs normal-case text-slate-300 file:mr-3 file:rounded file:border-0 file:bg-slate-700 file:px-3 file:py-1 file:text-slate-100"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs uppercase tracking-[0.18em] text-slate-400">
            Server certificate (PEM)
            <input
              aria-label="Server certificate"
              type="file"
              accept=".pem,.crt,.cer"
              disabled={!canWrite || busy}
              onChange={(event) => setCertFile(event.target.files?.[0] ?? null)}
              className="text-xs normal-case text-slate-300 file:mr-3 file:rounded file:border-0 file:bg-slate-700 file:px-3 file:py-1 file:text-slate-100"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs uppercase tracking-[0.18em] text-slate-400">
            Server private key (PEM)
            <input
              aria-label="Server private key"
              type="file"
              accept=".pem,.key"
              disabled={!canWrite || busy}
              onChange={(event) => setKeyFile(event.target.files?.[0] ?? null)}
              className="text-xs normal-case text-slate-300 file:mr-3 file:rounded file:border-0 file:bg-slate-700 file:px-3 file:py-1 file:text-slate-100"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs uppercase tracking-[0.18em] text-slate-400">
            Chain (PEM, optional)
            <input
              aria-label="Chain"
              type="file"
              accept=".pem,.crt,.cer"
              disabled={!canWrite || busy}
              onChange={(event) => setChainFile(event.target.files?.[0] ?? null)}
              className="text-xs normal-case text-slate-300 file:mr-3 file:rounded file:border-0 file:bg-slate-700 file:px-3 file:py-1 file:text-slate-100"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs uppercase tracking-[0.18em] text-slate-400">
            Expected names (comma separated)
            <input
              aria-label="Expected names"
              type="text"
              value={expectedNames}
              disabled={!canWrite || busy}
              onChange={(event) => setExpectedNames(event.target.value)}
              placeholder="mosquitto.example.internal"
              className="rounded bg-slate-800 px-3 py-2 font-mono text-sm normal-case text-slate-100"
            />
          </label>
          <label className="flex items-center gap-2 text-sm text-slate-300">
            <input
              aria-label="Require client certificate"
              type="checkbox"
              checked={requireCa}
              disabled={!canWrite || busy}
              onChange={(event) => setRequireCa(event.target.checked)}
            />
            Require a client certificate (mutual TLS)
          </label>
        </div>

        <div className="mt-5 flex items-center gap-3">
          <button
            type="button"
            onClick={() => { void handleUpload() }}
            disabled={uploadDisabled}
            title={canWrite ? undefined : permissionTitle('certificate.write')}
            className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
          >
            Upload revision
          </button>
          {!canWrite ? <p className="text-xs text-slate-400">{permissionTitle('certificate.write')}</p> : null}
        </div>

        {uploadError ? (
          <div className="mt-5 rounded-2xl border border-dashed border-rose-300/30 bg-rose-400/10 p-5 text-sm text-rose-100">
            {uploadError}
            {uploadIssues.length > 0 ? (
              <ul className="mt-2 list-disc space-y-1 pl-5">
                {uploadIssues.map((issue) => <li key={issue}>{issue}</li>)}
              </ul>
            ) : null}
          </div>
        ) : null}
        {uploadedRevision ? (
          <div className="mt-5 rounded-2xl border border-emerald-300/30 bg-emerald-400/10 p-5 text-sm text-emerald-100">
            Revision {uploadedRevision.revision_id} stored. Preview it and activate it to serve it.
          </div>
        ) : null}
      </div>

      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Profiles</p>
        {isLoading ? <p className="mt-4 text-sm text-slate-400">Loading certificate profiles…</p> : null}
        {!isLoading && profiles.length === 0 ? <p className="mt-4 text-sm text-slate-400">No certificate profiles yet.</p> : null}
        <div className="mt-4 space-y-4">
          {profiles.map((profile) => (
            <ProfileRow
              key={profile.name}
              profile={profile}
              canWrite={canWrite}
              canApply={canApply}
              isBusy={busy}
              onPreview={(name, revision) => { void requestPreview({ profile: name, revision_id: revision }) }}
              onActivate={(name, revision) => { void applyRevision({ profile: name, revision_id: revision }) }}
            />
          ))}
        </div>

        {previewError ? <div className="mt-5 rounded-2xl border border-dashed border-rose-300/30 bg-rose-400/10 p-5 text-sm text-rose-100">{previewError}</div> : null}
        {preview ? (
          <div className="mt-5 rounded-2xl border border-white/10 bg-slate-950/40 p-5">
            <p className="text-xs font-semibold uppercase tracking-[0.18em] text-cyan-300">Preview</p>
            <p className="mt-2 font-mono text-sm text-slate-200">{preview.profile} · {preview.revision_id}</p>
            <p className="mt-1 text-xs text-slate-400">
              current revision: {preview.current_revision || 'none'} · restart required: {preview.restart_required ? 'yes' : 'no'}
            </p>
            <ul className="mt-3 space-y-1 text-xs text-slate-400">
              {preview.files?.cert_path ? <li className="break-all">certificate: {preview.files.cert_path}</li> : null}
              {preview.files?.key_path ? <li className="break-all">private key: {preview.files.key_path}</li> : null}
              {preview.files?.ca_path ? <li className="break-all">authority: {preview.files.ca_path}</li> : null}
              {preview.files?.chain_path ? <li className="break-all">chain: {preview.files.chain_path}</li> : null}
            </ul>
          </div>
        ) : null}

        {applyError ? <div className="mt-5 rounded-2xl border border-dashed border-rose-300/30 bg-rose-400/10 p-5 text-sm text-rose-100">{applyError}</div> : null}
        {applyResult ? (
          <div className="mt-5 rounded-2xl border border-emerald-300/30 bg-emerald-400/10 p-5 text-sm text-emerald-100">
            <p>Revision {applyResult.revision_id} activated. Previous revision: {applyResult.previous_revision || 'none'}.</p>
            {applyResult.recovered ? <p className="mt-1">The previous material was restored: {applyResult.recovery_note}</p> : null}
            {(applyResult.verification ?? []).length > 0 ? (
              <ul className="mt-2 list-disc space-y-1 pl-5">
                {(applyResult.verification ?? []).map((verification, index) => (
                  <li key={`${verification.listener_id ?? 'listener'}-${index}`}>
                    {verification.listener_id ?? 'listener'} port {verification.port}: {verification.status}
                    {verification.reason ? ` — ${verification.reason}` : ''}
                  </li>
                ))}
              </ul>
            ) : null}
          </div>
        ) : null}
      </div>
    </section>
  )
}
