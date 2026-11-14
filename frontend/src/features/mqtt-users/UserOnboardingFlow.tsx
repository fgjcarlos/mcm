import { useState, type FormEvent } from 'react'
import { authenticatedFetch, isForbiddenResponseError, isUnauthorizedResponseError } from '../api/client'
import { can, permissionTitle } from '../auth/permissions'
import { useRecentMFA } from '../mfa/useRecentMFA'
import RecentMFAConfirmation from '../mfa/RecentMFAConfirmation'
import type { ACLPermission, ACLScope } from '../acl/types'
import ConnectionTestStep from './ConnectionTestStep'

// The integrated onboarding journey of issue #341: the MQTT user, its
// access rule, the preview and the apply in one place.
//
// The journey adds no guarantee of its own. It chains the endpoints MCM
// already exposes, and it keeps every gate the backend owns: the apply
// still demands the admin role and a recent MFA proof, and the activation
// verification with its rollback still happens in the deploy service
// (#293/#305/#339). The journey only shows the operator, step by step,
// what the saved-but-not-applied state really means.

type Deployment = {
  id: number | string
  status: string
  message?: string
  created_at: string
}

type DeployPreview = {
  revision_id: string
  acl_diff: string
  passwd_diff: string
  has_changes: boolean
  orphan_rules?: { id: string; principal: string; topic_filter: string; permission: string }[]
  at_risk_entries?: string[]
  preserved_entries?: string[]
  composition_warnings?: string[]
  scope_issues?: string[]
}

type Stage = 'user' | 'rule' | 'preview' | 'apply' | 'test'

type UserOnboardingFlowProps = {
  token: string
  onLogout: () => void
  role?: string
  // The journey starts at the user step, or at the rule step for a user
  // that already exists.
  initialStage?: Stage
  initialUsername?: string
  onMutationStart?: () => number
  onMutation?: (mutationID: number) => void
  onApplyStart?: () => number
  onApplySuccess?: (mutationWatermark: number) => void
  onUserCreated?: (username: string) => void
  onClose?: () => void
}

const STEPS: { id: Stage; label: string }[] = [
  { id: 'user', label: 'User' },
  { id: 'rule', label: 'Access rule' },
  { id: 'preview', label: 'Preview' },
  { id: 'apply', label: 'Apply' },
  { id: 'test', label: 'Test connection' },
]

// A deployment that left the configuration on disk. The activation
// verification may still be pending for it.
const APPLIED_STATUSES = ['active_verified', 'applied', 'pending_activation']

function isVerified(status: string) {
  return status === 'active_verified' || status === 'applied'
}

// What each non-verified status honestly means for the broker configuration,
// and the stage it belongs to. `failed` does not say the previous
// configuration was restored: the applier may or may not have restored it.
const NON_VERIFIED_STATUS: Record<string, { stage: string; explanation: string }> = {
  pending_activation: {
    stage: 'Stage: activation verification (pending).',
    explanation: 'The configuration was written to the broker, but the activation was not proven yet.',
  },
  rolled_back: {
    stage: 'Failed stage: activation verification (apply).',
    explanation: 'MCM restored the previous configuration; nothing of this journey is live on the broker.',
  },
  rollback_failed: {
    stage: 'Failed stage: activation verification (apply).',
    explanation: 'The restore of the previous configuration failed; the broker may be running a partial configuration and an operator has to intervene.',
  },
  failed: {
    stage: 'Failed stage: apply (the broker files and the reload).',
    explanation: 'The apply failed. MCM does not confirm the state of the broker configuration; check the deployment detail before retrying.',
  },
}

export default function UserOnboardingFlow({
  token,
  onLogout,
  role = '',
  initialStage = 'user',
  initialUsername = '',
  onMutationStart,
  onMutation,
  onApplyStart,
  onApplySuccess,
  onUserCreated,
  onClose = () => {},
}: UserOnboardingFlowProps) {
  const canWriteUser = can(role, 'mqttUser.write')
  const canWriteRule = can(role, 'acl.write')
  const canPreview = can(role, 'deploy.preview')
  const canApply = can(role, 'deploy.apply')

  const mfa = useRecentMFA(token, onLogout)

  const [stage, setStage] = useState<Stage>(initialStage)
  const [username, setUsername] = useState(initialUsername)
  const [createdPassword, setCreatedPassword] = useState('')
  const [passwordCopied, setPasswordCopied] = useState(false)
  const [passwordCopyFailed, setPasswordCopyFailed] = useState(false)
  const [creatingUser, setCreatingUser] = useState(false)
  const [userError, setUserError] = useState('')

  const [topicFilter, setTopicFilter] = useState('')
  const [permission, setPermission] = useState<ACLPermission>('readwrite')
  const [scope, setScope] = useState<ACLScope>('global')
  const [listenerID, setListenerID] = useState('')
  const [description, setDescription] = useState('')
  const [savingRule, setSavingRule] = useState(false)
  const [ruleError, setRuleError] = useState('')

  const [preview, setPreview] = useState<DeployPreview | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewError, setPreviewError] = useState('')

  const [applying, setApplying] = useState(false)
  const [applyError, setApplyError] = useState('')
  const [result, setResult] = useState<Deployment | null>(null)

  const handleCreateUser = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (creatingUser) return
    setCreatingUser(true)
    setUserError('')
    const mutationID = onMutationStart?.() ?? 0
    try {
      const response = await authenticatedFetch('/api/v1/mqtt-users', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: username.trim() }),
      })
      if (!response.ok) {
        const errBody = (await response.json().catch(() => null)) as { error?: string } | null
        setUserError(errBody?.error ?? 'Create failed.')
        return
      }
      const created = (await response.json()) as { id: number; username: string; password: string }
      setUsername(created.username)
      setCreatedPassword(created.password)
      setPasswordCopied(false)
      setPasswordCopyFailed(false)
      onMutation?.(mutationID)
      onUserCreated?.(created.username)
      setStage('rule')
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) { setUserError((err as Error).message); return }
      setUserError('Could not reach the server.')
    } finally {
      setCreatingUser(false)
    }
  }

  const handleSaveRule = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (savingRule) return
    setSavingRule(true)
    setRuleError('')
    const mutationID = onMutationStart?.() ?? 0
    try {
      const response = await authenticatedFetch('/api/v1/acls', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          principal: username.trim(),
          topic_filter: topicFilter.trim(),
          permission,
          selector: 'user',
          scope,
          listener_id: scope === 'listener' ? listenerID.trim() : '',
          description: description.trim(),
        }),
      })
      if (!response.ok) {
        const errBody = (await response.json().catch(() => null)) as { error?: string; details?: string[] } | null
        const details = errBody?.details?.join('; ')
        setRuleError(details ?? errBody?.error ?? 'The access rule could not be saved.')
        return
      }
      onMutation?.(mutationID)
      setStage('preview')
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) { setRuleError((err as Error).message); return }
      setRuleError('Could not reach the server.')
    } finally {
      setSavingRule(false)
    }
  }

  const handlePreview = async () => {
    setPreviewLoading(true)
    setPreviewError('')
    setPreview(null)
    try {
      const response = await authenticatedFetch('/api/v1/deployments/preview', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
      })
      if (!response.ok) {
        const errBody = (await response.json().catch(() => null)) as { error?: string } | null
        setPreviewError(errBody?.error ?? 'Preview failed.')
        return
      }
      const data = (await response.json()) as DeployPreview
      setPreview(data)
      // A new revision replaces a consumed one: the previous apply outcome no longer applies.
      setApplyError('')
      setResult(null)
      setStage('apply')
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) { setPreviewError((err as Error).message); return }
      setPreviewError('Could not reach the server.')
    } finally {
      setPreviewLoading(false)
    }
  }

  const handleApply = async () => {
    if (!mfa.proof) { setApplyError('Confirm recent MFA before applying.'); return }
    if (!preview?.revision_id) {
      setApplyError('Preview revision is missing. Generate a new preview before applying.')
      return
    }
    const mutationWatermark = onApplyStart?.() ?? 0
    setApplying(true)
    setApplyError('')
    setResult(null)
    try {
      const response = await authenticatedFetch('/api/v1/deployments/apply', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-MCM-MFA': mfa.proof },
        body: JSON.stringify({ revision_id: preview.revision_id }),
      })
      if (!response.ok) {
        const errBody = (await response.json().catch(() => null)) as { error?: string; details?: string[] } | null
        const details = errBody?.details?.join('; ')
        setApplyError(details ?? errBody?.error ?? 'Apply failed.')
        return
      }
      const deployment = (await response.json()) as Deployment
      setResult(deployment)
      if (isVerified(deployment.status)) {
        onApplySuccess?.(mutationWatermark)
      }
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) { setApplyError((err as Error).message); return }
      setApplyError('Could not reach the server.')
    } finally {
      setApplying(false)
    }
  }

  const stepIndex = STEPS.findIndex((step) => step.id === stage)

  return (
    <section aria-label="MQTT user onboarding" className="rounded-2xl border border-cyan-300/25 bg-cyan-400/[0.04] p-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">MQTT user onboarding</p>
          <p className="mt-1 text-sm text-slate-300">
            User, access rule, preview, apply and a test with your own MQTT client in one journey. The apply still needs a recent MFA confirmation and
            verifies the activation against the live broker.
          </p>
        </div>
        <button
          type="button"
          onClick={onClose}
          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white"
        >
          Close journey
        </button>
      </div>

      <ol className="mt-5 flex flex-wrap items-center gap-2 text-xs">
        {STEPS.map((step, index) => {
          const state = index < stepIndex ? 'done' : index === stepIndex ? 'current' : 'todo'
          return (
            <li key={step.id} className="flex items-center gap-2">
              <span
                aria-current={state === 'current' ? 'step' : undefined}
                className={`rounded-full px-3 py-1 font-semibold uppercase tracking-[0.18em] ${
                  state === 'done'
                    ? 'bg-emerald-400/10 text-emerald-200'
                    : state === 'current'
                      ? 'bg-cyan-400/15 text-cyan-100'
                      : 'bg-white/5 text-slate-400'
                }`}
              >
                {index + 1}. {step.label}
              </span>
              {index < STEPS.length - 1 ? <span className="text-slate-600">→</span> : null}
            </li>
          )
        })}
      </ol>

      {createdPassword ? (
        <div className="mt-5 rounded-2xl border border-emerald-300/30 bg-emerald-400/10 p-5">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-emerald-200">User created — save this password</p>
          <p className="mt-2 text-sm text-slate-300">This password will not be shown again.</p>
          <pre className="mt-3 rounded-xl bg-slate-950/60 px-4 py-3 font-mono text-sm text-emerald-100">{createdPassword}</pre>
          <div className="mt-3 flex gap-2">
            <button
              type="button"
              onClick={() => {
                const copyFailed = () => { setPasswordCopied(false); setPasswordCopyFailed(true) }
                if (typeof navigator.clipboard?.writeText !== 'function') {
                  copyFailed()
                  return
                }
                void navigator.clipboard
                  .writeText(createdPassword)
                  .then(() => { setPasswordCopyFailed(false); setPasswordCopied(true) })
                  .catch(copyFailed)
              }}
              className="rounded-xl border border-emerald-300/30 px-3 py-1.5 text-xs text-emerald-100 transition hover:border-emerald-300/60"
            >
              {passwordCopied ? 'Copied' : 'Copy password'}
            </button>
            <button
              type="button"
              onClick={() => { setCreatedPassword(''); setPasswordCopied(false); setPasswordCopyFailed(false) }}
              className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white"
            >
              Dismiss
            </button>
          </div>
          {passwordCopyFailed ? (
            <p role="status" className="mt-2 text-xs text-amber-200">
              Copy failed: select the password and copy it by hand.
            </p>
          ) : null}
        </div>
      ) : null}

      {stage === 'user' ? (
        <form onSubmit={handleCreateUser} className="mt-6 space-y-4">
          <label className="block">
            <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Username</span>
            <input
              type="text"
              required
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="device-sensor-01"
              className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
            />
            <span className="mt-1 block text-xs text-slate-400">A secure password will be generated automatically.</span>
          </label>
          {userError ? (
            <div role="alert" className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">
              <p className="font-semibold">User creation failed</p>
              <p className="mt-1">{userError}</p>
            </div>
          ) : null}
          <div className="flex gap-3">
            <button
              type="submit"
              disabled={creatingUser || !canWriteUser}
              title={!canWriteUser ? permissionTitle('mqttUser.write') : undefined}
              className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
            >
              {creatingUser ? 'Creating…' : 'Create user'}
            </button>
            <button
              type="button"
              onClick={onClose}
              className="rounded-xl border border-white/10 px-4 py-2 text-sm text-slate-300 transition hover:border-white/20 hover:text-white"
            >
              Cancel
            </button>
          </div>
        </form>
      ) : null}

      {stage === 'rule' ? (
        <form onSubmit={handleSaveRule} className="mt-6 space-y-4">
          <p className="text-sm text-slate-300">
            The access rule for <span className="font-mono text-cyan-100">{username}</span>. It is a normal MCM rule: the
            ACL panel lists it and the apply renders it into the Mosquitto ACL file.
          </p>
          <label className="block">
            <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Topic filter</span>
            <input
              type="text"
              required
              value={topicFilter}
              onChange={(e) => setTopicFilter(e.target.value)}
              placeholder="sensors/# or device/+/status"
              className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
            />
            <span className="mt-1 block text-xs text-slate-400">MQTT wildcards: + (single level), # (multi-level, must be last)</span>
          </label>
          <label className="block">
            <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Permission</span>
            <select
              aria-label="Permission"
              value={permission}
              onChange={(e) => setPermission(e.target.value as ACLPermission)}
              className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
            >
              <option value="read">read</option>
              <option value="write">write</option>
              <option value="readwrite">readwrite</option>
              <option value="deny">deny</option>
            </select>
            <span className="mt-1 block text-xs text-slate-400">
              A readwrite grant on this identity is what lets the activation verification prove the new user against the
              live broker. A narrower grant is still applied, but the activation is then verified through the labelled
              managed service account.
            </span>
          </label>
          <label className="block">
            <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">ACL file</span>
            <select
              value={scope}
              onChange={(e) => setScope(e.target.value as ACLScope)}
              className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
            >
              <option value="global">Broker-wide &mdash; the rule applies to every listener</option>
              <option value="listener">One listener &mdash; the rule applies to that listener only</option>
            </select>
          </label>
          {scope === 'listener' ? (
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Listener</span>
              <input
                type="text"
                required
                value={listenerID}
                onChange={(e) => setListenerID(e.target.value)}
                placeholder="tls"
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              />
            </label>
          ) : null}
          <label className="block">
            <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Description (optional)</span>
            <input
              type="text"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Short description for audit purposes"
              className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
            />
          </label>
          {ruleError ? (
            <div role="alert" className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">
              <p className="font-semibold">Access rule step failed</p>
              <p className="mt-1">{ruleError}</p>
            </div>
          ) : null}
          <div className="flex gap-3">
            <button
              type="submit"
              disabled={savingRule || !canWriteRule}
              title={!canWriteRule ? permissionTitle('acl.write') : undefined}
              className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
            >
              {savingRule ? 'Saving…' : 'Save access rule'}
            </button>
            <button
              type="button"
              onClick={onClose}
              className="rounded-xl border border-white/10 px-4 py-2 text-sm text-slate-300 transition hover:border-white/20 hover:text-white"
            >
              Cancel
            </button>
          </div>
        </form>
      ) : null}

      {stage === 'preview' ? (
        <div className="mt-6 space-y-4">
          <p className="text-sm text-slate-300">
            The preview shows the exact ACL and password file changes this journey would apply. Nothing is written to the
            broker until the apply.
          </p>
          {previewError ? (
            <div role="alert" className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">
              <p className="font-semibold">Preview step failed</p>
              <p className="mt-1">{previewError}</p>
            </div>
          ) : null}
          <button
            type="button"
            onClick={() => { void handlePreview() }}
            disabled={previewLoading || !canPreview}
            title={!canPreview ? permissionTitle('deploy.preview') : undefined}
            className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
          >
            {previewLoading ? 'Generating…' : 'Preview changes'}
          </button>
        </div>
      ) : null}

      {stage === 'apply' ? (
        <div className="mt-6 space-y-5">
          {preview && !preview.has_changes ? (
            <div className="rounded-2xl border border-dashed border-white/10 bg-white/[0.03] p-5 text-sm text-slate-300">
              No pending changes — the configuration is already up to date.
            </div>
          ) : null}

          {preview?.acl_diff ? (
            <div>
              <p className="mb-2 text-xs font-semibold uppercase tracking-[0.18em] text-cyan-300">ACL diff</p>
              <pre className="max-h-48 overflow-auto rounded-xl border border-white/10 bg-slate-950/70 p-4 text-xs text-slate-100">{preview.acl_diff}</pre>
            </div>
          ) : null}

          {preview?.passwd_diff ? (
            <div>
              <p className="mb-2 text-xs font-semibold uppercase tracking-[0.18em] text-cyan-300">Password file diff</p>
              <pre className="max-h-48 overflow-auto rounded-xl border border-white/10 bg-slate-950/70 p-4 text-xs text-slate-100">{preview.passwd_diff}</pre>
            </div>
          ) : null}

          {preview?.scope_issues?.length ? (
            <div className="rounded-2xl border border-rose-300/30 bg-rose-400/10 p-5 text-sm text-rose-50">
              <p className="text-xs font-semibold uppercase tracking-[0.18em] text-rose-200">Listener scope problems</p>
              <ul className="mt-2 space-y-1">
                {preview.scope_issues.map((issue) => <li key={issue}>{issue}</li>)}
              </ul>
            </div>
          ) : null}

          {preview?.composition_warnings?.length ? (
            <div className="rounded-2xl border border-amber-300/30 bg-amber-400/10 p-5 text-sm text-amber-50">
              <p className="text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">Composition warnings</p>
              <ul className="mt-2 space-y-1">
                {preview.composition_warnings.map((warning) => <li key={warning}>{warning}</li>)}
              </ul>
            </div>
          ) : null}

          {canApply ? <RecentMFAConfirmation state={mfa} /> : null}
          {!canApply ? (
            <p className="text-sm text-slate-400">Applying a deployment requires the admin role. {permissionTitle('deploy.apply')}.</p>
          ) : null}

          {applyError ? (
            <div role="alert" className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">
              <p className="font-semibold">Apply step failed</p>
              <p className="mt-1">{applyError}</p>
            </div>
          ) : null}

          {previewError ? (
            <div role="alert" className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">
              <p className="font-semibold">Preview step failed</p>
              <p className="mt-1">{previewError}</p>
            </div>
          ) : null}

          {result ? (
            <div
              role="status"
              className={`rounded-2xl border p-5 ${
                isVerified(result.status)
                  ? 'border-emerald-300/30 bg-emerald-400/10'
                  : result.status === 'rollback_failed' || result.status === 'failed'
                    ? 'border-rose-300/30 bg-rose-400/10'
                    : 'border-amber-300/30 bg-amber-400/10'
              }`}
            >
              <p
                className={`text-xs font-semibold uppercase tracking-[0.18em] ${
                  isVerified(result.status) ? 'text-emerald-200' : 'text-amber-200'
                }`}
              >
                {result.status === 'applied'
                  ? 'Applied — no activation proof recorded'
                  : isVerified(result.status)
                  ? 'Applied and verified'
                  : result.status === 'pending_activation'
                    ? 'Applied — activation pending'
                    : result.status === 'rollback_failed'
                      ? 'Rollback failed'
                      : result.status === 'rolled_back'
                        ? 'Rolled back'
                        : `Apply failed (${result.status})`}
              </p>
              {!isVerified(result.status) ? (
                <p className="mt-2 text-sm text-slate-300">
                  {(NON_VERIFIED_STATUS[result.status] ?? NON_VERIFIED_STATUS.failed).stage}{' '}
                  {(NON_VERIFIED_STATUS[result.status] ?? NON_VERIFIED_STATUS.failed).explanation}
                </p>
              ) : null}
              {result.message ? <p className="mt-2 text-sm text-slate-300">{result.message}</p> : null}
            </div>
          ) : null}

          {result && APPLIED_STATUSES.includes(result.status) ? (
            <button
              type="button"
              onClick={() => setStage('test')}
              className="mr-3 rounded-xl border border-cyan-300/40 px-4 py-2 text-sm font-semibold text-cyan-100 transition hover:border-cyan-300/70"
            >
              Test the connection
            </button>
          ) : null}

          {applyError || previewError || (result && !APPLIED_STATUSES.includes(result.status)) ? (
            <button
              type="button"
              onClick={() => { void handlePreview() }}
              disabled={previewLoading || !canPreview}
              title={!canPreview ? permissionTitle('deploy.preview') : undefined}
              className="mr-3 rounded-xl border border-cyan-300/40 px-4 py-2 text-sm font-semibold text-cyan-100 transition hover:border-cyan-300/70 disabled:opacity-50"
            >
              {previewLoading ? 'Generating…' : 'Generate a new preview'}
            </button>
          ) : null}

          <button
            type="button"
            onClick={() => { void handleApply() }}
            disabled={applying || !canApply || !mfa.proof || !preview?.revision_id || !preview?.has_changes}
            title={!canApply ? permissionTitle('deploy.apply') : undefined}
            className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
          >
            {applying ? 'Applying…' : 'Apply and verify'}
          </button>
        </div>
      ) : null}

      {stage === 'test' && result ? (
        <ConnectionTestStep
          token={token}
          onLogout={onLogout}
          role={role}
          username={username}
          topicFilter={topicFilter}
          permission={permission}
          scope={scope}
          listenerID={listenerID}
          deployment={result}
        />
      ) : null}
    </section>
  )
}
