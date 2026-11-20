import { useState } from 'react'
import { can } from '../auth/permissions'
import { usePlugins } from './usePlugins'
import { useRecentMFA } from '../mfa/useRecentMFA'
import RecentMFAConfirmation from '../mfa/RecentMFAConfirmation'
import { pluginBackendLabels, pluginBackendTones } from './pluginTypes'
import type { Plugin, PluginSpec } from './pluginTypes'
import { deploymentLamp, lampPanel } from '../status/deploymentLamp'
import { DiffBlock, Plate, Section } from '../ui/primitives'
import { bodyText, fieldHint, fieldLabel, input, leverDanger, leverPrimary, leverSecondary, notice } from '../ui/styles'
import { DraftState, Findings, ReloadKind } from './panelParts'
import { ClientPager } from '../lists/ClientPager'
import { useClientPage } from '../lists/useClientPage'

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
  const mfa = useRecentMFA(token, onLogout)
  const { plugins, preview, previewError, applyError, issues, isLoading, isPreviewing, isApplying, applied, appliedStatus, requestPreview, apply } =
    usePlugins({ token, onLogout, onMFARejected: mfa.clear })
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
  const catalogPager = useClientPage(catalog, { match: (spec, query) => `${spec.title} ${spec.id} ${spec.module}`.toLowerCase().includes(query) })
  const configuredPager = useClientPage(specs, { match: (plugin, query) => `${plugin.id ?? ''} ${plugin.module}`.toLowerCase().includes(query) })
  // A preview only applies to the exact set that is on screen. Without
  // this guard the operator could apply a revision that no longer matches
  // what they reviewed.
  const isPreviewCurrent = Boolean(preview?.revision_id) && JSON.stringify(previewedPlugins) === JSON.stringify(specs)
  const hasChanges = preview?.has_changes !== false
  const applyEnabled =
    isAdmin && Boolean(mfa.proof) && !isApplying && isPreviewCurrent && hasChanges && (!preview?.requires_restart || confirmRestart)

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
    configuredPager.showLast()
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
    }, mfa.proof)
    if (result) {
      setDraft(undefined)
      setPreviewedPlugins(null)
      setConfirmRestart(false)
    }
  }

  if (isLoading && !plugins) {
    return (
      <section className="mt-8">
        <p className={bodyText}>Loading plugins…</p>
      </section>
    )
  }

  if (!plugins) {
    return (
      <section className="mt-8">
        <div className={notice.neutral}>
          Plugin management is not configured on this MCM instance.
        </div>
      </section>
    )
  }

  return (
    <section className="mt-8 space-y-10">
      <Section
        title="Plugins"
        description="Load the plugins this broker needs and preview the change before it reaches the broker."
        aside={
          <div className="flex flex-wrap items-center gap-2">
            <Plate tone={pluginBackendTones[plugins.backend] ?? pluginBackendTones.none}>
              {pluginBackendLabels[plugins.backend] ?? plugins.backend}
            </Plate>
            <button
              type="button"
              onClick={handlePreview}
              disabled={!canPreview || isLoading || isPreviewing || isApplying}
              title={!canPreview ? 'Requires operator role' : undefined}
              className={leverSecondary}
            >
              {isPreviewing ? 'Generating…' : 'Preview'}
            </button>
          </div>
        }
      >
        <p className={`mt-1 ${fieldHint}`}>
          Mosquitto {plugins.version} · <span className="font-mono break-all">{plugins.path}</span>
        </p>
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
          <div className={`mt-5 px-4 py-3 text-sm ${lampPanel[deploymentLamp(appliedStatus)]}`} role="status">
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
          <Findings tone="caution" title="Warnings">
            {plugins.warnings.map((warning, index) => (
              <li key={`${index}-${warning}`}>{warning}</li>
            ))}
          </Findings>
        ) : null}
        {issues.length > 0 ? (
          <Findings tone="failed" title="Issues">
            {issues.map((issue, index) => (
              <li key={`${issue.kind}-${issue.plugin}-${index}`}>
                {issue.plugin ? <span className="font-mono text-xs">{issue.plugin}</span> : null}
                {issue.plugin ? ' · ' : null}
                {issue.message}
              </li>
            ))}
          </Findings>
        ) : null}
      </Section>

      <Section
        title="Supported plugins"
        description={`The plugins MCM supports for Mosquitto ${plugins.version}. A plugin is loaded at start-up, so every change needs a restart.`}
      >
        <div className="mt-4 space-y-4">
          <ClientPager pager={catalogPager} noun={{ one: 'supported plugin', many: 'supported plugins' }} searchLabel="Filter supported plugins" />
          <div>
            {catalog.length === 0 ? (
              <p className={bodyText}>MCM knows no plugin for Mosquitto {plugins.version}.</p>
            ) : null}
            {catalogPager.rows.map(({ item: spec }) => {
              const enabled = specs.some((plugin) => plugin.id === spec.id)
              return (
                <div key={spec.id} className="border-t border-track py-4 first:border-t-0 first:pt-0">
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="min-w-0">
                      <h4 className="text-sm font-semibold text-enamel-bright">{spec.title}</h4>
                      {spec.description ? <p className={`mt-1 max-w-[68ch] ${fieldHint}`}>{spec.description}</p> : null}
                      <p className="mt-2 break-all font-mono text-xs text-enamel-soft">{spec.module}</p>
                      {spec.modules?.length ? (
                        <p className="mt-1 break-all font-mono text-xs text-enamel-muted">{spec.modules.join(' · ')}</p>
                      ) : null}
                      <p className={`mt-1 ${fieldHint}`}>
                        {spec.since_version ? <>since Mosquitto {spec.since_version} · </> : null}
                        <span className="font-mono">{spec.id}</span>
                      </p>
                    </div>
                    <ReloadKind kind={spec.reload_kind || 'restart'} />
                  </div>

                  {spec.options?.length ? (
                    <div className="mt-4">
                      <h5 className="text-xs font-semibold text-enamel">Options</h5>
                      <ul className="mt-2 space-y-2">
                        {spec.options.map((option) => (
                          <li key={option.name} className="text-sm text-enamel-soft">
                            <span className="font-mono text-enamel-bright">plugin_opt_{option.name}</span>
                            {option.required ? <span className="ml-2 text-xs text-lamp">required</span> : null}
                            {option.default ? (
                              <span className="ml-2 text-xs text-enamel-muted">
                                default <span className="font-mono">{option.default}</span>
                              </span>
                            ) : null}
                            {option.description ? <span className={`mt-0.5 block ${fieldHint}`}>{option.description}</span> : null}
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
                        className={leverSecondary}
                      >
                        Disable
                      </button>
                    ) : (
                      <button
                        type="button"
                        aria-label={`Enable ${spec.title}`}
                        disabled={isApplying || isPreviewing}
                        onClick={() => enablePlugin(spec)}
                        className={leverSecondary}
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
      </Section>

      <Section title="Configured plugins" description="The plugins the broker loads. The plugin MCM does not know is preserved untouched.">
        <div className="mt-4 space-y-4">
          <ClientPager pager={configuredPager} noun={{ one: 'configured plugin', many: 'configured plugins' }} searchLabel="Filter configured plugins" />
          <div>
            {specs.length === 0 ? (
              <div className={notice.neutral}>
                No plugin is configured. Enable one from the catalog to load it in the broker.
              </div>
            ) : null}

            {configuredPager.rows.map(({ item: plugin, index }) => (
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
      </Section>

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
              Mosquitto loads a plugin at start-up, so this change needs a broker restart.
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
    <div className="border-t border-track py-4 first:border-t-0">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h4 className="text-sm font-semibold text-enamel-bright">
            {known ? spec?.title ?? plugin.id : 'Unknown plugin'}
          </h4>
          {known && spec?.description ? <p className={`mt-1 max-w-[68ch] ${fieldHint}`}>{spec.description}</p> : null}
          {!known ? (
            <p className={`mt-1 max-w-[68ch] ${fieldHint}`}>
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
            className={leverDanger}
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
              className={`w-full font-mono ${input}`}
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
                className={`w-full font-mono ${input}`}
              />
            </Field>
          ))}
        </div>
      ) : (
        <p className="mt-4 break-all font-mono text-xs text-enamel-soft">{plugin.module}</p>
      )}

      {preserved.length > 0 ? (
        <div className="mt-4 border-t border-track pt-3">
          <h5 className="text-xs font-semibold text-enamel">Preserved options</h5>
          <p className={`mt-1 ${fieldHint}`}>MCM does not manage these options and writes them back untouched.</p>
          <ul className="mt-2 space-y-1 font-mono text-xs text-enamel-soft">
            {preserved.map(([name, value]) => (
              <li key={name}>
                <span className="text-enamel-bright">plugin_opt_{name}</span> = {value}
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
      <span className={fieldLabel}>{label}</span>
      <span className="mt-2 block">{children}</span>
      {hint ? <span className={`mt-1 block ${fieldHint}`}>{hint}</span> : null}
    </label>
  )
}
