import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { authenticatedFetch, isForbiddenResponseError, isUnauthorizedResponseError } from '../api/client'
import { can, permissionTitle } from '../auth/permissions'

type AdminUser = {
  id: number
  username: string
  disabled: boolean
  role: string
  mfa_enabled?: boolean
  created_at: string
  updated_at: string
}

type RoleOption = 'viewer' | 'auditor' | 'operator' | 'admin'
const ROLES: RoleOption[] = ['viewer', 'auditor', 'operator', 'admin']

const ROLE_COLORS: Record<string, string> = {
  admin:    'bg-violet-400/10 text-violet-200',
  operator: 'bg-cyan-400/10 text-cyan-200',
  auditor:  'bg-amber-400/10 text-amber-200',
  viewer:   'bg-slate-400/10 text-slate-300',
}

function roleBadge(role: string) {
  const cls = ROLE_COLORS[role] ?? 'bg-slate-400/10 text-slate-300'
  return (
    <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${cls}`}>
      {role}
    </span>
  )
}

function AdminUsersPanel({ token, onLogout, role = '' }: { token: string; onLogout: () => void; role?: string }) {
  const canWrite = can(role, 'adminUser.write')

  const [users, setUsers]           = useState<AdminUser[]>([])
  const [loading, setLoading]       = useState(true)
  const [error, setError]           = useState('')
  const [refreshTick, setRefreshTick] = useState(0)

  // Create form
  const [showCreate, setShowCreate]     = useState(false)
  const [createUsername, setCreateUsername] = useState('')
  const [createRole, setCreateRole]         = useState<RoleOption>('operator')
  const [createError, setCreateError]       = useState('')
  const [createSubmitting, setCreateSubmitting] = useState(false)
  const [createdPassword, setCreatedPassword] = useState<{ userId: number; password: string } | null>(null)

  // Edit role
  const [editUserId, setEditUserId]   = useState<number | null>(null)
  const [editRole, setEditRole]       = useState<RoleOption>('operator')
  const [editError, setEditError]     = useState('')
  const [editSubmitting, setEditSubmitting] = useState(false)

  // Reset password
  const [resetUserId, setResetUserId]   = useState<number | null>(null)
  const [resetPassword, setResetPassword] = useState<string | null>(null)

  // Delete confirmation
  const [deleteConfirmId, setDeleteConfirmId] = useState<number | null>(null)

  const fetchUsers = useCallback(() => {
    setLoading(true)
    setRefreshTick((n) => n + 1)
  }, [])

  useEffect(() => {
    let cancelled = false
    authenticatedFetch('/api/v1/admin-users', { token, onUnauthorized: onLogout })
      .then((res) => {
        if (!res.ok) throw new Error('Failed to load admin users.')
        return res.json() as Promise<AdminUser[]>
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
    return () => { cancelled = true }
  }, [token, refreshTick, onLogout])

  const handleCreate = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (createSubmitting) return
    setCreateSubmitting(true)
    setCreateError('')
    try {
      const res = await authenticatedFetch('/api/v1/admin-users', {
        token, onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: createUsername.trim(), role: createRole }),
      })
      if (!res.ok) {
        const errBody = (await res.json().catch(() => null)) as { error?: string } | null
        setCreateError(errBody?.error ?? 'Create failed.')
        return
      }
      const created = (await res.json()) as AdminUser & { password: string }
      setCreatedPassword({ userId: created.id, password: created.password })
      setCreateUsername('')
      setCreateRole('operator')
      setShowCreate(false)
      fetchUsers()
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) { setCreateError((err as Error).message); return }
      setCreateError('Could not reach the server.')
    } finally {
      setCreateSubmitting(false)
    }
  }

  const openEdit = (user: AdminUser) => {
    setEditUserId(user.id)
    setEditRole((ROLES.includes(user.role as RoleOption) ? user.role : 'operator') as RoleOption)
    setEditError('')
  }

  const handleEditRole = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (editUserId === null || editSubmitting) return
    setEditSubmitting(true)
    setEditError('')
    try {
      const target = users.find((u) => u.id === editUserId)
      if (!target) { setEditError('User not found.'); return }
      const res = await authenticatedFetch(`/api/v1/admin-users/${editUserId}`, {
        token, onUnauthorized: onLogout,
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: target.username, role: editRole, disabled: target.disabled }),
      })
      if (!res.ok) {
        const errBody = (await res.json().catch(() => null)) as { error?: string } | null
        setEditError(errBody?.error ?? 'Update failed.')
        return
      }
      setEditUserId(null)
      fetchUsers()
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) { setEditError((err as Error).message); return }
      setEditError('Could not reach the server.')
    } finally {
      setEditSubmitting(false)
    }
  }

  const handleToggle = async (user: AdminUser) => {
    try {
      const res = await authenticatedFetch(`/api/v1/admin-users/${user.id}`, {
        token, onUnauthorized: onLogout,
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: user.username, role: user.role, disabled: !user.disabled }),
      })
      if (!res.ok) {
        const errBody = (await res.json().catch(() => null)) as { error?: string } | null
        setError(errBody?.error ?? 'Update failed.')
        return
      }
      fetchUsers()
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) { setError((err as Error).message); return }
      setError('Could not reach the server.')
    }
  }

  const handleResetPassword = async (userId: number) => {
    try {
      const res = await authenticatedFetch(`/api/v1/admin-users/${userId}/reset-password`, {
        token, onUnauthorized: onLogout, method: 'POST',
      })
      if (!res.ok) {
        const errBody = (await res.json().catch(() => null)) as { error?: string } | null
        setError(errBody?.error ?? 'Reset failed.')
        return
      }
      const result = (await res.json()) as AdminUser & { password: string }
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
      const res = await authenticatedFetch(`/api/v1/admin-users/${id}`, {
        token, onUnauthorized: onLogout, method: 'DELETE',
      })
      if (!res.ok && res.status !== 204) {
        const errBody = (await res.json().catch(() => null)) as { error?: string } | null
        setError(errBody?.error ?? 'Delete failed.')
        return
      }
      setDeleteConfirmId(null)
      if (createdPassword?.userId === id) setCreatedPassword(null)
      if (resetUserId === id) { setResetUserId(null); setResetPassword(null) }
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
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-violet-300">Admin users</p>
          <p className="mt-1 text-sm text-slate-300">{users.length} operator{users.length !== 1 ? 's' : ''} configured</p>
        </div>
        <button
          type="button"
          onClick={() => { setShowCreate(true); setCreateError(''); setCreateUsername(''); setCreateRole('operator') }}
          disabled={!canWrite}
          title={!canWrite ? permissionTitle('adminUser.write') : undefined}
          className="rounded-xl bg-violet-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-violet-400 disabled:opacity-50"
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
          <button type="button" onClick={() => setCreatedPassword(null)}
            className="mt-3 rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white">
            Dismiss
          </button>
        </div>
      ) : null}

      {resetUserId !== null && resetPassword !== null ? (
        <div className="rounded-2xl border border-emerald-300/30 bg-emerald-400/10 p-5">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-emerald-200">Password reset — save this password</p>
          <p className="mt-2 text-sm text-slate-300">This password will not be shown again.</p>
          <pre className="mt-3 rounded-xl bg-slate-950/60 px-4 py-3 font-mono text-sm text-emerald-100">{resetPassword}</pre>
          <button type="button" onClick={() => { setResetUserId(null); setResetPassword(null) }}
            className="mt-3 rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white">
            Dismiss
          </button>
        </div>
      ) : null}

      {showCreate ? (
        <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-violet-300">New admin user</p>
          <form onSubmit={(e) => { void handleCreate(e) }} className="mt-4 space-y-4">
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-violet-300">Username</span>
              <input
                type="text" required
                value={createUsername} onChange={(e) => setCreateUsername(e.target.value)}
                placeholder="alice"
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-violet-300/60 focus:ring-2 focus:ring-violet-300/30"
              />
              <span className="mt-1 block text-xs text-slate-400">A secure password will be generated automatically.</span>
            </label>
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-violet-300">Role</span>
              <select
                value={createRole} onChange={(e) => setCreateRole(e.target.value as RoleOption)}
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-violet-300/60 focus:ring-2 focus:ring-violet-300/30"
              >
                {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
              </select>
            </label>
            {createError ? (
              <div className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">{createError}</div>
            ) : null}
            <div className="flex gap-3">
              <button type="submit" disabled={createSubmitting}
                className="rounded-xl bg-violet-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-violet-400 disabled:opacity-50">
                {createSubmitting ? 'Creating…' : 'Create user'}
              </button>
              <button type="button" onClick={() => setShowCreate(false)}
                className="rounded-xl border border-white/10 px-4 py-2 text-sm text-slate-300 transition hover:border-white/20 hover:text-white">
                Cancel
              </button>
            </div>
          </form>
        </div>
      ) : null}

      {editUserId !== null ? (
        <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-violet-300">Edit role</p>
          <form onSubmit={(e) => { void handleEditRole(e) }} className="mt-4 space-y-4">
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-violet-300">Role</span>
              <select
                value={editRole} onChange={(e) => setEditRole(e.target.value as RoleOption)}
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-violet-300/60 focus:ring-2 focus:ring-violet-300/30"
              >
                {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
              </select>
            </label>
            {editError ? (
              <div className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">{editError}</div>
            ) : null}
            <div className="flex gap-3">
              <button type="submit" disabled={editSubmitting}
                className="rounded-xl bg-violet-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-violet-400 disabled:opacity-50">
                {editSubmitting ? 'Saving…' : 'Save role'}
              </button>
              <button type="button" onClick={() => setEditUserId(null)}
                className="rounded-xl border border-white/10 px-4 py-2 text-sm text-slate-300 transition hover:border-white/20 hover:text-white">
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
          <p className="p-6 text-sm text-slate-400">No admin users found.</p>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10">
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Username</th>
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Role</th>
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Status</th>
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">MFA</th>
                <th className="px-4 py-3 text-right text-xs uppercase tracking-[0.18em] text-slate-400">Actions</th>
              </tr>
            </thead>
            <tbody>
              {users.map((user) => (
                <tr key={user.id} className="border-b border-white/5 last:border-0">
                  <td className="px-4 py-3 font-mono text-sm text-slate-200">{user.username}</td>
                  <td className="px-4 py-3">{roleBadge(user.role)}</td>
                  <td className="px-4 py-3">
                    <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${user.disabled ? 'bg-rose-400/10 text-rose-200' : 'bg-emerald-400/10 text-emerald-200'}`}>
                      {user.disabled ? 'disabled' : 'active'}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    <span className={`text-xs ${user.mfa_enabled ? 'text-emerald-300' : 'text-slate-500'}`}>
                      {user.mfa_enabled ? '✓ on' : '—'}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-right">
                    {deleteConfirmId === user.id ? (
                      <span className="inline-flex items-center gap-2">
                        <span className="text-xs text-slate-300">Delete?</span>
                        <button type="button" onClick={() => { void handleDelete(user.id) }}
                          className="rounded-xl bg-red-500/20 px-3 py-1.5 text-xs text-red-300 transition hover:bg-red-500/30">
                          Confirm
                        </button>
                        <button type="button" onClick={() => setDeleteConfirmId(null)}
                          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20">
                          Cancel
                        </button>
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-2">
                        <button type="button" onClick={() => openEdit(user)}
                          disabled={!canWrite}
                          title={!canWrite ? permissionTitle('adminUser.write') : undefined}
                          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white disabled:opacity-50">
                          Edit role
                        </button>
                        <button type="button" onClick={() => { void handleToggle(user) }}
                          disabled={!canWrite}
                          title={!canWrite ? permissionTitle('adminUser.write') : undefined}
                          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white disabled:opacity-50">
                          {user.disabled ? 'Enable' : 'Disable'}
                        </button>
                        <button type="button" onClick={() => { void handleResetPassword(user.id) }}
                          disabled={!canWrite}
                          title={!canWrite ? permissionTitle('adminUser.write') : undefined}
                          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white disabled:opacity-50">
                          Reset password
                        </button>
                        <button type="button" onClick={() => setDeleteConfirmId(user.id)}
                          disabled={!canWrite}
                          title={!canWrite ? permissionTitle('adminUser.write') : undefined}
                          className="rounded-xl bg-red-500/20 px-3 py-1.5 text-xs text-red-300 transition hover:bg-red-500/30 disabled:opacity-50">
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

export default AdminUsersPanel
