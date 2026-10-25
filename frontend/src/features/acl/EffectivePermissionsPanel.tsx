import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { isForbiddenResponseError, isUnauthorizedResponseError } from '../api/client'
import { fetchACLScopes, fetchEffectivePermissions, simulateAccess } from './api'
import { PERMISSION_LABELS, SELECTOR_LABELS, type ACLScope, type ACLScopeDescriptor, type EffectiveScope, type SimulationDecision } from './types'

// EffectivePermissionsPanel shows the permissions an identity really has.
//
// The panel resolves the rules the way Mosquitto does — a deny rule is
// handled before a grant, the anonymous section applies to unauthenticated
// clients only, and the pattern rules apply to every client — so the
// operator sees the access a client has instead of the rules MCM stores.
//
// The simulator answers the question an operator asks when a rule looks
// wrong: may this client do this on this topic?
function EffectivePermissionsPanel({ token, onLogout }: { token: string; onLogout: () => void }) {
  const [scopes, setScopes] = useState<ACLScopeDescriptor[]>([])
  const [effective, setEffective] = useState<EffectiveScope[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [refreshTick, setRefreshTick] = useState(0)

  const [simUser, setSimUser] = useState('')
  const [simClientID, setSimClientID] = useState('')
  const [simTopic, setSimTopic] = useState('')
  const [simAccess, setSimAccess] = useState<'read' | 'write'>('read')
  const [simScope, setSimScope] = useState<ACLScope>('global')
  const [simListenerID, setSimListenerID] = useState('')
  const [decision, setDecision] = useState<SimulationDecision | null>(null)
  const [simError, setSimError] = useState('')
  const [simulating, setSimulating] = useState(false)

  const reload = useCallback(() => setRefreshTick((n) => n + 1), [])
  const handleUnauthorized = useCallback(() => onLogout(), [onLogout])

  useEffect(() => {
    let cancelled = false
    Promise.all([fetchACLScopes(token, handleUnauthorized), fetchEffectivePermissions(token, handleUnauthorized)])
      .then(([scopeList, effectiveList]) => {
        if (cancelled) return
        setScopes(scopeList)
        setEffective(effectiveList)
        setError('')
        setLoading(false)
      })
      .catch((err: Error) => {
        if (cancelled) return
        if (isUnauthorizedResponseError(err)) return
        setError(err.message)
        setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [token, refreshTick, handleUnauthorized])

  const handleSimulate = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (simulating) return
    setSimulating(true)
    setSimError('')
    setDecision(null)
    try {
      const result = await simulateAccess(token, handleUnauthorized, {
        scope: simScope,
        listener_id: simScope === 'listener' ? simListenerID.trim() : '',
        username: simUser.trim(),
        client_id: simClientID.trim(),
        topic: simTopic.trim(),
        access: simAccess,
      })
      setDecision(result)
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) {
        setSimError((err as Error).message)
        return
      }
      setSimError((err as Error).message || 'Could not reach the server.')
    } finally {
      setSimulating(false)
    }
  }

  return (
    <section className="mt-8 space-y-6">
      <div>
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Effective MQTT permissions</p>
        <p className="mt-1 text-sm text-slate-300">
          The access every identity really has, resolved with the Mosquitto semantics: a deny rule is handled before a
          grant, the anonymous section applies to unauthenticated clients only, and the pattern rules apply to every
          client.
        </p>
        <p className="mt-1 text-xs text-slate-400">
          These are MQTT permissions. The MCM administrative roles (viewer, auditor, operator, admin) belong to the
          administration panel and are not MQTT permissions.
        </p>
      </div>

      {error ? (
        <div className="rounded-2xl border border-dashed border-amber-300/30 bg-amber-400/10 p-5 text-sm text-amber-100">{error}</div>
      ) : null}

      {loading ? (
        <p className="text-sm text-slate-400">Loading the effective permissions…</p>
      ) : (
        <div className="space-y-4">
          {scopes
            .filter((scope) => !scope.wired)
            .map((scope) => (
              <div
                key={scope.scope === 'global' ? scope.path : `${scope.path}-${scope.listener_id}`}
                className="rounded-2xl border border-amber-300/30 bg-amber-400/10 p-4 text-sm text-amber-100"
              >
                <p className="font-semibold">
                  {scope.scope === 'global' ? 'The broker-wide ACL file is not applied' : `The ACL file of ${scope.listener_id} is not applied`}
                </p>
                <p className="mt-1 text-xs">{scope.wiring_issue}</p>
              </div>
            ))}

          {effective.map((scope) => (
            <div
              key={scope.scope === 'global' ? scope.path : `${scope.path}-${scope.listener_id}`}
              className="rounded-2xl border border-white/10 bg-slate-900/60"
            >
              <div className="border-b border-white/10 px-4 py-3">
                <p className="text-xs font-semibold uppercase tracking-[0.18em] text-cyan-300">
                  {scope.scope === 'global' ? 'Broker-wide ACL file' : `ACL file of ${scope.listener_id}`}
                </p>
                <p className="mt-1 font-mono text-xs text-slate-400">{scope.path}</p>
              </div>
              {scope.identities.length === 0 ? (
                <p className="p-4 text-sm text-slate-400">No identity has a rule in this file.</p>
              ) : (
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-white/10">
                      <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Identity</th>
                      <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Source</th>
                      <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Topic filter</th>
                      <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Permission</th>
                      <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Effective</th>
                    </tr>
                  </thead>
                  <tbody>
                    {scope.identities.flatMap((identity) =>
                      identity.grants.length === 0
                        ? [
                            <tr key={`${scope.path}-${identity.identity}-empty`} className="border-b border-white/5 last:border-0">
                              <td className="px-4 py-3 font-mono text-sm text-slate-200">{identity.identity}</td>
                              <td className="px-4 py-3 text-xs text-slate-400">{identity.kind}</td>
                              <td className="px-4 py-3 text-xs text-slate-500" colSpan={3}>
                                No access.
                              </td>
                            </tr>,
                          ]
                        : identity.grants.map((grant, index) => (
                            <tr
                              key={`${scope.path}-${identity.identity}-${grant.selector}-${grant.topic_filter}-${index}`}
                              className="border-b border-white/5 last:border-0"
                            >
                              <td className="px-4 py-3 font-mono text-sm text-slate-200">{identity.identity}</td>
                              <td className="px-4 py-3 text-xs text-slate-400">{SELECTOR_LABELS[grant.selector]}</td>
                              <td className="px-4 py-3 font-mono text-sm text-cyan-100">{grant.topic_filter}</td>
                              <td className="px-4 py-3">
                                <span
                                  className={`rounded-full px-2.5 py-1 font-mono text-xs ${
                                    grant.permission === 'deny'
                                      ? 'bg-rose-400/10 text-rose-200'
                                      : 'bg-cyan-400/10 text-cyan-200'
                                  }`}
                                >
                                  {PERMISSION_LABELS[grant.permission]}
                                </span>
                              </td>
                              <td className="px-4 py-3 text-xs">
                                {grant.denied ? (
                                  <span className="text-rose-200">
                                    denied by {grant.denied_by?.join(', ')}
                                  </span>
                                ) : (
                                  <span className="text-emerald-200">granted</span>
                                )}
                              </td>
                            </tr>
                          )),
                    )}
                  </tbody>
                </table>
              )}
            </div>
          ))}
        </div>
      )}

      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Access simulator</p>
        <p className="mt-1 text-sm text-slate-300">
          Ask what a client may do on a topic. The answer follows the same rules the broker applies.
        </p>
        <form onSubmit={handleSimulate} className="mt-4 grid gap-4 md:grid-cols-2">
          <label className="block">
            <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Username (empty means anonymous)</span>
            <input
              type="text"
              value={simUser}
              onChange={(e) => setSimUser(e.target.value)}
              placeholder="device-sensor-01"
              className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
            />
          </label>
          <label className="block">
            <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Client id (%c)</span>
            <input
              type="text"
              value={simClientID}
              onChange={(e) => setSimClientID(e.target.value)}
              placeholder="client-1"
              className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
            />
          </label>
          <label className="block">
            <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Topic</span>
            <input
              type="text"
              required
              value={simTopic}
              onChange={(e) => setSimTopic(e.target.value)}
              placeholder="sensors/1/temperature"
              className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
            />
          </label>
          <label className="block">
            <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Action</span>
            <select
              value={simAccess}
              onChange={(e) => setSimAccess(e.target.value as 'read' | 'write')}
              className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
            >
              <option value="read">read (subscribe)</option>
              <option value="write">write (publish)</option>
            </select>
          </label>
          <label className="block">
            <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">ACL file</span>
            <select
              value={simScope}
              onChange={(e) => setSimScope(e.target.value as ACLScope)}
              className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
            >
              <option value="global">broker-wide</option>
              {scopes
                .filter((scope) => scope.scope === 'listener')
                .map((scope) => (
                  <option key={scope.listener_id} value="listener">
                    {scope.listener_id}
                  </option>
                ))}
            </select>
          </label>
          {simScope === 'listener' ? (
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Listener</span>
              <input
                type="text"
                required
                value={simListenerID}
                onChange={(e) => setSimListenerID(e.target.value)}
                placeholder="tls"
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              />
            </label>
          ) : null}
          <div className="md:col-span-2">
            {simError ? (
              <div className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">{simError}</div>
            ) : null}
            {decision ? (
              <div
                className={`rounded-2xl border px-4 py-3 text-sm ${
                  decision.allowed
                    ? 'border-emerald-300/30 bg-emerald-400/10 text-emerald-100'
                    : 'border-rose-300/30 bg-rose-400/10 text-rose-100'
                }`}
              >
                <p className="font-semibold">{decision.allowed ? 'Allowed' : 'Denied'}</p>
                <p className="mt-1 text-xs">{decision.reason}</p>
              </div>
            ) : null}
            <div className="mt-4 flex gap-3">
              <button
                type="submit"
                disabled={simulating}
                className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
              >
                {simulating ? 'Simulating…' : 'Simulate'}
              </button>
              <button
                type="button"
                onClick={reload}
                className="rounded-xl border border-white/10 px-4 py-2 text-sm text-slate-300 transition hover:border-white/20 hover:text-white"
              >
                Refresh
              </button>
            </div>
          </div>
        </form>
      </div>
    </section>
  )
}

export default EffectivePermissionsPanel
