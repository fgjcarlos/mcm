import { useCallback, useEffect, useState, type FormEvent } from 'react'
import type { MQTTUser } from '../types'
import { createMQTTUser, deleteMQTTUser, listMQTTUsers, resetPassword, updateMQTTUser } from '../lib/api'

type Props = {
  token: string
}

export function UsersPage({ token }: Props) {
  const [users, setUsers] = useState<MQTTUser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [showCreateForm, setShowCreateForm] = useState(false)
  const [createUsername, setCreateUsername] = useState('')
  const [createError, setCreateError] = useState('')
  const [createSubmitting, setCreateSubmitting] = useState(false)
  const [createdPassword, setCreatedPassword] = useState<{ userId: number; password: string } | null>(null)
  const [resetUserId, setResetUserId] = useState<number | null>(null)
  const [resetPasswordValue, setResetPasswordValue] = useState<string | null>(null)
  const [deleteConfirmId, setDeleteConfirmId] = useState<number | null>(null)
  const [togglingId, setTogglingId] = useState<number | null>(null)
  const [refreshTick, setRefreshTick] = useState(0)

  const fetchUsers = useCallback(() => { setRefreshTick((n) => n + 1) }, [])

  useEffect(() => {
    let cancelled = false
    listMQTTUsers(token)
      .then((data) => {
        if (cancelled) return
        setUsers(data ?? [])
        setError('')
        setLoading(false)
      })
      .catch((err: Error) => {
        if (cancelled) return
        setError(err.message)
        setLoading(false)
      })
    return () => { cancelled = true }
  }, [token, refreshTick])

  const handleCreate = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (createSubmitting) return
    setCreateSubmitting(true)
    setCreateError('')
    try {
      const created = await createMQTTUser(token, createUsername.trim())
      setCreatedPassword({ userId: created.id, password: created.password })
      setCreateUsername('')
      setShowCreateForm(false)
      fetchUsers()
    } catch (err) {
      setCreateError((err as Error).message ?? 'Create failed.')
    } finally {
      setCreateSubmitting(false)
    }
  }

  const handleToggle = async (user: MQTTUser) => {
    setTogglingId(user.id)
    try {
      await updateMQTTUser(token, user.id, { disabled: !user.disabled })
      fetchUsers()
    } catch (err) {
      setError((err as Error).message ?? 'Update failed.')
    } finally {
      setTogglingId(null)
    }
  }

  const handleResetPassword = async (userId: number) => {
    try {
      const result = await resetPassword(token, userId)
      setResetPasswordValue(result.password)
      setResetUserId(userId)
    } catch (err) {
      setError((err as Error).message ?? 'Reset failed.')
    }
  }

  const handleDelete = async (id: number) => {
    try {
      await deleteMQTTUser(token, id)
      setDeleteConfirmId(null)
      if (createdPassword?.userId === id) setCreatedPassword(null)
      if (resetUserId === id) { setResetUserId(null); setResetPasswordValue(null) }
      fetchUsers()
    } catch (err) {
      setError((err as Error).message ?? 'Delete failed.')
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
          onClick={() => { setShowCreateForm(true); setCreateError(''); setCreateUsername('') }}
          className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400"
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

      {resetUserId !== null && resetPasswordValue !== null ? (
        <div className="rounded-2xl border border-emerald-300/30 bg-emerald-400/10 p-5">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-emerald-200">Password reset — save this password</p>
          <p className="mt-2 text-sm text-slate-300">This password will not be shown again.</p>
          <pre className="mt-3 rounded-xl bg-slate-950/60 px-4 py-3 font-mono text-sm text-emerald-100">{resetPasswordValue}</pre>
          <button
            type="button"
            onClick={() => { setResetUserId(null); setResetPasswordValue(null) }}
            className="mt-3 rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white"
          >
            Dismiss
          </button>
        </div>
      ) : null}

      {showCreateForm ? (
        <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">New MQTT user</p>
          <form onSubmit={(e) => { void handleCreate(e) }} className="mt-4 space-y-4">
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
                disabled={createSubmitting}
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
                          onClick={() => { void handleDelete(user.id) }}
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
                          onClick={() => { void handleToggle(user) }}
                          disabled={togglingId === user.id}
                          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white disabled:opacity-50"
                        >
                          {togglingId === user.id ? '…' : user.disabled ? 'Enable' : 'Disable'}
                        </button>
                        <button
                          type="button"
                          onClick={() => { void handleResetPassword(user.id) }}
                          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white"
                        >
                          Reset password
                        </button>
                        <button
                          type="button"
                          onClick={() => setDeleteConfirmId(user.id)}
                          className="rounded-xl bg-red-500/20 px-3 py-1.5 text-xs text-red-300 transition hover:bg-red-500/30"
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
