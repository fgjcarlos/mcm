import { useEffect, useState } from 'react'
import { useBrokerConfig } from './useBrokerConfig'
import type { ValidationIssue } from './types'
const reloadKindColor: Record<string, string> = {
  reload: 'bg-cyan-400/10 text-cyan-200',
  restart: 'bg-amber-400/10 text-amber-200',
  none: 'bg-slate-400/10 text-slate-200',
}

// Map validation issue kinds to a visual severity. Errors block apply;
// warnings and info are advisory. Unknown-directive is treated as info
// because the importer preserves them byte-for-byte (see
// internal/mosquitto/conf/validate.go).
function severityFor(issue: ValidationIssue): 'error' | 'warning' | 'info' {
  if (issue.kind === 'type' || issue.kind === 'dependency') return 'error'
  if (issue.kind === 'scope' || issue.kind === 'multiplicity') return 'warning'
  if (issue.kind === 'since_version' || issue.kind === 'unknown') return 'info'
  return 'warning'
}

const severityClasses = {
  error: 'border-rose-300/30 bg-rose-400/10 text-rose-100',
  warning: 'border-amber-300/30 bg-amber-400/10 text-amber-100',
  info: 'border-cyan-300/20 bg-cyan-400/10 text-cyan-100',
} as const

function statusPill(status: ReturnType<typeof useBrokerConfig>['status']): { label: string; tone: string } {
  switch (status) {
    case 'previewing':
      return { label: 'previewing', tone: 'bg-cyan-400/10 text-cyan-200' }
    case 'adoption_required':
      return { label: 'adoption required', tone: 'bg-amber-400/10 text-amber-200' }
    case 'applying':
      return { label: 'applying', tone: 'bg-violet-400/10 text-violet-200' }
    default:
      return { label: 'active', tone: 'bg-emerald-400/10 text-emerald-200' }
  }
}

export default function BrokerConfigPanel({
  token,
  onLogout,
  role = '',
}: {
  token: string
  onLogout: () => void
  role?: string
}) {
  const { config, preview, catalog, status, loading, error, load, importPreview, adopt, apply, loadCatalog } =
    useBrokerConfig(token, onLogout)
  const isAdmin = role === 'admin'
  const [confirmRestart, setConfirmRestart] = useState(false)
  const [catalogOpen, setCatalogOpen] = useState(false)

  useEffect(() => {
    void load()
    void loadCatalog()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const pill = statusPill(status)

  const handleApply = () => {
    if (!preview) return
    if (preview.requires_restart && !confirmRestart) {
      setConfirmRestart(true)
      return
    }
    setConfirmRestart(false)
    void apply({ revision_id: preview.revision_id, force: preview.requires_restart, adopt: preview.requires_restart })
  }

  if (loading && !config && !preview) {
    return (
      <section className="mt-8">
        <p className="text-sm text-slate-400">Loading broker configuration…</p>
      </section>
    )
  }

  return (
    <section className="mt-8 space-y-6">
      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Broker configuration</p>
            <p className="mt-1 text-sm text-slate-300">
              Import, preview and apply versioned changes to <span className="font-mono">{config?.path ?? 'mosquitto.conf'}</span>.
            </p>
          </div>
          <div className="flex items-center gap-3">
            <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${pill.tone}`}>
              {pill.label}
            </span>
            <button
              type="button"
              onClick={() => {
                void load()
              }}
              disabled={loading}
              className="rounded-xl border border-white/10 px-4 py-2 text-sm font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
            >
              {loading ? 'Reloading…' : 'Reload'}
            </button>
          </div>
        </div>

        {config ? (
          <div className="mt-4 grid gap-2 sm:grid-cols-2">
            <div className="rounded-xl border border-white/10 bg-slate-950/35 px-3 py-2">
              <p className="text-[0.65rem] font-semibold uppercase tracking-[0.18em] text-slate-400">Path</p>
              <p className="mt-1 break-all font-mono text-sm text-white">{config.path}</p>
            </div>
            <div className="rounded-xl border border-white/10 bg-slate-950/35 px-3 py-2">
              <p className="text-[0.65rem] font-semibold uppercase tracking-[0.18em] text-slate-400">Hash</p>
              <p className="mt-1 break-all font-mono text-sm text-white">{config.hash.slice(0, 12)}</p>
            </div>
          </div>
        ) : (
          <div className="mt-4 rounded-2xl border border-dashed border-white/10 bg-white/[0.03] p-4 text-sm text-slate-300">
            Broker config is not configured on this MCM instance.
          </div>
        )}

        {error ? (
          <div className="mt-4 rounded-2xl border border-dashed border-rose-300/30 bg-rose-400/10 p-5 text-sm text-rose-100" role="alert">
            {error}
          </div>
        ) : null}

        <div className="mt-5 flex flex-wrap items-center gap-3">
          <button
            type="button"
            onClick={() => {
              void importPreview()
            }}
            disabled={!config || status === 'previewing'}
            className="rounded-xl border border-white/10 px-4 py-2 text-sm font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
          >
            {status === 'previewing' ? 'Generating…' : 'Import / Refresh preview'}
          </button>
          <button
            type="button"
            onClick={() => {
              void adopt()
            }}
            disabled={!isAdmin || !preview || !preview.requires_restart || status === 'adoption_required'}
            title={!isAdmin ? 'Requires admin role' : undefined}
            className="rounded-xl border border-white/10 px-4 py-2 text-sm font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
          >
            Adopt
          </button>
          {confirmRestart ? (
            <span className="inline-flex items-center gap-2">
              <span className="text-xs text-amber-200">Apply restart-required change?</span>
              <button
                type="button"
                onClick={handleApply}
                disabled={!isAdmin || status === 'applying'}
                className="rounded-xl bg-amber-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-amber-400 disabled:opacity-50"
              >
                {status === 'applying' ? 'Applying…' : 'Force Apply'}
              </button>
              <button
                type="button"
                onClick={() => setConfirmRestart(false)}
                className="rounded-xl border border-white/10 px-4 py-2 text-sm text-slate-300 transition hover:border-white/20 hover:text-white"
              >
                Cancel
              </button>
            </span>
          ) : (
            <button
              type="button"
              onClick={handleApply}
              disabled={!isAdmin || !preview || status === 'applying'}
              title={!isAdmin ? 'Requires admin role' : undefined}
              className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
            >
              {status === 'applying' ? 'Applying…' : 'Apply'}
            </button>
          )}
        </div>

        {preview?.requires_restart && preview.reload_kind ? (
          <p className="mt-3 text-xs text-amber-200">
            This preview requires a broker restart (directives: <span className="font-mono">{preview.reload_kind}</span>). Adoption must be recorded before apply.
          </p>
        ) : null}
      </div>

      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Current body</p>
        <pre className="mt-4 max-h-96 overflow-auto rounded-2xl border border-white/10 bg-slate-950/70 p-4 font-mono text-xs leading-5 text-slate-200">
          {config?.body ?? ''}
        </pre>
      </div>

      {preview ? (
        <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Preview</p>
            <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${reloadKindColor[preview.reload_kind] ?? reloadKindColor.reload}`}>
              {preview.reload_kind || 'reload'}
            </span>
          </div>
          <p className="mt-2 break-all font-mono text-xs text-slate-400">revision {preview.revision_id}</p>
          {preview.conf_diff ? (
            <pre className="mt-4 max-h-64 overflow-auto rounded-2xl border border-white/10 bg-slate-950/70 p-4 font-mono text-xs leading-5">
              {preview.conf_diff.split('\n').map((line, idx) => (
                <span
                  key={idx}
                  className={`block ${line.startsWith('+') ? 'bg-emerald-500/10 text-emerald-200' : line.startsWith('-') ? 'bg-red-500/10 text-red-200' : 'text-slate-300'}`}
                >
                  {line || ' '}
                </span>
              ))}
            </pre>
          ) : (
            <p className="mt-4 text-sm text-slate-300">No changes — configuration is up to date.</p>
          )}

          {preview.validation_issues?.length ? (
            <div className="mt-5 space-y-2">
              <p className="text-xs font-semibold uppercase tracking-[0.18em] text-cyan-300">Validation</p>
              {preview.validation_issues.map((issue, idx) => {
                const sev = severityFor(issue)
                return (
                  <div key={idx} className={`rounded-xl border px-3 py-2 text-sm ${severityClasses[sev]}`}>
                    <p className="font-mono text-xs">
                      line {issue.line}
                      {issue.directive ? ` · ${issue.directive}` : ''} · {issue.kind}
                    </p>
                    <p className="mt-1">{issue.message}</p>
                  </div>
                )
              })}
            </div>
          ) : null}
        </div>
      ) : null}

      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <button
          type="button"
          onClick={() => setCatalogOpen((v) => !v)}
          className="flex w-full items-center justify-between text-left"
        >
          <span className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Directive catalog</span>
          <span className="text-xs text-slate-400">{catalogOpen ? '▾' : '▸'}</span>
        </button>
        {catalogOpen ? (
          <div className="mt-4">
            {!catalog ? (
              <p className="text-sm text-slate-400">Loading catalog…</p>
            ) : catalog.directives.length === 0 ? (
              <p className="text-sm text-slate-400">Catalog is empty.</p>
            ) : (
              <>
                <p className="text-xs text-slate-400">Mosquitto {catalog.version} · {catalog.directives.length} directives</p>
                <ul className="mt-3 max-h-64 divide-y divide-white/5 overflow-auto rounded-xl border border-white/10 bg-slate-950/40">
                  {catalog.directives.map((d) => (
                    <li key={d.name} className="px-3 py-2 text-sm">
                      <div className="flex items-center justify-between gap-3">
                        <span className="font-mono text-cyan-100">{d.name}</span>
                        <span className={`rounded-full px-2 py-0.5 text-xs font-semibold uppercase tracking-[0.18em] ${reloadKindColor[d.reload_kind] ?? reloadKindColor.reload}`}>
                          {d.reload_kind}
                        </span>
                      </div>
                      <p className="mt-1 text-xs text-slate-400">{d.description || '—'}</p>
                    </li>
                  ))}
                </ul>
              </>
            )}
          </div>
        ) : null}
      </div>

    </section>
  )
}
