import { useMemo, useState } from 'react'
import { can } from '../auth/permissions'
import { useSettings } from './useSettings'
import { deploymentLamp, lampPanel } from '../status/deploymentLamp'
import { DiffBlock, Section } from '../ui/primitives'
import { bodyText, fieldHint, input, leverDanger, leverPrimary, leverSecondary, notice, select } from '../ui/styles'
import { DraftState, Findings, ReloadKind } from './panelParts'
import { useRecentMFA } from '../mfa/useRecentMFA'
import RecentMFAConfirmation from '../mfa/RecentMFAConfirmation'
import type { SettingsDocument, SettingsEntry, SettingsField } from './settingsTypes'

type Occurrence = { values: string[] }
type Draft = Record<string, Occurrence[]>

// draftFromDocument turns the document the server sent into the editable
// state of the form. A directive the server marked unset has no occurrence.
function draftFromDocument(document?: SettingsDocument): Draft {
  const draft: Draft = {}
  for (const entry of document?.entries ?? []) {
    if (entry.unset) continue
    draft[entry.directive] = [...(draft[entry.directive] ?? []), { values: [...(entry.values ?? [])] }]
  }
  return draft
}

// buildDocument turns the editable state back into the document the server
// expects. Every managed directive is listed: the ones without an
// occurrence are marked unset so the broker default applies.
function buildDocument(draft: Draft, fields: SettingsField[]): SettingsDocument {
  const entries: SettingsEntry[] = []
  for (const field of fields) {
    const occurrences = draft[field.directive] ?? []
    if (occurrences.length === 0) {
      entries.push({ directive: field.directive, unset: true })
      continue
    }
    for (const occurrence of occurrences) {
      entries.push({ directive: field.directive, values: occurrence.values })
    }
  }
  return { entries }
}

// boundText describes the range the broker accepts, with its unit.
function boundText(field: SettingsField): string {
  const parts: string[] = []
  if (field.min !== undefined && field.max !== undefined) parts.push(`${field.min} – ${field.max}`)
  else if (field.min !== undefined) parts.push(`≥ ${field.min}`)
  else if (field.max !== undefined) parts.push(`≤ ${field.max}`)
  if (field.unit) parts.push(field.unit)
  return parts.join(' ')
}

// defaultValuesFor returns the value a directive starts with when the
// operator switches it on. It comes from the catalog so the form never
// guesses a range.
function defaultValuesFor(field: SettingsField): string[] {
  if (field.type === 'boolean') return ['true']
  if (field.type === 'integer') {
    const defaultValue = field.default?.trim() ? Number(field.default) : Number.NaN
    const defaultWithinBounds = Number.isFinite(defaultValue) &&
      (field.min === undefined || defaultValue >= field.min) &&
      (field.max === undefined || defaultValue <= field.max)
    if (defaultWithinBounds) return [field.default!]
    if (field.min !== undefined) return [String(field.min)]
    if (field.max !== undefined) return [String(field.max)]
    return ['']
  }
  if (field.type === 'enum') return [field.allowed_values?.[0] ?? '']
  if (field.type === 'duration') {
    const suffix = field.duration_suffixes?.[0] ?? ''
    return suffix ? [`1${suffix}`] : ['']
  }
  return ['']
}

function applyNotice(status: string | undefined, requiresRestart: boolean) {
  switch (status) {
    case 'active_verified':
      return {
        role: 'status' as const,
        message: requiresRestart ? 'Settings applied. The broker was restarted.' : 'Settings applied. The broker was signalled to reload.',
      }
    case 'pending_activation':
      return {
        role: 'status' as const,
        message: 'Settings were written, but broker activation is pending.',
      }
    case 'rolled_back':
      return {
        role: 'alert' as const,
        message: 'Settings did not activate; the previous configuration was restored.',
      }
    case 'rollback_failed':
      return {
        role: 'alert' as const,
        message: 'Settings activation and rollback failed; manual recovery is required.',
      }
    case 'failed':
      return {
        role: 'alert' as const,
        message: 'Settings apply failed; check the broker before continuing.',
      }
    default:
      return {
        role: 'status' as const,
        message: 'Apply completed, but broker activation status is unknown.',
      }
  }
}

export default function SettingsPanel({
  token,
  onLogout,
  role = '',
}: {
  token: string
  onLogout: () => void
  role?: string
}) {
  const mfa = useRecentMFA(token, onLogout)
  const { settings, preview, previewError, applyError, issues, isLoading, isPreviewing, isApplying, applied, applyResult, requestPreview, apply } =
    useSettings({ token, onLogout, onMFARejected: mfa.clear })
  const [draft, setDraft] = useState<Draft | undefined>()
  const [confirmRestart, setConfirmRestart] = useState(false)
  const [appliedRequiresRestart, setAppliedRequiresRestart] = useState(false)
  const appliedNotice = applyNotice(applyResult?.status, appliedRequiresRestart)
  const appliedLamp = deploymentLamp(applyResult?.status)
  const isAdmin = role === 'admin'
  // The preview endpoint requires an operator; the apply endpoint an
  // admin. The panel stays visible to an auditor, but the buttons that
  // would be refused are disabled.
  const canPreview = can(role, 'deploy.preview')

  const fields = useMemo(() => (settings?.sections ?? []).flatMap((section) => section.fields), [settings])
  const current = draft ?? draftFromDocument(settings?.document)
  const dirty = useMemo(() => {
    const base = draftFromDocument(settings?.document)
    return JSON.stringify(buildDocument(current, fields)) !== JSON.stringify(buildDocument(base, fields))
  }, [current, fields, settings])

  const updateOccurrence = (directive: string, index: number, values: string[]) => {
    setDraft((state) => {
      const next: Draft = { ...(state ?? draftFromDocument(settings?.document)) }
      const occurrences = [...(next[directive] ?? [])]
      occurrences[index] = { values }
      next[directive] = occurrences
      return next
    })
    setConfirmRestart(false)
  }

  const setOccurrence = (directive: string, index: number, values: string[]) => {
    setDraft((state) => {
      const next: Draft = { ...(state ?? draftFromDocument(settings?.document)) }
      const occurrences = [...(next[directive] ?? [])]
      while (occurrences.length <= index) occurrences.push({ values: [] })
      occurrences[index] = { values }
      next[directive] = occurrences
      return next
    })
    setConfirmRestart(false)
  }

  const addOccurrence = (directive: string) => {
    setDraft((state) => {
      const next: Draft = { ...(state ?? draftFromDocument(settings?.document)) }
      next[directive] = [...(next[directive] ?? []), { values: [''] }]
      return next
    })
    setConfirmRestart(false)
  }

  const removeOccurrence = (directive: string, index: number) => {
    setDraft((state) => {
      const next: Draft = { ...(state ?? draftFromDocument(settings?.document)) }
      const occurrences = [...(next[directive] ?? [])]
      occurrences.splice(index, 1)
      next[directive] = occurrences
      return next
    })
    setConfirmRestart(false)
  }

  const handlePreview = () => {
    setConfirmRestart(false)
    setAppliedRequiresRestart(false)
    void requestPreview(buildDocument(current, fields))
  }

  const handleApply = async () => {
    if (!preview?.revision_id) return
    const requiresRestart = preview.requires_restart === true
    setAppliedRequiresRestart(requiresRestart)
    const ok = await apply({
      revision_id: preview.revision_id,
      force: requiresRestart,
      // Settings apply is the adoption action for this reviewed revision.
      // The backend requires a fresh adoption even for reload-only changes.
      adopt: true,
    }, mfa.proof)
    if (ok) {
      setDraft(undefined)
      setConfirmRestart(false)
    } else {
      setAppliedRequiresRestart(false)
    }
  }

  if (isLoading && !settings) {
    return (
      <section className="mt-8">
        <p className={bodyText}>Loading broker settings…</p>
      </section>
    )
  }

  if (!settings) {
    return (
      <section className="mt-8">
        <div className={notice.neutral}>
          Broker settings are not configured on this MCM instance.
        </div>
      </section>
    )
  }

  return (
    <section className="mt-8 space-y-10">
      <Section
        title="Broker settings"
        description={<>Edit the general settings of <span className="font-mono">{settings.path}</span> and preview the change before it reaches the broker.</>}
        aside={
          <button
            type="button"
            onClick={handlePreview}
            disabled={!canPreview || isLoading || isPreviewing || isApplying}
            title={!canPreview ? 'Requires operator role' : undefined}
            className={leverSecondary}
          >
            {isPreviewing ? 'Generating…' : 'Preview changes'}
          </button>
        }
      >
        <p className={`mt-1 ${fieldHint}`}>Mosquitto {settings.version}</p>
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
        {settings.warnings?.length ? (
          <Findings tone="caution" title="Warnings">
            {settings.warnings.map((warning, index) => (
              <li key={`${index}-${warning}`}>{warning}</li>
            ))}
          </Findings>
        ) : null}
        {issues.length > 0 ? (
          <Findings tone="failed" title="Issues">
            {issues.map((issue, index) => (
              <li key={`${issue.kind}-${issue.directive}-${index}`}>
                <span className="font-mono text-xs">{issue.directive}</span> · {issue.message}
              </li>
            ))}
          </Findings>
        ) : null}
      </Section>

      {settings.sections.map((section) => (
        <Section key={section.id} title={section.title} description={section.hint || undefined}>
          <div className="mt-4">
            {section.fields.map((field) => {
              const occurrences = current[field.directive] ?? []
              const repeatable = field.multiplicity === 'many'
              return (
                <div key={field.directive} className="border-t border-track py-4 first:border-t-0 first:pt-0">
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="min-w-0">
                      <h4 className="font-mono text-sm font-semibold text-enamel-bright">{field.directive}</h4>
                      <p className={`mt-1 max-w-[68ch] ${fieldHint}`}>{field.description}</p>
                    </div>
                    <ReloadKind kind={field.reload_kind} />
                  </div>

                  <p className={`mt-2 ${fieldHint}`}>
                    {boundText(field) ? <span className="font-mono">{boundText(field)}</span> : null}
                    {boundText(field) && field.default ? ' · ' : null}
                    {field.default ? <>default <span className="font-mono">{field.default}</span></> : null}
                  </p>

                  <div className="mt-3 space-y-2">
                    {repeatable ? (
                      <>
                        {occurrences.length === 0 ? (
                          <p className={fieldHint}>Not set in the configuration — the broker default applies.</p>
                        ) : null}
                        {occurrences.map((occurrence, index) => (
                          <div key={`${field.directive}-${index}`} className="flex flex-wrap items-center gap-2">
                            <OccurrenceEditor
                              field={field}
                              values={occurrence.values}
                              disabled={isApplying}
                              onChange={(values) => updateOccurrence(field.directive, index, values)}
                            />
                            <button
                              type="button"
                              aria-label={`Remove ${field.directive} ${index + 1}`}
                              disabled={isApplying}
                              onClick={() => removeOccurrence(field.directive, index)}
                              className={leverDanger}
                            >
                              Remove
                            </button>
                          </div>
                        ))}
                      </>
                    ) : (
                      <label className="flex flex-wrap items-center gap-3">
                        <span className="inline-flex items-center gap-2 text-sm text-enamel">
                          <input
                            type="checkbox"
                            aria-label={`Set ${field.directive}`}
                            disabled={isApplying}
                            checked={occurrences.length > 0}
                            onChange={(event) => {
                              if (event.target.checked) {
                                setOccurrence(field.directive, 0, defaultValuesFor(field))
                              } else {
                                removeOccurrence(field.directive, 0)
                              }
                            }}
                          />
                          Set in the configuration
                        </span>
                        {occurrences.length > 0 ? (
                          <OccurrenceEditor
                            field={field}
                            values={occurrences[0].values}
                            disabled={isApplying}
                            onChange={(values) => updateOccurrence(field.directive, 0, values)}
                          />
                        ) : (
                          <span className={fieldHint}>Not set — the broker default applies.</span>
                        )}
                      </label>
                    )}
                  </div>

                  {repeatable ? (
                    <button
                      type="button"
                      onClick={() => addOccurrence(field.directive)}
                      disabled={isApplying}
                      className={`mt-3 ${leverSecondary}`}
                    >
                      Add {field.directive}
                    </button>
                  ) : null}
                </div>
              )
            })}
          </div>
        </Section>
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
              This change needs a broker restart: {preview.changed_directives?.join(', ') || 'a restart-required directive'}.
            </p>
          ) : (
            <p className={`mt-4 ${fieldHint}`}>The broker picks this up on the next reload.</p>
          )}

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
              disabled={!isAdmin || !mfa.proof || isApplying || (preview.requires_restart && !confirmRestart)}
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

// OccurrenceEditor renders the input of one occurrence of a directive.
// The shape of the input follows the catalog: a boolean is a choice, an
// integer is a number with the bounds of the broker, a duration is a
// number and a unit, and an enum is a choice of the allowed values.
function OccurrenceEditor({
  field,
  values,
  disabled,
  onChange,
}: {
  field: SettingsField
  values: string[]
  disabled: boolean
  onChange: (values: string[]) => void
}) {
  const label = `${field.directive} value`

  if (field.type === 'boolean') {
    return (
      <select
        aria-label={label}
        disabled={disabled}
        value={values[0] ?? 'true'}
        onChange={(event) => onChange([event.target.value])}
        className={select}
      >
        <option value="true">true</option>
        <option value="false">false</option>
      </select>
    )
  }

  if (field.type === 'integer') {
    return (
      <input
        aria-label={label}
        type="number"
        disabled={disabled}
        min={field.min}
        max={field.max}
        value={values[0] ?? ''}
        onChange={(event) => onChange([event.target.value])}
        className={`w-32 font-mono ${input}`}
      />
    )
  }

  if (field.type === 'duration') {
    const raw = values[0] ?? ''
    const suffix = field.duration_suffixes?.find((candidate) => raw.endsWith(candidate)) ?? field.duration_suffixes?.[0] ?? ''
    const digits = suffix && raw.endsWith(suffix) ? raw.slice(0, -suffix.length) : raw
    return (
      <span className="inline-flex items-center gap-1">
        <input
          aria-label={`${field.directive} amount`}
          type="number"
          min={1}
          disabled={disabled}
          value={digits}
          onChange={(event) => onChange([`${event.target.value}${suffix}`])}
          className={`w-28 font-mono ${input}`}
        />
        <select
          aria-label={`${field.directive} unit`}
          disabled={disabled}
          value={suffix}
          onChange={(event) => onChange([`${digits}${event.target.value}`])}
          className={select}
        >
          {(field.duration_suffixes ?? []).map((candidate) => (
            <option key={candidate} value={candidate}>
              {candidate}
            </option>
          ))}
        </select>
      </span>
    )
  }

  if (field.type === 'enum') {
    const extra = values.slice(1)
    return (
      <span className="inline-flex flex-wrap items-center gap-2">
        <select
          aria-label={label}
          disabled={disabled}
          value={values[0] ?? ''}
          onChange={(event) => {
            const next = [event.target.value, ...extra]
            // The file destination needs the path of the log file.
            if (event.target.value !== 'file') next.length = 1
            onChange(next)
          }}
          className={select}
        >
          <option value="">select…</option>
          {(field.allowed_values ?? []).map((allowed) => (
            <option key={allowed} value={allowed}>
              {allowed}
            </option>
          ))}
        </select>
        {values[0] === 'file' ? (
          <input
            aria-label={`${field.directive} path`}
            type="text"
            disabled={disabled}
            value={extra[0] ?? ''}
            onChange={(event) => onChange([values[0], event.target.value])}
            placeholder="/var/log/mosquitto.log"
            className={`w-64 font-mono ${input}`}
          />
        ) : null}
      </span>
    )
  }

  return (
    <input
      aria-label={label}
      type="text"
      disabled={disabled}
      value={values[0] ?? ''}
      onChange={(event) => onChange([event.target.value])}
      className={`w-72 font-mono ${input}`}
    />
  )
}
