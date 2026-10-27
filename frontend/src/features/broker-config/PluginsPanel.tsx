import { useState } from 'react'
import { can } from '../auth/permissions'
import { usePlugins } from './usePlugins'
import { pluginBackendLabels, pluginBackendTones } from './pluginTypes'
import type { Plugin, PluginSpec } from './pluginTypes'

const reloadKindTone: Record<string, string> = {
  reload: 'bg-cyan-400/10 text-cyan-200',
  restart: 'bg-amber-400/10 text-amber-200',
  both: 'bg-violet-400/10 text-violet-200',
}

// defaultOptions returns the options a plugin starts with when the
// operator enables it: the defaults of the catalog.
function defaultOptions(spec: PluginSpec): Record<string, string> {
  const options: Record<string, string> = {}
  for (const option of spec.options ?? []) {
    if (option.default) options[option.name] = option.default
  }
  return options
}

export default function PluginsPanel({
  token,
  onLogout,
  role = '',
}: {
  token: string
  onLogout: () => void
  role?: string
}) {
  const { plugins, preview, previewError, applyError, issues, isLoading, isPreviewing, isApplying, applied, appliedStatus, requestPreview, apply } =
    usePlugins({ token, onLogout })
  const [draft, setDraft] = useState<Plugin[] | undefined>()
  const [previewedPlugins, setPreviewedPlugins] = useState<Plugin[] | null>(null)
  const [confirmRestart, setConfirmRestart] = useState(false)
  const isAdmin = can(role, 'deploy.apply')
  // The preview endpoint requires an operator; the apply endpoint an admin.
  // The panel stays visible to an auditor, but the buttons that would be
  // refused are disabled.
  const canPreview = can(role, 'deploy.preview')

  const catalog = plugins?.catalog ?? []
  const loaded = plugins?.document?.plugins ?? []
  const specs = draft ?? loaded
  const dirty = draft !== undefined
  // A preview only applies to the exact set that is on screen. Without
  // this guard the operator could apply a revision that no longer matches
  // what they reviewed.
  const isPreviewCurrent = Boolean(preview?.revision_id) && JSON.stringify(previewedPlugins) === JSON.stringify(specs)
  const hasChanges = preview?.has_changes !== false
  const applyEnabled =
    isAdmin && !isApplying && isPreviewCurrent && hasChanges && (!preview?.requires_restart || confirmRestart)

  const updatePlugin = (index: number, changes: Partial<Plugin>) => {
    setDraft((current) => {
      const base = current ?? loaded
      return base.map((plugin, i) => (i === index ? { ...plugin, ...changes } : plugin))
    })
    setConfirmRestart(false)
  }

  const setOption = (index: number, name: string, value: string) => {
    const plugin = specs[index]
    if (!plugin) return
    const options = { ...(plugin.options ?? {}) }
    if (value === '') delete options[name]
    else options[name] = value
    updatePlugin(index, { options })
  }

  const enablePlugin = (spec: PluginSpec) => {
    setDraft((current) => [...(current ?? loaded), { id: spec.id, module: spec.module, options: defaultOptions(spec) }])
    setConfirmRestart(false)
  }

  const removePlugin = (index: number) => {
    setDraft((current) => (current ?? loaded).filter((_, i) => i !== index))
    setConfirmRestart(false)
  }

  const handlePreview = () => {
    setConfirmRestart(false)
    void requestPreview(specs).then(() => setPreviewedPlugins(specs))
  }

  const handleApply = async () => {
    if (!preview?.revision_id || !applyEnabled) return
    const result = await apply({
      revision_id: preview.revision_id,
      force: preview.requires_restart,
      adopt: preview.requires_restart,
    })
    if (result) {
      setDraft(undefined)
      setPreviewedPlugins(null)
      setConfirmRestart(false)
    }
  }

  if (isLoading && !plugins) {
    return (
      <section className="mt-8">
        <p className="text-sm text-slate-400">Loading plugins…</p>
      </section>
    )
  }

  if (!plugins) {
    return (
      <section className="mt-8">
        <div className="rounded-2xl border border-dashed border-white/10 bg-white/[0.03] p-5 text-sm text-slate-300">
          Plugin management is not configured on this MCM instance.
        </div>
      </section>
    )
  }

  return (
    <section className="mt-8 space-y-6">
      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Plugins</p>
            <p className="mt-1 text-sm text-slate-300">
              Load the plugins this broker needs and preview the change before it reaches the broker.
            </p>
            <p className="mt-1 text-xs text-slate-400">
              Mosquitto {plugins.version} · <span className="font-mono break-all">{plugins.path}</span>
            </p>
            <p className="mt-1 text-xs text-slate-400">{dirty ? 'Unsaved changes' : 'Saved'}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${pluginBackendTones[plugins.backend] ?? pluginBackendTones.none}`}>
              {pluginBackendLabels[plugins.backend] ?? plugins.backend}
            </span>
            <button
              type="button"
              onClick={handlePreview}
              disabled={!canPreview || isLoading || isPreviewing || isApplying}
              title={!canPreview ? 'Requires operator role' : undefined}
              className="rounded-xl border border-white/10 px-4 py-2 text-sm font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
            >
              {isPreviewing ? 'Generating…' : 'Preview'}
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
            {appliedStatus === 'pending_activation' ? (
              <p>
                The plugin is written to the configuration, but the broker was only reloaded. Restart it to activate the
                plugin: Mosquitto loads a plugin at start-up.
              </p>
            ) : (
              <p>Plugins applied. The broker was restarted, so the plugins are active.</p>
            )}
          </div>
        ) : null}
        {plugins.warnings?.length ? (
          <div className="mt-5 rounded-2xl border border-amber-300/30 bg-amber-400/10 p-5">
            <p className="text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">Warnings</p>
            <ul className="mt-3 list-disc space-y-1 pl-5 text-sm text-amber-100">
              {plugins.warnings.map((warning, index) => (
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
                <li key={`${issue.kind}-${issue.plugin}-${index}`}>
                  {issue.plugin ? <span className="font-mono text-xs">{issue.plugin}</span> : null}
                  {issue.plugin ? ' · ' : null}
                  {issue.message}
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </div>

      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Supported plugins</p>
        <p className="mt-1 text-sm text-slate-400">
          The plugins MCM supports for Mosquitto {plugins.version}. A plugin is loaded at start-up, so every change needs a
          restart.
        </p>

        <div className="mt-5 space-y-4">
          {catalog.length === 0 ? (
            <p className="text-sm text-slate-400">MCM knows no plugin for Mosquitto {plugins.version}.</p>
          ) : null}
          {catalog.map((spec) => {
            const enabled = specs.some((plugin) => plugin.id === spec.id)
            return (
              <div key={spec.id} className="rounded-2xl border border-white/10 bg-slate-950/40 p-4">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div>
                    <p className="text-sm font-semibold text-cyan-100">{spec.title}</p>
                    {spec.description ? <p className="mt-1 max-w-2xl text-xs text-slate-400">{spec.description}</p> : null}
                    <p className="mt-2 break-all font-mono text-xs text-slate-500">{spec.module}</p>
                    {spec.modules?.length ? (
                      <p className="mt-1 break-all font-mono text-xs text-slate-600">{spec.modules.join(' · ')}</p>
                    ) : null}
                    <p className="mt-1 text-xs text-slate-500">
                      {spec.since_version ? <>since Mosquitto {spec.since_version} · </> : null}
                      <span className="font-mono">{spec.id}</span>
                    </p>
                  </div>
                  <span className={`rounded-full px-2 py-0.5 text-xs font-semibold uppercase tracking-[0.18em] ${reloadKindTone[spec.reload_kind ?? ''] ?? reloadKindTone.restart}`}>
                    {spec.reload_kind || 'restart'}
                  </span>
                </div>

                {spec.options?.length ? (
                  <div className="mt-4">
                    <p className="text-xs font-semibold uppercase tracking-[0.18em] text-slate-400">Options</p>
                    <ul className="mt-2 space-y-2">
                      {spec.options.map((option) => (
                        <li key={option.name} className="text-sm text-slate-300">
                          <span className="font-mono text-cyan-200">plugin_opt_{option.name}</span>
                          {option.required ? <span className="ml-2 text-xs text-amber-200">required</span> : null}
                          {option.default ? (
                            <span className="ml-2 text-xs text-slate-500">
                              default <span className="font-mono">{option.default}</span>
                            </span>
                          ) : null}
                          {option.description ? <span className="mt-0.5 block text-xs text-slate-500">{option.description}</span> : null}
                        </li>
                      ))}
                    </ul>
                  </div>
                ) : null}

                <div className="mt-4">
                  {enabled ? (
                    <button
                      type="button"
                      aria-label={`Disable ${spec.title}`}
                      disabled={isApplying || isPreviewing}
                      onClick={() => {
                        const index = specs.findIndex((plugin) => plugin.id === spec.id)
                        if (index >= 0) removePlugin(index)
                      }}
                      className="rounded-xl border border-white/10 px-3 py-1.5 text-xs font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
                    >
                      Disable
                    </button>
                  ) : (
                    <button
                      type="button"
                      aria-label={`Enable ${spec.title}`}
                      disabled={isApplying || isPreviewing}
                      onClick={() => enablePlugin(spec)}
                      className="rounded-xl border border-white/10 px-3 py-1.5 text-xs font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
                    >
                      Enable
                    </button>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      </div>

      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Configured plugins</p>
        <p className="mt-1 text-sm text-slate-400">The plugins the broker loads. The plugin MCM does not know is preserved untouched.</p>

        <div className="mt-5 space-y-4">
          {specs.length === 0 ? (
            <div className="rounded-2xl border border-dashed border-white/10 bg-white/[0.03] p-5 text-sm text-slate-300">
              No plugin is configured. Enable one from the catalog to load it in the broker.
            </div>
          ) : null}

          {specs.map((plugin, index) => (
            <PluginCard
              key={index}
              plugin={plugin}
              spec={catalog.find((candidate) => candidate.id === plugin.id)}
              disabled={isApplying || isPreviewing}
              onChange={(changes) => updatePlugin(index, changes)}
              onOptionChange={(name, value) => setOption(index, name, value)}
              onRemove={() => removePlugin(index)}
            />
          ))}
        </div>
      </div>

      {preview ? (
        <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Preview</p>
            <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${reloadKindTone[preview.reload_kind ?? ''] ?? reloadKindTone.reload}`}>
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
                  className={`block ${line.startsWith('+') ? 'bg-emerald-500/10 text-emerald-200' : line.startsWith('-') ? 'bg-rose-500/10 text-rose-200' : 'text-slate-300'}`}
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
              Mosquitto loads a plugin at start-up, so this change needs a broker restart.
            </p>
          ) : null}

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
              disabled={!applyEnabled}
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

// PluginCard renders one configured plugin. A plugin the catalog knows is
// editable; a plugin it does not know is shown read-only, because MCM
// preserves it byte-for-byte.
function PluginCard({
  plugin,
  spec,
  disabled,
  onChange,
  onOptionChange,
  onRemove,
}: {
  plugin: Plugin
  spec?: PluginSpec
  disabled: boolean
  onChange: (changes: Partial<Plugin>) => void
  onOptionChange: (name: string, value: string) => void
  onRemove: () => void
}) {
  const known = Boolean(plugin.id)
  const label = plugin.id || plugin.module
  const preserved = Object.entries(plugin.unknown_options ?? {})

  return (
    <div className="rounded-2xl border border-white/10 bg-slate-950/40 p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">
            {known ? spec?.title ?? plugin.id : 'Unknown plugin'}
          </p>
          {known && spec?.description ? <p className="mt-1 max-w-2xl text-xs text-slate-400">{spec.description}</p> : null}
          {!known ? (
            <p className="mt-1 max-w-2xl text-xs text-slate-400">
              MCM does not know this plugin. Its directives are preserved exactly as they are and are never edited here.
            </p>
          ) : null}
        </div>
        {known ? (
          <button
            type="button"
            aria-label={`Remove plugin ${label}`}
            disabled={disabled}
            onClick={onRemove}
            className="rounded-xl border border-white/10 px-3 py-1.5 text-xs font-semibold text-rose-200 transition hover:border-rose-300/40 hover:text-rose-100 disabled:opacity-50"
          >
            Remove
          </button>
        ) : null}
      </div>

      {known ? (
        <div className="mt-4 grid gap-4 sm:grid-cols-2">
          <Field label="Module">
            <input
              aria-label={`Module ${label}`}
              type="text"
              disabled={disabled}
              value={plugin.module}
              onChange={(event) => onChange({ module: event.target.value })}
              className="w-full rounded bg-slate-800 px-2 py-1 font-mono text-sm text-slate-100"
            />
          </Field>
          {(spec?.options ?? []).map((option) => (
            <Field
              key={option.name}
              label={option.name}
              hint={option.required ? 'Required.' : undefined}
            >
              <input
                aria-label={`Option ${option.name} ${label}`}
                type="text"
                disabled={disabled}
                placeholder={option.default || (option.placeholder ? '/path/to/file' : '')}
                value={plugin.options?.[option.name] ?? ''}
                onChange={(event) => onOptionChange(option.name, event.target.value)}
                className="w-full rounded bg-slate-800 px-2 py-1 font-mono text-sm text-slate-100"
              />
            </Field>
          ))}
        </div>
      ) : (
        <p className="mt-4 break-all font-mono text-xs text-slate-400">{plugin.module}</p>
      )}

      {preserved.length > 0 ? (
        <div className="mt-4 rounded-2xl border border-white/10 bg-slate-900/60 p-3">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-slate-400">Preserved options</p>
          <p className="mt-1 text-xs text-slate-500">MCM does not manage these options and writes them back untouched.</p>
          <ul className="mt-2 space-y-1 font-mono text-xs text-slate-300">
            {preserved.map(([name, value]) => (
              <li key={name}>
                <span className="text-cyan-200">plugin_opt_{name}</span> = {value}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  )
}

// Field renders one labelled input with its hint.
function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <label className="block">
      <span className="text-xs font-semibold uppercase tracking-[0.18em] text-slate-400">{label}</span>
      <span className="mt-1 block">{children}</span>
      {hint ? <span className="mt-1 block text-xs text-slate-500">{hint}</span> : null}
    </label>
  )
}
