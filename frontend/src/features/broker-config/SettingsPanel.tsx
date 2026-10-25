import { useMemo, useState } from 'react'
import { can } from '../auth/permissions'
import { useSettings } from './useSettings'
import type { SettingsDocument, SettingsEntry, SettingsField } from './settingsTypes'

type Occurrence = { values: string[] }
type Draft = Record<string, Occurrence[]>

const reloadKindTone: Record<string, string> = {
  reload: 'bg-cyan-400/10 text-cyan-200',
  restart: 'bg-amber-400/10 text-amber-200',
  both: 'bg-violet-400/10 text-violet-200',
}

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
  if (field.type === 'integer') return [field.default ?? '']
  if (field.type === 'enum') return [field.allowed_values?.[0] ?? '']
  if (field.type === 'duration') {
    const suffix = field.duration_suffixes?.[0] ?? ''
    return suffix ? [`1${suffix}`] : ['']
  }
  return ['']
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
  const { settings, preview, previewError, applyError, issues, isLoading, isPreviewing, isApplying, applied, requestPreview, apply } =
    useSettings({ token, onLogout })
  const [draft, setDraft] = useState<Draft | undefined>()
  const [confirmRestart, setConfirmRestart] = useState(false)
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
    void requestPreview(buildDocument(current, fields))
  }

  const handleApply = async () => {
    if (!preview?.revision_id) return
    const ok = await apply({
      revision_id: preview.revision_id,
      force: preview.requires_restart,
      adopt: preview.requires_restart,
    })
    if (ok) {
      setDraft(undefined)
      setConfirmRestart(false)
    }
  }

  if (isLoading && !settings) {
    return (
      <section className="mt-8">
        <p className="text-sm text-slate-400">Loading broker settings…</p>
      </section>
    )
  }

  if (!settings) {
    return (
      <section className="mt-8">
        <div className="rounded-2xl border border-dashed border-white/10 bg-white/[0.03] p-5 text-sm text-slate-300">
          Broker settings are not configured on this MCM instance.
        </div>
      </section>
    )
  }

  return (
    <section className="mt-8 space-y-6">
      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Broker settings</p>
            <p className="mt-1 text-sm text-slate-300">
              Edit the general settings of <span className="font-mono">{settings.path}</span> and preview the change before it reaches the broker.
            </p>
            <p className="mt-1 text-xs text-slate-400">Mosquitto {settings.version}</p>
            <p className="mt-1 text-xs text-slate-400">{dirty ? 'Unsaved changes' : 'Saved'}</p>
          </div>          <div className="flex gap-2">
            <button
              type="button"
              onClick={handlePreview}
              disabled={!canPreview || isLoading || isPreviewing || isApplying}
              title={!canPreview ? 'Requires operator role' : undefined}
              className="rounded-xl border border-white/10 px-4 py-2 text-sm font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
            >
              {isPreviewing ? 'Generating…' : 'Preview changes'}
            </button>
          </div>
        </div>

        {previewError ? (
          <div className="mt-5 rounded-2xl border border-dashed border-rose-300/30 bg-rose-400/10 p-5 text-sm text-rose-100" role="alert">
            {previewError}
          </div>
        ) : null}
        {applyError ? (
          <div className="mt-5 rounded-2xl border border-dashed border-rose-300/30 bg-rose-400/10 p-5 text-sm text-rose-100" role="alert">
            {applyError}
          </div>
        ) : null}
        {applied ? (
          <div className="mt-5 rounded-2xl border border-emerald-300/30 bg-emerald-400/10 p-5 text-sm text-emerald-100" role="status">
            Settings applied. The broker was signalled to reload.
          </div>
        ) : null}
        {settings.warnings?.length ? (
          <div className="mt-5 rounded-2xl border border-amber-300/30 bg-amber-400/10 p-5">
            <p className="text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">Warnings</p>
            <ul className="mt-3 list-disc space-y-1 pl-5 text-sm text-amber-100">
              {settings.warnings.map((warning, index) => (
                <li key={`${index}-${warning}`}>{warning}</li>
              ))}
            </ul>
          </div>
        ) : null}
        {issues.length > 0 ? (
          <div className="mt-5 rounded-2xl border border-rose-300/30 bg-rose-400/10 p-5">
            <p className="text-xs font-semibold uppercase tracking-[0.18em] text-rose-200">Issues</p>
            <ul className="mt-3 list-disc space-y-1 pl-5 text-sm text-rose-100">
              {issues.map((issue, index) => (
                <li key={`${issue.kind}-${issue.directive}-${index}`}>
                  <span className="font-mono text-xs">{issue.directive}</span> · {issue.message}
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </div>

      {settings.sections.map((section) => (
        <div key={section.id} className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">{section.title}</p>
          {section.hint ? <p className="mt-1 text-sm text-slate-400">{section.hint}</p> : null}

          <div className="mt-5 space-y-4">
            {section.fields.map((field) => {
              const occurrences = current[field.directive] ?? []
              const repeatable = field.multiplicity === 'many'
              return (
                <div key={field.directive} className="rounded-2xl border border-white/10 bg-slate-950/40 p-4">
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div>
                      <p className="font-mono text-sm text-cyan-100">{field.directive}</p>
                      <p className="mt-1 max-w-2xl text-xs text-slate-400">{field.description}</p>
                    </div>
                    <span className={`rounded-full px-2 py-0.5 text-xs font-semibold uppercase tracking-[0.18em] ${reloadKindTone[field.reload_kind] ?? reloadKindTone.reload}`}>
                      {field.reload_kind}
                    </span>
                  </div>

                  <p className="mt-2 text-xs text-slate-500">
                    {boundText(field) ? <span className="font-mono">{boundText(field)}</span> : null}
                    {boundText(field) && field.default ? ' · ' : null}
                    {field.default ? <>default <span className="font-mono">{field.default}</span></> : null}
                  </p>

                  <div className="mt-3 space-y-2">
                    {repeatable ? (
                      <>
                        {occurrences.length === 0 ? (
                          <p className="text-xs text-slate-500">Not set in the configuration — the broker default applies.</p>
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
                              className="text-xs text-rose-200 disabled:opacity-50"
                            >
                              Remove
                            </button>
                          </div>
                        ))}
                      </>
                    ) : (
                      <label className="flex flex-wrap items-center gap-3">
                        <span className="inline-flex items-center gap-2 text-xs text-slate-300">
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
                          <span className="text-xs text-slate-500">Not set — the broker default applies.</span>
                        )}
                      </label>
                    )}
                  </div>

                  {repeatable ? (
                    <button
                      type="button"
                      onClick={() => addOccurrence(field.directive)}
                      disabled={isApplying}
                      className="mt-3 rounded-xl border border-white/10 px-3 py-1 text-xs font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
                    >
                      Add {field.directive}
                    </button>
                  ) : null}
                </div>
              )
            })}
          </div>
        </div>
      ))}

      {preview ? (
        <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Preview</p>
            <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${reloadKindTone[preview.reload_kind] ?? reloadKindTone.reload}`}>
              {preview.reload_kind || 'reload'}
            </span>
          </div>
          <p className="mt-2 break-all font-mono text-xs text-slate-400">revision {preview.revision_id}</p>
          {preview.changed_directives?.length ? (
            <p className="mt-2 text-xs text-slate-300">
              Changed: <span className="font-mono text-cyan-200">{preview.changed_directives.join(', ')}</span>
            </p>
          ) : null}
          {preview.conf_diff ? (
            <pre className="mt-4 max-h-64 overflow-auto rounded-2xl border border-white/10 bg-slate-950/70 p-4 font-mono text-xs leading-5">
              {preview.conf_diff.split('\n').map((line, index) => (
                <span
                  key={index}
                  className={`block ${line.startsWith('+') ? 'bg-emerald-500/10 text-emerald-200' : line.startsWith('-') ? 'bg-red-500/10 text-red-200' : 'text-slate-300'}`}
                >
                  {line || ' '}
                </span>
              ))}
            </pre>
          ) : (
            <p className="mt-4 text-sm text-slate-300">No changes — the configuration already matches.</p>
          )}

          {preview.requires_restart ? (
            <p className="mt-4 text-xs text-amber-200">
              This change needs a broker restart: {preview.changed_directives?.join(', ') || 'a restart-required directive'}.
            </p>
          ) : (
            <p className="mt-4 text-xs text-cyan-200">The broker picks this up on the next reload.</p>
          )}

          <div className="mt-5 flex flex-wrap items-center gap-3">
            {preview.requires_restart ? (
              <label className="flex items-center gap-2 text-sm text-slate-200">
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
              disabled={!isAdmin || isApplying || (preview.requires_restart && !confirmRestart)}
              title={!isAdmin ? 'Requires admin role' : undefined}
              className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
            >
              {isApplying ? 'Applying…' : 'Apply'}
            </button>
          </div>
        </div>
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
        className="rounded bg-slate-800 px-2 py-1 text-sm text-slate-100"
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
        className="w-32 rounded bg-slate-800 px-2 py-1 text-sm text-slate-100"
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
          className="w-28 rounded bg-slate-800 px-2 py-1 text-sm text-slate-100"
        />
        <select
          aria-label={`${field.directive} unit`}
          disabled={disabled}
          value={suffix}
          onChange={(event) => onChange([`${digits}${event.target.value}`])}
          className="rounded bg-slate-800 px-2 py-1 text-sm text-slate-100"
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
          className="rounded bg-slate-800 px-2 py-1 text-sm text-slate-100"
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
            className="w-64 rounded bg-slate-800 px-2 py-1 text-sm text-slate-100"
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
      className="w-72 rounded bg-slate-800 px-2 py-1 text-sm text-slate-100"
    />
  )
}
