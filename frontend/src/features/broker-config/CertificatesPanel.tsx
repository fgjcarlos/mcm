import { useState } from 'react'
import { can, permissionTitle } from '../auth/permissions'
import { useCertificates } from './useCertificates'
import { useRecentMFA } from '../mfa/useRecentMFA'
import RecentMFAConfirmation from '../mfa/RecentMFAConfirmation'
import type { CertificateProfile, CertificateRevision } from './certificateTypes'
import { Section } from '../ui/primitives'
import { bodyText, fieldHint, fieldLabel, input, leverPrimary, leverSecondary, notice } from '../ui/styles'
import { fileInput } from './panelParts'
import { ClientPager } from '../lists/ClientPager'
import { useClientPage } from '../lists/useClientPage'

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
  hasProof,
  isBusy,
  onPreview,
  onActivate,
}: {
  profile: CertificateProfile
  canWrite: boolean
  canApply: boolean
  hasProof: boolean
  isBusy: boolean
  onPreview: (profile: string, revision: string) => void
  onActivate: (profile: string, revision: string) => void
}) {
  const revisions = profile.revisions ?? []
  const pager = useClientPage(revisions, { pageSize: 5 })
  return (
    <div className="border-t border-track pt-4 first:border-t-0 first:pt-0">
      <h4 className="font-mono text-sm font-semibold text-enamel-bright">{profile.name}</h4>
      <p className={`mt-1 ${fieldHint}`}>
        active revision: {profile.active_revision || 'none'} · expiry: {profile.expiry_status || 'unknown'}
      </p>
      {profile.description ? <p className={`mt-1 ${fieldHint}`}>{profile.description}</p> : null}
      {revisions.length === 0 ? <p className={`mt-3 ${bodyText}`}>No revisions uploaded.</p> : null}
      <div className="mt-3 space-y-3">
        {/* The noun carries the profile name: every profile has its own pager. */}
        <ClientPager pager={pager} noun={{ one: `${profile.name} revision`, many: `${profile.name} revisions` }} />
        <ul className="border-y border-track">
          {pager.rows.map(({ item: revision }) => (
            <li key={revision.revision_id} className="border-b border-track py-3 last:border-0">
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                <div className="min-w-0">
                  <p className="font-mono text-xs text-enamel-bright">{revision.revision_id}</p>
                  <p className={`mt-1 ${fieldHint}`}>
                    state: {revision.state} · {expiryLabel(revision)}
                    {revision.subject ? ` · ${revision.subject}` : ''}
                  </p>
                  {revision.fingerprint_sha256 ? (
                    <p className="mt-1 break-all font-mono text-[11px] text-enamel-muted">sha256:{revision.fingerprint_sha256}</p>
                  ) : null}
                  {revision.failure_reason ? <p className="mt-1 text-xs text-danger-text">{revision.failure_reason}</p> : null}
                </div>
                <div className="flex gap-2">
                  <button
                    type="button"
                    onClick={() => onPreview(profile.name, revision.revision_id)}
                    disabled={!canWrite || isBusy}
                    title={canWrite ? undefined : permissionTitle('certificate.write')}
                    className={leverSecondary}
                  >
                    Preview
                  </button>
                  <button
                    type="button"
                    onClick={() => onActivate(profile.name, revision.revision_id)}
                    disabled={!canApply || !hasProof || isBusy || revision.state === 'active'}
                    title={canApply ? undefined : permissionTitle('certificate.apply')}
                    className={leverSecondary}
                  >
                    Activate
                  </button>
                </div>
              </div>
            </li>
          ))}
        </ul>
      </div>
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
  const mfa = useRecentMFA(token, onLogout)
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
  } = useCertificates({ token, onLogout, onMFARejected: mfa.clear })

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
  const profilePager = useClientPage(profiles, { match: (profile, query) => profile.name.toLowerCase().includes(query) })
  // Ask for MFA only when an admin has a revision to activate.
  const hasActivatable = canApply && profiles.some((profile) => (profile.revisions ?? []).some((revision) => revision.state !== 'active'))

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
    <section className="mt-8 space-y-10">
      <Section title="Broker TLS certificates" description="Upload certificate material, activate a revision and bind it to a listener.">
        {isDisabled ? (
          <div className={`mt-5 ${notice.caution}`}>
            The certificate surface is disabled. Configure a broker config directory to manage certificates.
          </div>
        ) : null}
        {listError ? <div className={`mt-5 ${notice.failed}`}>{listError}</div> : null}

        <div className="mt-5 grid gap-4 md:grid-cols-2">
          <label className={`flex flex-col gap-2 ${fieldLabel}`}>
            Profile name
            <input
              aria-label="Profile name"
              type="text"
              value={profileName}
              disabled={!canWrite || busy}
              onChange={(event) => setProfileName(event.target.value)}
              placeholder="production"
              className={`font-mono font-normal ${input}`}
            />
          </label>
          <label className={`flex flex-col gap-2 ${fieldLabel}`}>
            Description
            <input
              aria-label="Description"
              type="text"
              value={description}
              disabled={!canWrite || busy}
              onChange={(event) => setDescription(event.target.value)}
              className={`font-normal ${input}`}
            />
          </label>
          <label className={`flex flex-col gap-2 ${fieldLabel}`}>
            CA certificate (PEM)
            <input
              aria-label="CA certificate"
              type="file"
              accept=".pem,.crt,.cer"
              disabled={!canWrite || busy}
              onChange={(event) => setCaFile(event.target.files?.[0] ?? null)}
              className={fileInput}
            />
          </label>
          <label className={`flex flex-col gap-2 ${fieldLabel}`}>
            Server certificate (PEM)
            <input
              aria-label="Server certificate"
              type="file"
              accept=".pem,.crt,.cer"
              disabled={!canWrite || busy}
              onChange={(event) => setCertFile(event.target.files?.[0] ?? null)}
              className={fileInput}
            />
          </label>
          <label className={`flex flex-col gap-2 ${fieldLabel}`}>
            Server private key (PEM)
            <input
              aria-label="Server private key"
              type="file"
              accept=".pem,.key"
              disabled={!canWrite || busy}
              onChange={(event) => setKeyFile(event.target.files?.[0] ?? null)}
              className={fileInput}
            />
          </label>
          <label className={`flex flex-col gap-2 ${fieldLabel}`}>
            Chain (PEM, optional)
            <input
              aria-label="Chain"
              type="file"
              accept=".pem,.crt,.cer"
              disabled={!canWrite || busy}
              onChange={(event) => setChainFile(event.target.files?.[0] ?? null)}
              className={fileInput}
            />
          </label>
          <label className={`flex flex-col gap-2 ${fieldLabel}`}>
            Expected names (comma separated)
            <input
              aria-label="Expected names"
              type="text"
              value={expectedNames}
              disabled={!canWrite || busy}
              onChange={(event) => setExpectedNames(event.target.value)}
              placeholder="mosquitto.example.internal"
              className={`font-mono font-normal ${input}`}
            />
          </label>
          <label className="flex items-center gap-2 text-sm text-enamel">
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

        <div className="mt-6 flex flex-wrap items-center gap-3">
          <button
            type="button"
            onClick={() => { void handleUpload() }}
            disabled={uploadDisabled}
            title={canWrite ? undefined : permissionTitle('certificate.write')}
            className={leverPrimary}
          >
            Upload revision
          </button>
          {!canWrite ? <p className={fieldHint}>{permissionTitle('certificate.write')}</p> : null}
        </div>

        {uploadError ? (
          <div className={`mt-5 ${notice.failed}`}>
            {uploadError}
            {uploadIssues.length > 0 ? (
              <ul className="mt-2 list-disc space-y-1 pl-5">
                {uploadIssues.map((issue) => <li key={issue}>{issue}</li>)}
              </ul>
            ) : null}
          </div>
        ) : null}
        {uploadedRevision ? (
          <div className={`mt-5 ${notice.neutral}`}>
            Revision {uploadedRevision.revision_id} stored. Preview it and activate it to serve it.
          </div>
        ) : null}
      </Section>

      <Section title="Profiles">
        {hasActivatable ? (
          <div className="mt-4 space-y-2">
            <RecentMFAConfirmation state={mfa} />
            {!mfa.proof ? <p className="text-xs text-lamp">Confirm MFA above before applying.</p> : null}
          </div>
        ) : null}
        {isLoading ? <p className={`mt-4 ${bodyText}`}>Loading certificate profiles…</p> : null}
        {!isLoading && profiles.length === 0 ? <p className={`mt-4 ${bodyText}`}>No certificate profiles yet.</p> : null}
        <div className="mt-4 space-y-4">
          <ClientPager pager={profilePager} noun={{ one: 'certificate profile', many: 'certificate profiles' }} searchLabel="Filter certificate profiles by name" />
          <div>
            {profilePager.rows.map(({ item: profile }) => (
              <ProfileRow
                key={profile.name}
                profile={profile}
                canWrite={canWrite}
                canApply={canApply}
                hasProof={Boolean(mfa.proof)}
                isBusy={busy}
                onPreview={(name, revision) => { void requestPreview({ profile: name, revision_id: revision }) }}
                onActivate={(name, revision) => { void applyRevision({ profile: name, revision_id: revision }, mfa.proof) }}
              />
            ))}
          </div>
        </div>

        {previewError ? <div className={`mt-5 ${notice.failed}`}>{previewError}</div> : null}
        {preview ? (
          <div className="mt-6 border-t border-track pt-4">
            <h4 className="text-sm font-semibold text-enamel-bright">Preview</h4>
            <p className="mt-2 font-mono text-sm text-enamel-bright">{preview.profile} · {preview.revision_id}</p>
            <p className={`mt-1 ${fieldHint}`}>
              current revision: {preview.current_revision || 'none'} · restart required: {preview.restart_required ? 'yes' : 'no'}
            </p>
            <ul className="mt-3 space-y-1 font-mono text-xs text-enamel-soft">
              {preview.files?.cert_path ? <li className="break-all">certificate: {preview.files.cert_path}</li> : null}
              {preview.files?.key_path ? <li className="break-all">private key: {preview.files.key_path}</li> : null}
              {preview.files?.ca_path ? <li className="break-all">authority: {preview.files.ca_path}</li> : null}
              {preview.files?.chain_path ? <li className="break-all">chain: {preview.files.chain_path}</li> : null}
            </ul>
          </div>
        ) : null}

        {applyError ? <div className={`mt-5 ${notice.failed}`}>{applyError}</div> : null}
        {applyResult ? (
          // A restored previous material is a rollback (amber); the activation itself carries no verified status here.
          <div className={`mt-5 ${applyResult.recovered ? notice.caution : notice.neutral}`}>
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
      </Section>
    </section>
  )
}
