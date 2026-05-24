import { useEffect, useState } from 'react'
import { getSettings } from '../lib/api'

type Props = {
  token: string
}

export function SettingsPage({ token }: Props) {
  const [config, setConfig] = useState<unknown>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    getSettings(token)
      .then((data) => {
        if (cancelled) return
        setConfig(data)
        setLoading(false)
      })
      .catch((err: Error) => {
        if (cancelled) return
        setError(err.message)
        setLoading(false)
      })
    return () => { cancelled = true }
  }, [token])

  return (
    <section className="mt-8 space-y-6">
      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <div className="flex items-center justify-between gap-4">
          <div>
            <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Platform configuration</p>
            <p className="mt-1 text-sm text-slate-300">Global defaults and broker-level configuration.</p>
          </div>
          <span className="rounded-full bg-cyan-400/10 px-3 py-1 text-xs font-semibold uppercase tracking-[0.18em] text-cyan-200">Read only</span>
        </div>

        {error ? (
          <div className="mt-5 rounded-2xl border border-dashed border-amber-300/30 bg-amber-400/10 p-5 text-sm text-amber-100">{error}</div>
        ) : loading ? (
          <p className="mt-5 text-sm text-slate-400">Loading settings…</p>
        ) : config === null ? (
          <p className="mt-5 text-sm text-slate-400">No settings returned from the server.</p>
        ) : (
          <div className="mt-5">
            {typeof config === 'object' && config !== null && !Array.isArray(config) ? (
              <dl className="space-y-3">
                {Object.entries(config as Record<string, unknown>).map(([key, value]) => (
                  <div key={key} className="rounded-2xl border border-white/10 bg-white/[0.03] p-4">
                    <dt className="text-xs font-semibold uppercase tracking-[0.18em] text-cyan-300">{key}</dt>
                    <dd className="mt-2 break-all font-mono text-sm text-slate-200">
                      {typeof value === 'object' ? (
                        <pre className="max-h-48 overflow-auto rounded-xl bg-slate-950/60 p-3 text-xs">{JSON.stringify(value, null, 2)}</pre>
                      ) : (
                        String(value)
                      )}
                    </dd>
                  </div>
                ))}
              </dl>
            ) : (
              <pre className="max-h-96 overflow-auto rounded-2xl border border-white/10 bg-slate-950/60 p-4 text-sm text-slate-200">{JSON.stringify(config, null, 2)}</pre>
            )}
          </div>
        )}
      </div>
    </section>
  )
}
