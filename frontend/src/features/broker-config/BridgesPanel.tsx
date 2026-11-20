import { useMemo, useState } from 'react'
import { can } from '../auth/permissions'
import { useBridges } from './useBridges'
import { useRecentMFA } from '../mfa/useRecentMFA'
import RecentMFAConfirmation from '../mfa/RecentMFAConfirmation'
import { deploymentLamp, lampPanel } from '../status/deploymentLamp'
import { DiffBlock, Section } from '../ui/primitives'
import { bodyText, fieldHint, fieldLabel, input, leverDanger, leverPrimary, leverSecondary, notice, select, tableHead, tableRow } from '../ui/styles'
import { DraftState, Findings, ReloadKind } from './panelParts'
import { ClientPager } from '../lists/ClientPager'
import { useClientPage } from '../lists/useClientPage'
import {
  bridgeDirections,
  bridgeProtocolVersions,
  bridgeStartTypes,
  bridgeTLSVersions,
  issuesForBridge,
  validateBridges,
} from './bridgeValidation'
import type { Bridge, BridgeTopicRule } from './bridgeTypes'

function applyNotice(status: string | undefined) {
  switch (status) {
    case 'active_verified':
      return {
        role: 'status' as const,
        message: 'Bridges applied. The broker confirmed the bridge configuration as active.',
      }
    case 'pending_activation':
      return {
        role: 'status' as const,
        message: 'Bridge configuration was applied, but broker activation could not be verified.',
      }
    case 'rolled_back':
      return {
        role: 'alert' as const,
        message: 'The bridge change did not activate; the previous configuration was restored.',
      }
    case 'rollback_failed':
      return {
        role: 'alert' as const,
        message: 'Bridge activation and rollback failed; manual recovery is required.',
      }
    case 'failed':
      return {
        role: 'alert' as const,
        message: 'Bridge apply failed; check the broker before continuing.',
      }
    default:
      return {
        role: 'status' as const,
        message: 'Apply completed, but broker activation status is unknown.',
      }
  }
}

// newBridge is the bridge an operator starts from: a named connection to a
// remote broker with a single bidirectional topic rule.
function newBridge(index: number): Bridge {
  return {
    name: `bridge-${index}`,
    address: '',
    remote_client_id: '',
    username: '',
    password: '',
    topics: [{ topic: '', direction: 'both', qos: 0, local_prefix: '', remote_prefix: '' }],
  }
}

// newTopicRule is the topic rule an operator starts from.
function newTopicRule(): BridgeTopicRule {
  return { topic: '', direction: 'both', qos: 0, local_prefix: '', remote_prefix: '' }
}

export default function BridgesPanel({
  token,
  onLogout,
  role = '',
}: {
  token: string
  onLogout: () => void
  role?: string
}) {
  const mfa = useRecentMFA(token, onLogout)
  const { bridges, preview, previewError, applyError, issues, isLoading, isPreviewing, isApplying, applied, appliedStatus, requestPreview, apply } =
    useBridges({ token, onLogout, onMFARejected: mfa.clear })
  const appliedNotice = applyNotice(appliedStatus)
  const appliedLamp = deploymentLamp(appliedStatus)
  const [draft, setDraft] = useState<Bridge[] | undefined>()
  const [previewedBridges, setPreviewedBridges] = useState<Bridge[] | null>(null)
  const [confirmRestart, setConfirmRestart] = useState(false)
  const isAdmin = can(role, 'deploy.apply')
  // The preview endpoint requires an operator; the apply endpoint an admin.
  // The panel stays visible to an auditor, but the buttons that would be
  // refused are disabled.
  const canPreview = can(role, 'deploy.preview')

  const specs = draft ?? bridges
  const dirty = draft !== undefined
  const pager = useClientPage(specs, { pageSize: 5, match: (bridge, query) => `${bridge.name ?? ''} ${bridge.address ?? ''}`.toLowerCase().includes(query) })
  const validation = useMemo(() => validateBridges(specs), [specs])
  const blockingIssues = validation.issues
  const warnings = validation.warnings
  // A preview only applies to the exact set that is on screen. Without
  // this guard the operator could apply a revision that no longer matches
  // what they reviewed.
  const isPreviewCurrent = Boolean(preview?.revision_id) && JSON.stringify(previewedBridges) === JSON.stringify(specs)
  const hasChanges = preview?.has_changes !== false
  const applyEnabled =
    isAdmin && Boolean(mfa.proof) && !isApplying && isPreviewCurrent && hasChanges && blockingIssues.length === 0 && (!preview?.requires_restart || confirmRestart)

  const updateBridge = (index: number, changes: Partial<Bridge>) => {
    setDraft((current) => {
      const base = current ?? bridges
      return base.map((bridge, i) => (i === index ? { ...bridge, ...changes } : bridge))
    })
    setConfirmRestart(false)
  }

  const addBridge = () => {
    const nextIndex = specs.reduce((max, bridge) => {
      const match = bridge.name?.match(/^bridge-(\d+)$/)
      return match ? Math.max(max, Number(match[1])) : max
    }, 0) + 1
    setDraft((current) => [...(current ?? bridges), newBridge(nextIndex)])
    setConfirmRestart(false)
    pager.showLast()
  }

  const removeBridge = (index: number) => {
    setDraft((current) => (current ?? bridges).filter((_, i) => i !== index))
    setConfirmRestart(false)
  }

  const updateTopic = (bridgeIndex: number, topicIndex: number, changes: Partial<BridgeTopicRule>) => {
    const bridge = specs[bridgeIndex]
    if (!bridge) return
    const topics = (bridge.topics ?? []).map((rule, i) => (i === topicIndex ? { ...rule, ...changes } : rule))
    updateBridge(bridgeIndex, { topics })
  }

  const addTopic = (bridgeIndex: number) => {
    const bridge = specs[bridgeIndex]
    if (!bridge) return
    updateBridge(bridgeIndex, { topics: [...(bridge.topics ?? []), newTopicRule()] })
  }

  const removeTopic = (bridgeIndex: number, topicIndex: number) => {
    const bridge = specs[bridgeIndex]
    if (!bridge) return
    updateBridge(bridgeIndex, { topics: (bridge.topics ?? []).filter((_, i) => i !== topicIndex) })
  }

  const handlePreview = () => {
    setConfirmRestart(false)
    void requestPreview(specs).then(() => setPreviewedBridges(specs))
  }

  const handleApply = async () => {
    if (!preview?.revision_id || !applyEnabled) return
    const result = await apply({
      revision_id: preview.revision_id,
      force: preview.requires_restart,
      adopt: preview.requires_restart,
    }, mfa.proof)
    if (result) {
      setDraft(undefined)
      setPreviewedBridges(null)
      setConfirmRestart(false)
    }
  }

  if (isLoading && bridges.length === 0 && !draft) {
    return (
      <section className="mt-8">
        <p className={bodyText}>Loading bridges…</p>
      </section>
    )
  }

  return (
    <section className="mt-8 space-y-10">
      <Section
        title="Bridges"
        description="Connect this broker to remote brokers and preview the change before it reaches the broker."
        aside={
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              onClick={addBridge}
              disabled={isLoading || isApplying}
              className={leverSecondary}
            >
              Add bridge
            </button>
            <button
              type="button"
              onClick={handlePreview}
              disabled={!canPreview || isLoading || isPreviewing || isApplying || blockingIssues.length > 0}
              title={!canPreview ? 'Requires operator role' : undefined}
              className={leverSecondary}
            >
              {isPreviewing ? 'Generating…' : 'Preview'}
            </button>
          </div>
        }
      >
        <DraftState dirty={dirty} />

        {previewError ? (
          <div className={`mt-5 ${notice.failed}`} role="alert">
            {previewError}
          </div>
        ) : null}
        {applyError ? (
          <div className={`mt-5 ${notice.failed}`} role="alert">
            {applyError}
          </div>
        ) : null}
        {applied ? (
          <div className={`mt-5 px-4 py-3 text-sm ${lampPanel[appliedLamp]}`} role={appliedNotice.role}>
            {appliedNotice.message}
          </div>
        ) : null}
        {blockingIssues.length > 0 ? (
          <Findings tone="failed" title="Issues">
            {blockingIssues.map((issue, index) => (
              <li key={`${issue.kind}-${issue.bridge}-${index}`}>
                {issue.bridge ? <span className="font-mono text-xs">{issue.bridge}</span> : null}
                {issue.bridge ? ' · ' : null}
                {issue.message}
              </li>
            ))}
          </Findings>
        ) : null}
        {warnings.length > 0 ? (
          <Findings tone="caution" title="Warnings">
            {warnings.map((warning, index) => (
              <li key={`${warning.kind}-${warning.bridge}-${index}`}>
                {warning.bridge ? <span className="font-mono text-xs">{warning.bridge}</span> : null}
                {warning.bridge ? ' · ' : null}
                {warning.message}
              </li>
            ))}
          </Findings>
        ) : null}
        {issues.length > 0 && issues.every((issue) => issue.severity === 'warning') ? (
          <Findings tone="caution" title="Server warnings">
            {issues.map((issue, index) => (
              <li key={`${issue.kind}-${issue.bridge}-${index}`}>{issue.message}</li>
            ))}
          </Findings>
        ) : null}
      </Section>

      {specs.length === 0 ? (
        <div className={notice.neutral}>
          No bridge is configured. Add one to connect this broker to a remote broker.
        </div>
      ) : null}

      <ClientPager pager={pager} noun={{ one: 'bridge', many: 'bridges' }} searchLabel="Filter bridges by name or address" />

      {pager.rows.map(({ item: bridge, index }) => (
        <BridgeCard
          key={index}
          bridge={bridge}
          issues={issuesForBridge(blockingIssues, bridge.name)}
          warnings={issuesForBridge(warnings, bridge.name)}
          disabled={isApplying || isPreviewing}
          onChange={(changes) => updateBridge(index, changes)}
          onRemove={() => removeBridge(index)}
          onTopicChange={(topicIndex, changes) => updateTopic(index, topicIndex, changes)}
          onTopicAdd={() => addTopic(index)}
          onTopicRemove={(topicIndex) => removeTopic(index, topicIndex)}
        />
      ))}

      {preview ? (
        <Section title="Preview" aside={<ReloadKind kind={preview.reload_kind || 'reload'} />}>
          <p className="mt-2 break-all font-mono text-xs text-enamel-muted">revision {preview.revision_id}</p>
          {preview.changed_directives?.length ? (
            <p className={`mt-2 ${fieldHint}`}>
              Changed: <span className="font-mono text-enamel-bright">{preview.changed_directives.join(', ')}</span>
            </p>
          ) : null}
          {preview.conf_diff ? (
            <div className="mt-4"><DiffBlock content={preview.conf_diff} /></div>
          ) : (
            <p className={`mt-4 ${bodyText}`}>No changes — the configuration already matches.</p>
          )}

          {preview.requires_restart ? (
            <p className="mt-4 text-xs text-lamp">
              Mosquitto reads a bridge at start-up, so this change needs a broker restart.
            </p>
          ) : null}

          {isAdmin ? <div className="mt-5"><RecentMFAConfirmation state={mfa} /></div> : null}

          <div className="mt-5 flex flex-wrap items-center gap-3">
            {preview.requires_restart ? (
              <label className="flex items-center gap-2 text-sm text-enamel">
                <input
                  type="checkbox"
                  aria-label="Confirm broker restart"
                  disabled={isApplying}
                  checked={confirmRestart}
                  onChange={(event) => setConfirmRestart(event.target.checked)}
                />
                Confirm broker restart
              </label>
            ) : null}
            <button
              type="button"
              onClick={() => { void handleApply() }}
              disabled={!applyEnabled}
              title={!isAdmin ? 'Requires admin role' : undefined}
              className={leverPrimary}
            >
              {isApplying ? 'Applying…' : 'Apply'}
            </button>
            {isAdmin && !mfa.proof ? <p className="text-xs text-lamp">Confirm MFA above before applying.</p> : null}
          </div>
        </Section>
      ) : null}
    </section>
  )
}

// BridgeCard renders the editable form of one bridge.
function BridgeCard({
  bridge,
  issues,
  warnings,
  disabled,
  onChange,
  onRemove,
  onTopicChange,
  onTopicAdd,
  onTopicRemove,
}: {
  bridge: Bridge
  issues: Array<{ kind: string; field?: string; topic?: string; message: string }>
  warnings: Array<{ kind: string; field?: string; topic?: string; message: string }>
  disabled: boolean
  onChange: (changes: Partial<Bridge>) => void
  onRemove: () => void
  onTopicChange: (topicIndex: number, changes: Partial<BridgeTopicRule>) => void
  onTopicAdd: () => void
  onTopicRemove: (topicIndex: number) => void
}) {
  const name = bridge.name ?? ''
  const tls = bridge.tls ?? {}
  const fieldIssues = (field: string) => issues.filter((issue) => issue.field === field)
  const fieldWarnings = (field: string) => warnings.filter((issue) => issue.field === field)

  return (
    <Section
      title="Bridge"
      description="A connection to a remote broker and the topics it forwards."
      aside={
        <button
          type="button"
          aria-label={`Remove bridge ${name}`}
          disabled={disabled}
          onClick={onRemove}
          className={leverDanger}
        >
          Remove
        </button>
      }
    >

      <div className="mt-5 grid gap-4 sm:grid-cols-2">
        <Field label="Name" error={fieldIssues('name').map((issue) => issue.message)}>
          <input
            aria-label={`Name ${name}`}
            type="text"
            disabled={disabled}
            value={name}
            onChange={(event) => onChange({ name: event.target.value })}
            className={`w-full font-mono ${input}`}
          />
        </Field>
        <Field label="Address" error={fieldIssues('address').map((issue) => issue.message)}>
          <input
            aria-label={`Address ${name}`}
            type="text"
            disabled={disabled}
            placeholder="broker.example.org:1883"
            value={bridge.address ?? ''}
            onChange={(event) => onChange({ address: event.target.value })}
            className={`w-full font-mono ${input}`}
          />
        </Field>
        <Field label="Remote client id" error={fieldIssues('remote_client_id').map((issue) => issue.message)}>
          <input
            aria-label={`Remote client id ${name}`}
            type="text"
            disabled={disabled}
            value={bridge.remote_client_id ?? ''}
            onChange={(event) => onChange({ remote_client_id: event.target.value })}
            className={`w-full font-mono ${input}`}
          />
        </Field>
        <Field label="Local client id" error={fieldIssues('local_client_id').map((issue) => issue.message)}>
          <input
            aria-label={`Local client id ${name}`}
            type="text"
            disabled={disabled}
            value={bridge.local_client_id ?? ''}
            onChange={(event) => onChange({ local_client_id: event.target.value })}
            className={`w-full font-mono ${input}`}
          />
        </Field>
        <Field label="User name" error={fieldIssues('username').map((issue) => issue.message)}>
          <input
            aria-label={`User name ${name}`}
            type="text"
            disabled={disabled}
            value={bridge.username ?? ''}
            onChange={(event) => onChange({ username: event.target.value })}
            className={`w-full font-mono ${input}`}
          />
        </Field>
        <Field
          label="Password"
          error={fieldIssues('password').map((issue) => issue.message)}
          warning={fieldWarnings('password').map((issue) => issue.message)}
          hint={bridge.password_set ? 'A password is stored. Leave the field empty to keep it.' : undefined}
        >
          <input
            aria-label={`Password ${name}`}
            type="password"
            disabled={disabled}
            placeholder={bridge.password_set ? 'stored' : ''}
            value={bridge.password ?? ''}
            onChange={(event) => onChange({ password: event.target.value })}
            className={`w-full font-mono ${input}`}
          />
        </Field>
      </div>

      <h4 className="mt-6 text-sm font-semibold text-enamel-bright">Reconnection</h4>
      <div className="mt-3 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <Field label="Protocol version">
          <select
            aria-label={`Protocol version ${name}`}
            disabled={disabled}
            value={bridge.protocol_version ?? ''}
            onChange={(event) => onChange({ protocol_version: event.target.value || undefined })}
            className={`w-full ${select}`}
          >
            <option value="">broker default</option>
            {bridgeProtocolVersions.map((version) => (
              <option key={version} value={version}>{version}</option>
            ))}
          </select>
        </Field>
        <Field label="Start type">
          <select
            aria-label={`Start type ${name}`}
            disabled={disabled}
            value={bridge.start_type ?? ''}
            onChange={(event) => onChange({ start_type: event.target.value || undefined })}
            className={`w-full ${select}`}
          >
            <option value="">broker default</option>
            {bridgeStartTypes.map((type) => (
              <option key={type} value={type}>{type}</option>
            ))}
          </select>
        </Field>
        <Field label="Keepalive (seconds)">
          <input
            aria-label={`Keepalive ${name}`}
            type="number"
            min={5}
            max={65535}
            disabled={disabled}
            value={bridge.keepalive_seconds ?? ''}
            onChange={(event) => onChange({ keepalive_seconds: event.target.value === '' ? undefined : Number(event.target.value) })}
            className={`w-full font-mono ${input}`}
          />
        </Field>
        <Field label="Restart timeout" hint="Seconds, or a base and cap pair such as 5 30.">
          <input
            aria-label={`Restart timeout ${name}`}
            type="text"
            disabled={disabled}
            placeholder="5 30"
            value={bridge.restart_timeout ?? ''}
            onChange={(event) => onChange({ restart_timeout: event.target.value || undefined })}
            className={`w-full font-mono ${input}`}
          />
        </Field>
        <Field label="Threshold">
          <input
            aria-label={`Threshold ${name}`}
            type="number"
            min={1}
            disabled={disabled}
            value={bridge.threshold ?? ''}
            onChange={(event) => onChange({ threshold: event.target.value === '' ? undefined : Number(event.target.value) })}
            className={`w-full font-mono ${input}`}
          />
        </Field>
        <Field label="Idle timeout (seconds)">
          <input
            aria-label={`Idle timeout ${name}`}
            type="number"
            min={0}
            disabled={disabled}
            value={bridge.idle_timeout_seconds ?? ''}
            onChange={(event) => onChange({ idle_timeout_seconds: event.target.value === '' ? undefined : Number(event.target.value) })}
            className={`w-full font-mono ${input}`}
          />
        </Field>
      </div>

      <div className="mt-4 flex flex-wrap gap-4">
        <Toggle
          label="Clean session"
          ariaLabel={`Clean session ${name}`}
          checked={bridge.clean_session === true}
          disabled={disabled}
          onChange={(checked) => onChange({ clean_session: checked })}
        />
        <Toggle
          label="Try private"
          ariaLabel={`Try private ${name}`}
          checked={bridge.try_private === true}
          disabled={disabled}
          onChange={(checked) => onChange({ try_private: checked })}
        />
        <Toggle
          label="Notifications"
          ariaLabel={`Notifications ${name}`}
          checked={bridge.notifications === true}
          disabled={disabled}
          onChange={(checked) => onChange({ notifications: checked })}
        />
        <Toggle
          label="Round robin"
          ariaLabel={`Round robin ${name}`}
          checked={bridge.round_robin === true}
          disabled={disabled}
          onChange={(checked) => onChange({ round_robin: checked })}
        />
      </div>

      <div className="mt-4 grid gap-4 sm:grid-cols-2">
        <Field label="Notification topic" error={fieldIssues('notification_topic').map((issue) => issue.message)}>
          <input
            aria-label={`Notification topic ${name}`}
            type="text"
            disabled={disabled}
            value={bridge.notification_topic ?? ''}
            onChange={(event) => onChange({ notification_topic: event.target.value || undefined })}
            className={`w-full font-mono ${input}`}
          />
        </Field>
      </div>

      <h4 className="mt-6 text-sm font-semibold text-enamel-bright">TLS</h4>
      <div className="mt-3 grid gap-4 sm:grid-cols-2">
        <Field label="CA file">
          <input
            aria-label={`CA file ${name}`}
            type="text"
            disabled={disabled}
            value={tls.ca_file ?? ''}
            onChange={(event) => onChange({ tls: { ...tls, ca_file: event.target.value } })}
            className={`w-full font-mono ${input}`}
          />
        </Field>
        <Field label="CA path">
          <input
            aria-label={`CA path ${name}`}
            type="text"
            disabled={disabled}
            value={tls.ca_path ?? ''}
            onChange={(event) => onChange({ tls: { ...tls, ca_path: event.target.value } })}
            className={`w-full font-mono ${input}`}
          />
        </Field>
        <Field label="Client certificate" error={fieldIssues('tls.cert_file').map((issue) => issue.message)}>
          <input
            aria-label={`Client certificate ${name}`}
            type="text"
            disabled={disabled}
            value={tls.cert_file ?? ''}
            onChange={(event) => onChange({ tls: { ...tls, cert_file: event.target.value } })}
            className={`w-full font-mono ${input}`}
          />
        </Field>
        <Field label="Private key" error={fieldIssues('tls.key_file').map((issue) => issue.message)}>
          <input
            aria-label={`Private key ${name}`}
            type="text"
            disabled={disabled}
            value={tls.key_file ?? ''}
            onChange={(event) => onChange({ tls: { ...tls, key_file: event.target.value } })}
            className={`w-full font-mono ${input}`}
          />
        </Field>
        <Field label="TLS version">
          <select
            aria-label={`TLS version ${name}`}
            disabled={disabled}
            value={tls.tls_version ?? ''}
            onChange={(event) => onChange({ tls: { ...tls, tls_version: event.target.value || undefined } })}
            className={`w-full ${select}`}
          >
            <option value="">broker default</option>
            {bridgeTLSVersions.map((version) => (
              <option key={version} value={version}>{version}</option>
            ))}
          </select>
        </Field>
        <div className="flex items-end pb-1">
          <Toggle
            label="Skip certificate verification"
            ariaLabel={`Skip certificate verification ${name}`}
            checked={tls.insecure === true}
            disabled={disabled}
            onChange={(checked) => onChange({ tls: { ...tls, insecure: checked } })}
          />
        </div>
      </div>
      {fieldWarnings('tls.insecure').length > 0 ? (
        <p className="mt-2 text-xs text-lamp">{fieldWarnings('tls.insecure').map((issue) => issue.message).join(' ')}</p>
      ) : null}

      <div className="mt-6 flex flex-wrap items-center justify-between gap-3">
        <h4 className="text-sm font-semibold text-enamel-bright">Topic rules</h4>
        <button
          type="button"
          onClick={onTopicAdd}
          disabled={disabled}
          className={leverSecondary}
        >
          Add topic rule
        </button>
      </div>
      <div className="mt-3 table-scroll">
        <table className="w-full min-w-[46rem] text-left text-sm">
          <thead>
            <tr className="border-b border-track-strong">
              <th className={`${tableHead} text-left`}>Topic</th>
              <th className={`${tableHead} text-left`}>Direction</th>
              <th className={`${tableHead} text-left`}>QoS</th>
              <th className={`${tableHead} text-left`}>Local prefix</th>
              <th className={`${tableHead} text-left`}>Remote prefix</th>
              <th className={tableHead} />
            </tr>
          </thead>
          <tbody>
            {(bridge.topics ?? []).map((rule, topicIndex) => {
              const topicIssues = issues.filter((issue) => issue.topic === rule.topic && issue.field === 'topics')
              return (
                <tr key={topicIndex} className={`${tableRow} align-top`}>
                  <td className="py-2 pr-3">
                    <input
                      aria-label={`Topic ${name} ${topicIndex + 1}`}
                      type="text"
                      disabled={disabled}
                      placeholder="both/#"
                      value={rule.topic ?? ''}
                      onChange={(event) => onTopicChange(topicIndex, { topic: event.target.value })}
                      className={`w-full font-mono ${input}`}
                    />
                    {topicIssues.map((issue, index) => (
                      <p key={`${issue.kind}-${index}`} className="mt-1 text-xs text-danger-text">{issue.message}</p>
                    ))}
                  </td>
                  <td className="py-2 pr-3">
                    <select
                      aria-label={`Direction ${name} ${topicIndex + 1}`}
                      disabled={disabled}
                      value={rule.direction ?? 'both'}
                      onChange={(event) => onTopicChange(topicIndex, { direction: event.target.value as BridgeTopicRule['direction'] })}
                      className={select}
                    >
                      {bridgeDirections.map((direction) => (
                        <option key={direction} value={direction}>{direction}</option>
                      ))}
                    </select>
                  </td>
                  <td className="py-2 pr-3">
                    <select
                      aria-label={`QoS ${name} ${topicIndex + 1}`}
                      disabled={disabled}
                      value={String(rule.qos ?? 0)}
                      onChange={(event) => onTopicChange(topicIndex, { qos: Number(event.target.value) })}
                      className={select}
                    >
                      <option value="0">0</option>
                      <option value="1">1</option>
                      <option value="2">2</option>
                    </select>
                  </td>
                  <td className="py-2 pr-3">
                    <input
                      aria-label={`Local prefix ${name} ${topicIndex + 1}`}
                      type="text"
                      disabled={disabled}
                      value={rule.local_prefix ?? ''}
                      onChange={(event) => onTopicChange(topicIndex, { local_prefix: event.target.value })}
                      className={`w-full font-mono ${input}`}
                    />
                  </td>
                  <td className="py-2 pr-3">
                    <input
                      aria-label={`Remote prefix ${name} ${topicIndex + 1}`}
                      type="text"
                      disabled={disabled}
                      value={rule.remote_prefix ?? ''}
                      onChange={(event) => onTopicChange(topicIndex, { remote_prefix: event.target.value })}
                      className={`w-full font-mono ${input}`}
                    />
                  </td>
                  <td className="py-2 pr-3">
                    <button
                      type="button"
                      aria-label={`Remove topic ${name} ${topicIndex + 1}`}
                      disabled={disabled}
                      onClick={() => onTopicRemove(topicIndex)}
                      className={leverDanger}
                    >
                      Remove
                    </button>
                  </td>
                </tr>
              )
            })}
            {(bridge.topics ?? []).length === 0 ? (
              <tr>
                <td colSpan={6} className={`py-4 ${bodyText}`}>
                  This bridge has no topic rule. Mosquitto refuses a connection without topics.
                </td>
              </tr>
            ) : null}
          </tbody>
        </table>
      </div>
    </Section>
  )
}

// Field renders one labelled input with its findings.
function Field({
  label,
  hint,
  error,
  warning,
  children,
}: {
  label: string
  hint?: string
  error?: string[]
  warning?: string[]
  children: React.ReactNode
}) {
  return (
    <label className="block">
      <span className={fieldLabel}>{label}</span>
      <span className="mt-2 block">{children}</span>
      {hint ? <span className={`mt-1 block ${fieldHint}`}>{hint}</span> : null}
      {(error ?? []).map((message) => (
        <span key={message} className="mt-1 block text-xs text-danger-text">{message}</span>
      ))}
      {(warning ?? []).map((message) => (
        <span key={message} className="mt-1 block text-xs text-lamp">{message}</span>
      ))}
    </label>
  )
}

// Toggle renders one checkbox.
function Toggle({
  label,
  ariaLabel,
  checked,
  disabled,
  onChange,
}: {
  label: string
  ariaLabel: string
  checked: boolean
  disabled: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <label className="flex items-center gap-2 text-sm text-enamel">
      <input
        type="checkbox"
        aria-label={ariaLabel}
        disabled={disabled}
        checked={checked}
        onChange={(event) => onChange(event.target.checked)}
      />
      {label}
    </label>
  )
}
