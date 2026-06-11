import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { authenticatedFetch, isForbiddenResponseError, isUnauthorizedResponseError } from '../api/client'
import { can, permissionTitle } from '../auth/permissions'

type MQTTUser = {
  id: number
  username: string
  disabled: boolean
  created_at: string
  updated_at: string
}

function MQTTUsersPanel({ token, onLogout, role = '' }: { token: string; onLogout: () => void; role?: string }) {
  const canWrite = can(role, 'mqttUser.write')
  const [users, setUsers] = useState<MQTTUser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [showCreateForm, setShowCreateForm] = useState(false)
  const [createUsername, setCreateUsername] = useState('')
  const [createError, setCreateError] = useState('')
  const [createSubmitting, setCreateSubmitting] = useState(false)
  const [createdPassword, setCreatedPassword] = useState<{ userId: number; password: string } | null>(null)
  const [resetUserId, setResetUserId] = useState<number | null>(null)
  const [resetPassword, setResetPassword] = useState<string | null>(null)
  const [deleteConfirmId, setDeleteConfirmId] = useState<number | null>(null)
  const [togglingId, setTogglingId] = useState<number | null>(null)
  const [refreshTick, setRefreshTick] = useState(0)

  const fetchUsers = useCallback(() => {
    setRefreshTick((n) => n + 1)
  }, [])

  useEffect(() => {
    let cancelled = false
    authenticatedFetch('/api/v1/mqtt-users', { token, onUnauthorized: onLogout })
      .then(async (response) => {
        if (!response.ok) throw new Error('Failed to load MQTT users.')
        return response.json() as Promise<MQTTUser[]>
      })
      .then((data) => {
        if (cancelled) return
        setUsers(data ?? [])
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
  }, [token, refreshTick, onLogout])

  const handleCreate = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (createSubmitting) return
    setCreateSubmitting(true)
    setCreateError('')
    try {
      const response = await authenticatedFetch('/api/v1/mqtt-users', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: createUsername.trim() }),
      })
      if (!response.ok) {
        const errBody = (await response.json().catch(() => null)) as { error?: string } | null
        setCreateError(errBody?.error ?? 'Create failed.')
        return
      }
      const created = (await response.json()) as MQTTUser & { password: string }
      setCreatedPassword({ userId: created.id, password: created.password })
      setCreateUsername('')
      setShowCreateForm(false)
      fetchUsers()
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) { setCreateError((err as Error).message); return }
      setCreateError('Could not reach the server.')
    } finally {
      setCreateSubmitting(false)
    }
  }

  const handleToggle = async (user: MQTTUser) => {
    setTogglingId(user.id)
    try {
      const response = await authenticatedFetch(`/api/v1/mqtt-users/${user.id}`, {
        token,
        onUnauthorized: onLogout,
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ disabled: !user.disabled }),
      })
      if (!response.ok) {
        const errBody = (await response.json().catch(() => null)) as { error?: string } | null
        setError(errBody?.error ?? 'Update failed.')
        return
      }
      fetchUsers()
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) { setError((err as Error).message); return }
      setError('Could not reach the server.')
    } finally {
      setTogglingId(null)
    }
  }

  const handleResetPassword = async (userId: number) => {
    try {
      const response = await authenticatedFetch(`/api/v1/mqtt-users/${userId}/reset-password`, {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
      })
      if (!response.ok) {
        const errBody = (await response.json().catch(() => null)) as { error?: string } | null
        setError(errBody?.error ?? 'Reset failed.')
        return
      }
      const result = (await response.json()) as MQTTUser & { password: string }
      setResetPassword(result.password)
      setResetUserId(userId)
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) { setError((err as Error).message); return }
      setError('Could not reach the server.')
    }
  }

  const handleDelete = async (id: number) => {
    try {
      const response = await authenticatedFetch(`/api/v1/mqtt-users/${id}`, {
        token,
        onUnauthorized: onLogout,
        method: 'DELETE',
      })
      if (!response.ok && response.status !== 204) {
        const errBody = (await response.json().catch(() => null)) as { error?: string } | null
        setError(errBody?.error ?? 'Delete failed.')
        return
      }
      setDeleteConfirmId(null)
      if (createdPassword?.userId === id) setCreatedPassword(null)
      if (resetUserId === id) {
        setResetUserId(null)
        setResetPassword(null)
      }
      fetchUsers()
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) { setError((err as Error).message); return }
      setError('Could not reach the server.')
    }
  }

  return (
    <section className="mt-8 space-y-6">
      <div className="flex items-center justify-between gap-4">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">MQTT users</p>
          <p className="mt-1 text-sm text-slate-300">{users.length} user{users.length !== 1 ? 's' : ''} configured</p>
        </div>
        <button
          type="button"
          onClick={() => {
            setShowCreateForm(true)
            setCreateError('')
            setCreateUsername('')
          }}
          disabled={!canWrite}
          title={!canWrite ? permissionTitle('mqttUser.write') : undefined}
          className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
        >
          Add User
        </button>
      </div>

      {error ? (
        <div className="rounded-2xl border border-dashed border-amber-300/30 bg-amber-400/10 p-5 text-sm text-amber-100">{error}</div>
      ) : null}

      {createdPassword ? (
        <div className="rounded-2xl border border-emerald-300/30 bg-emerald-400/10 p-5">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-emerald-200">User created — save this password</p>
          <p className="mt-2 text-sm text-slate-300">This password will not be shown again.</p>
          <pre className="mt-3 rounded-xl bg-slate-950/60 px-4 py-3 font-mono text-sm text-emerald-100">{createdPassword.password}</pre>
          <button
            type="button"
            onClick={() => setCreatedPassword(null)}
            className="mt-3 rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white"
          >
            Dismiss
          </button>
        </div>
      ) : null}

      {resetUserId !== null && resetPassword !== null ? (
        <div className="rounded-2xl border border-emerald-300/30 bg-emerald-400/10 p-5">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-emerald-200">Password reset — save this password</p>
          <p className="mt-2 text-sm text-slate-300">This password will not be shown again.</p>
          <pre className="mt-3 rounded-xl bg-slate-950/60 px-4 py-3 font-mono text-sm text-emerald-100">{resetPassword}</pre>
          <button
            type="button"
            onClick={() => {
              setResetUserId(null)
              setResetPassword(null)
            }}
            className="mt-3 rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white"
          >
            Dismiss
          </button>
        </div>
      ) : null}

      {showCreateForm ? (
        <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">New MQTT user</p>
          <form onSubmit={handleCreate} className="mt-4 space-y-4">
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Username</span>
              <input
                type="text"
                required
                value={createUsername}
                onChange={(e) => setCreateUsername(e.target.value)}
                placeholder="device-sensor-01"
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              />
              <span className="mt-1 block text-xs text-slate-400">A secure password will be generated automatically.</span>
            </label>
            {createError ? (
              <div className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">{createError}</div>
            ) : null}
            <div className="flex gap-3">
              <button
                type="submit"
                disabled={createSubmitting || !canWrite}
                title={!canWrite ? permissionTitle('mqttUser.write') : undefined}
                className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
              >
                {createSubmitting ? 'Creating…' : 'Create user'}
              </button>
              <button
                type="button"
                onClick={() => setShowCreateForm(false)}
                className="rounded-xl border border-white/10 px-4 py-2 text-sm text-slate-300 transition hover:border-white/20 hover:text-white"
              >
                Cancel
              </button>
            </div>
          </form>
        </div>
      ) : null}

      <div className="rounded-2xl border border-white/10 bg-slate-900/60">
        {loading ? (
          <p className="p-6 text-sm text-slate-400">Loading users…</p>
        ) : users.length === 0 ? (
          <p className="p-6 text-sm text-slate-400">No MQTT users configured yet.</p>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10">
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Username</th>
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Status</th>
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Created</th>
                <th className="px-4 py-3 text-right text-xs uppercase tracking-[0.18em] text-slate-400">Actions</th>
              </tr>
            </thead>
            <tbody>
              {users.map((user) => (
                <tr key={user.id} className="border-b border-white/5 last:border-0">
                  <td className="px-4 py-3 font-mono text-sm text-slate-200">{user.username}</td>
                  <td className="px-4 py-3">
                    <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${user.disabled ? 'bg-rose-400/10 text-rose-200' : 'bg-emerald-400/10 text-emerald-200'}`}>
                      {user.disabled ? 'disabled' : 'enabled'}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-xs text-slate-400">{new Date(user.created_at).toLocaleDateString()}</td>
                  <td className="px-4 py-3 text-right">
                    {deleteConfirmId === user.id ? (
                      <span className="inline-flex items-center gap-2">
                        <span className="text-xs text-slate-300">Delete?</span>
                        <button
                          type="button"
                          onClick={() => {
                            void handleDelete(user.id)
                          }}
                          className="rounded-xl bg-red-500/20 px-3 py-1.5 text-xs text-red-300 transition hover:bg-red-500/30"
                        >
                          Confirm
                        </button>
                        <button
                          type="button"
                          onClick={() => setDeleteConfirmId(null)}
                          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20"
                        >
                          Cancel
                        </button>
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-2">
                        <button
                          type="button"
                          onClick={() => {
                            void handleToggle(user)
                          }}
                          disabled={togglingId === user.id || !canWrite}
                          title={!canWrite ? permissionTitle('mqttUser.write') : undefined}
                          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white disabled:opacity-50"
                        >
                          {togglingId === user.id ? '…' : user.disabled ? 'Enable' : 'Disable'}
                        </button>
                        <button
                          type="button"
                          onClick={() => {
                            void handleResetPassword(user.id)
                          }}
                          disabled={!canWrite}
                          title={!canWrite ? permissionTitle('mqttUser.write') : undefined}
                          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white disabled:opacity-50"
                        >
                          Reset password
                        </button>
                        <button
                          type="button"
                          onClick={() => setDeleteConfirmId(user.id)}
                          disabled={!canWrite}
                          title={!canWrite ? permissionTitle('mqttUser.write') : undefined}
                          className="rounded-xl bg-red-500/20 px-3 py-1.5 text-xs text-red-300 transition hover:bg-red-500/30 disabled:opacity-50"
                        >
                          Delete
                        </button>
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </section>
  )
}

export default MQTTUsersPanel
