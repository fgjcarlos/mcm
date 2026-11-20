import { useState, type FormEvent } from 'react'
import { authenticatedFetch, isForbiddenResponseError, isUnauthorizedResponseError } from '../api/client'
import { can, permissionTitle } from '../auth/permissions'
import { isMFARejection, useRecentMFA } from '../mfa/useRecentMFA'
import RecentMFAConfirmation from '../mfa/RecentMFAConfirmation'
import type { ACLPermission, ACLScope } from '../acl/types'
import ConnectionTestStep from './ConnectionTestStep'
import { deploymentLamp, lampPanel } from '../status/deploymentLamp'
import { DiffBlock, Lamp } from '../ui/primitives'
import { bodyText, codeBlock, fieldHint, fieldLabel, input, leverPrimary, leverSecondary, notice, select, type LampTone } from '../ui/styles'

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

// A finished step is done, not verified: enamel, never green. The step in progress is the amber route.
const STEP_LAMP: Record<'done' | 'current' | 'todo', { tone: LampTone; word: string }> = {
  done: { tone: 'neutral', word: 'done' },
  current: { tone: 'caution', word: 'in progress' },
  todo: { tone: 'off', word: 'to do' },
}

// A failed step of the journey: a failed request, so the one red notice.
function StepFailure({ title, message }: { title: string; message: string }) {
  return (
    <div role="alert" className={notice.failed}>
      <p className="font-semibold">{title}</p>
      <p className="mt-1">{message}</p>
    </div>
  )
}

// A deployment that left the configuration on disk. The activation
// verification may still be pending for it.
const APPLIED_STATUSES = ['active_verified', 'applied', 'pending_activation']

// What each non-verified status honestly means for the broker configuration,
// and the stage it belongs to. `failed` does not say the previous
// configuration was restored: the applier may or may not have restored it.
const NON_VERIFIED_STATUS: Record<string, { stage: string; explanation: string }> = {
  applied: {
    stage: 'Stage: activation verification (no proof recorded).',
    explanation: 'The configuration was written to the broker, but the activation was not proven.',
  },
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
      // `applied` wrote the config without activation proof: the saved changes are no
      // longer pending a deploy, so clear the marker (its lamp stays amber).
      if (deployment.status === 'active_verified' || deployment.status === 'applied') {
        onApplySuccess?.(mutationWatermark)
      }
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isMFARejection(err)) mfa.clear()
      if (isForbiddenResponseError(err)) { setApplyError((err as Error).message); return }
      setApplyError('Could not reach the server.')
    } finally {
      setApplying(false)
    }
  }

  const stepIndex = STEPS.findIndex((step) => step.id === stage)

  const resultLamp = result ? deploymentLamp(result.status) : null

  return (
    <section aria-label="MQTT user onboarding" className="border-y border-track-strong py-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <h4 className="text-base font-semibold text-enamel-bright">MQTT user onboarding</h4>
          <p className={`mt-1 max-w-[68ch] ${bodyText}`}>
            User, access rule, preview, apply and a test with your own MQTT client in one journey. The apply still needs a recent MFA confirmation and
            verifies the activation against the live broker.
          </p>
        </div>
        <button
          type="button"
          onClick={onClose}
          className={leverSecondary}
        >
          Close journey
        </button>
      </div>

      <ol className="mt-5 flex flex-wrap items-center gap-x-5 gap-y-2">
        {STEPS.map((step, index) => {
          const state = index < stepIndex ? 'done' : index === stepIndex ? 'current' : 'todo'
          const lamp = STEP_LAMP[state]
          return (
            <li key={step.id}>
              <span aria-current={state === 'current' ? 'step' : undefined} className="inline-flex items-baseline gap-2">
                <Lamp tone={lamp.tone}>{index + 1}. {step.label}</Lamp>
                <span className={`font-mono text-xs ${state === 'current' ? 'text-lamp' : 'text-enamel-muted'}`}>{lamp.word}</span>
              </span>
            </li>
          )
        })}
      </ol>

      {createdPassword ? (
        <div className={`mt-5 ${notice.caution}`}>
          <p className="font-semibold text-lamp">User created — save this password</p>
          <p className="mt-1">This password will not be shown again.</p>
          <pre className={`mt-3 ${codeBlock}`}>{createdPassword}</pre>
          <div className="mt-3 flex flex-wrap gap-2">
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
              className={leverSecondary}
            >
              {passwordCopied ? 'Copied' : 'Copy password'}
            </button>
            <button
              type="button"
              onClick={() => { setCreatedPassword(''); setPasswordCopied(false); setPasswordCopyFailed(false) }}
              className={leverSecondary}
            >
              Dismiss
            </button>
          </div>
          {passwordCopyFailed ? (
            <p role="status" className="mt-2 text-xs text-lamp">
              Copy failed: select the password and copy it by hand.
            </p>
          ) : null}
        </div>
      ) : null}

      {stage === 'user' ? (
        <form onSubmit={handleCreateUser} className="mt-6 max-w-2xl space-y-5">
          <label className="block">
            <span className={fieldLabel}>Username</span>
            <input
              type="text"
              required
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="device-sensor-01"
              className={`mt-2 w-full font-mono ${input}`}
            />
            <span className={`mt-1 block ${fieldHint}`}>A secure password will be generated automatically.</span>
          </label>
          {userError ? <StepFailure title="User creation failed" message={userError} /> : null}
          <div className="flex flex-wrap items-center gap-3">
            <button
              type="submit"
              disabled={creatingUser || !canWriteUser}
              title={!canWriteUser ? permissionTitle('mqttUser.write') : undefined}
              className={leverPrimary}
            >
              {creatingUser ? 'Creating…' : 'Create user'}
            </button>
            <button
              type="button"
              onClick={onClose}
              className={leverSecondary}
            >
              Cancel
            </button>
          </div>
        </form>
      ) : null}

      {stage === 'rule' ? (
        <form onSubmit={handleSaveRule} className="mt-6 max-w-2xl space-y-5">
          <p className={bodyText}>
            The access rule for <span className="font-mono text-enamel-bright">{username}</span>. It is a normal MCM rule: the
            ACL panel lists it and the apply renders it into the Mosquitto ACL file.
          </p>
          <label className="block">
            <span className={fieldLabel}>Topic filter</span>
            <input
              type="text"
              required
              value={topicFilter}
              onChange={(e) => setTopicFilter(e.target.value)}
              placeholder="sensors/# or device/+/status"
              className={`mt-2 w-full font-mono ${input}`}
            />
            <span className={`mt-1 block ${fieldHint}`}>MQTT wildcards: + (single level), # (multi-level, must be last)</span>
          </label>
          <label className="block">
            <span className={fieldLabel}>Permission</span>
            <select
              aria-label="Permission"
              value={permission}
              onChange={(e) => setPermission(e.target.value as ACLPermission)}
              className={`mt-2 w-full ${select}`}
            >
              <option value="read">read</option>
              <option value="write">write</option>
              <option value="readwrite">readwrite</option>
              <option value="deny">deny</option>
            </select>
            <span className={`mt-1 block ${fieldHint}`}>
              A readwrite grant on this identity is what lets the activation verification prove the new user against the
              live broker. A narrower grant is still applied, but the activation is then verified through the labelled
              managed service account.
            </span>
          </label>
          <label className="block">
            <span className={fieldLabel}>ACL file</span>
            <select
              value={scope}
              onChange={(e) => setScope(e.target.value as ACLScope)}
              className={`mt-2 w-full ${select}`}
            >
              <option value="global">Broker-wide &mdash; the rule applies to every listener</option>
              <option value="listener">One listener &mdash; the rule applies to that listener only</option>
            </select>
          </label>
          {scope === 'listener' ? (
            <label className="block">
              <span className={fieldLabel}>Listener</span>
              <input
                type="text"
                required
                value={listenerID}
                onChange={(e) => setListenerID(e.target.value)}
                placeholder="tls"
                className={`mt-2 w-full font-mono ${input}`}
              />
            </label>
          ) : null}
          <label className="block">
            <span className={fieldLabel}>Description (optional)</span>
            <input
              type="text"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Short description for audit purposes"
              className={`mt-2 w-full ${input}`}
            />
          </label>
          {ruleError ? <StepFailure title="Access rule step failed" message={ruleError} /> : null}
          <div className="flex flex-wrap items-center gap-3">
            <button
              type="submit"
              disabled={savingRule || !canWriteRule}
              title={!canWriteRule ? permissionTitle('acl.write') : undefined}
              className={leverPrimary}
            >
              {savingRule ? 'Saving…' : 'Save access rule'}
            </button>
            <button
              type="button"
              onClick={onClose}
              className={leverSecondary}
            >
              Cancel
            </button>
          </div>
        </form>
      ) : null}

      {stage === 'preview' ? (
        <div className="mt-6 space-y-4">
          <p className={`max-w-[68ch] ${bodyText}`}>
            The preview shows the exact ACL and password file changes this journey would apply. Nothing is written to the
            broker until the apply.
          </p>
          {previewError ? <StepFailure title="Preview step failed" message={previewError} /> : null}
          <button
            type="button"
            onClick={() => { void handlePreview() }}
            disabled={previewLoading || !canPreview}
            title={!canPreview ? permissionTitle('deploy.preview') : undefined}
            className={leverPrimary}
          >
            {previewLoading ? 'Generating…' : 'Preview changes'}
          </button>
        </div>
      ) : null}

      {stage === 'apply' ? (
        <div className="mt-6 space-y-5">
          {preview && !preview.has_changes ? (
            <p className={notice.neutral}>
              No pending changes — the configuration is already up to date.
            </p>
          ) : null}

          {preview?.acl_diff ? (
            <div>
              <h5 className="mb-2 text-sm font-semibold text-enamel-bright">ACL diff</h5>
              <DiffBlock content={preview.acl_diff} />
            </div>
          ) : null}

          {preview?.passwd_diff ? (
            <div>
              <h5 className="mb-2 text-sm font-semibold text-enamel-bright">Password file diff</h5>
              <DiffBlock content={preview.passwd_diff} />
            </div>
          ) : null}

          {preview?.scope_issues?.length ? (
            <div className={notice.failed}>
              <h5 className="font-semibold">Listener scope problems</h5>
              <div role="region" aria-label="Listener scope problems" tabIndex={0} className="mt-2 max-h-96 overflow-y-auto">
                <ul className="space-y-1 font-mono text-xs text-enamel-bright">
                  {preview.scope_issues.map((issue) => <li key={issue} className="break-words">{issue}</li>)}
                </ul>
              </div>
            </div>
          ) : null}

          {preview?.composition_warnings?.length ? (
            <div className={notice.caution}>
              <h5 className="font-semibold text-lamp">Composition warnings</h5>
              <div role="region" aria-label="Composition warnings" tabIndex={0} className="mt-2 max-h-96 overflow-y-auto">
                <ul className="space-y-1">
                  {preview.composition_warnings.map((warning) => <li key={warning} className="break-words">{warning}</li>)}
                </ul>
              </div>
            </div>
          ) : null}

          {canApply ? <RecentMFAConfirmation state={mfa} /> : null}
          {!canApply ? (
            <p className={fieldHint}>Applying a deployment requires the admin role. {permissionTitle('deploy.apply')}.</p>
          ) : null}

          {applyError ? <StepFailure title="Apply step failed" message={applyError} /> : null}

          {previewError ? <StepFailure title="Preview step failed" message={previewError} /> : null}

          {result && resultLamp ? (
            <div
              role="status"
              className={`px-4 py-3 text-sm leading-6 ${lampPanel[resultLamp]}`}
            >
              <p className="font-semibold">
                <Lamp tone={resultLamp}>
                  {result.status === 'applied'
                    ? 'Applied — no activation proof recorded'
                    : resultLamp === 'verified'
                    ? 'Applied and verified'
                    : result.status === 'pending_activation'
                      ? 'Applied — activation pending'
                      : result.status === 'rollback_failed'
                        ? 'Rollback failed'
                        : result.status === 'rolled_back'
                          ? 'Rolled back'
                          : `Apply failed (${result.status})`}
                </Lamp>
              </p>
              {resultLamp !== 'verified' ? (
                <p className="mt-2 text-enamel">
                  {(NON_VERIFIED_STATUS[result.status] ?? NON_VERIFIED_STATUS.failed).stage}{' '}
                  {(NON_VERIFIED_STATUS[result.status] ?? NON_VERIFIED_STATUS.failed).explanation}
                </p>
              ) : null}
              {result.message ? <p className="mt-2 text-enamel-soft">{result.message}</p> : null}
            </div>
          ) : null}

          <div className="flex flex-wrap items-center gap-3">
            {result && APPLIED_STATUSES.includes(result.status) ? (
              <button
                type="button"
                onClick={() => setStage('test')}
                className={leverPrimary}
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
                className={leverSecondary}
              >
                {previewLoading ? 'Generating…' : 'Generate a new preview'}
              </button>
            ) : null}

            {/* One primary lever: Apply until there is a result, then the next step. */}
            <button
              type="button"
              onClick={() => { void handleApply() }}
              disabled={applying || !canApply || !mfa.proof || !preview?.revision_id || !preview?.has_changes}
              title={!canApply ? permissionTitle('deploy.apply') : undefined}
              className={result ? leverSecondary : leverPrimary}
            >
              {applying ? 'Applying…' : 'Apply and verify'}
            </button>
          </div>
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
