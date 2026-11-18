import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { authenticatedFetch, isForbiddenResponseError, isUnauthorizedResponseError } from '../api/client'
import { can, permissionTitle } from '../auth/permissions'
import { Lamp, Plate } from '../ui/primitives'
import { bodyText, codeBlock, fieldHint, fieldLabel, leverDanger, leverPrimary, leverSecondary, input, notice, select, tableHead, tableRow } from '../ui/styles'

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

  const fetchUsers = useCallback(() => setRefreshTick((n) => n + 1), [])

  useEffect(() => {
    let cancelled = false
    authenticatedFetch('/api/v1/admin-users', { token, onUnauthorized: onLogout })
      .then(async (res) => {
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
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h3 className="text-lg font-semibold text-enamel-bright">Admin users</h3>
          <p className={`mt-1 ${bodyText}`}>{users.length} operator{users.length !== 1 ? 's' : ''} configured</p>
        </div>
        <button
          type="button"
          onClick={() => { setShowCreate(true); setCreateError(''); setCreateUsername(''); setCreateRole('operator') }}
          disabled={!canWrite}
          title={!canWrite ? permissionTitle('adminUser.write') : undefined}
          className={leverSecondary}
        >
          Add User
        </button>
      </div>

      {error ? (
        <div className={notice.failed}>{error}</div>
      ) : null}

      {createdPassword ? (
        <PasswordNotice title="User created — save this password" password={createdPassword.password} onDismiss={() => setCreatedPassword(null)} />
      ) : null}

      {resetUserId !== null && resetPassword !== null ? (
        <PasswordNotice title="Password reset — save this password" password={resetPassword} onDismiss={() => { setResetUserId(null); setResetPassword(null) }} />
      ) : null}

      {showCreate ? (
        <div className="max-w-2xl border-y border-track-strong py-6">
          <h4 className="text-base font-semibold text-enamel-bright">New admin user</h4>
          <form onSubmit={(e) => { void handleCreate(e) }} className="mt-4 space-y-5">
            <label className="block">
              <span className={fieldLabel}>Username</span>
              <input
                type="text" required
                value={createUsername} onChange={(e) => setCreateUsername(e.target.value)}
                placeholder="alice"
                className={`mt-2 w-full font-mono ${input}`}
              />
              <span className={`mt-1 block ${fieldHint}`}>A secure password will be generated automatically.</span>
            </label>
            <label className="block">
              <span className={fieldLabel}>Role</span>
              <select
                value={createRole} onChange={(e) => setCreateRole(e.target.value as RoleOption)}
                className={`mt-2 w-full ${select}`}
              >
                {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
              </select>
            </label>
            {createError ? (
              <div className={notice.failed}>{createError}</div>
            ) : null}
            <div className="flex flex-wrap items-center gap-3">
              <button type="submit" disabled={createSubmitting} className={leverPrimary}>
                {createSubmitting ? 'Creating…' : 'Create user'}
              </button>
              <button type="button" onClick={() => setShowCreate(false)} className={leverSecondary}>
                Cancel
              </button>
            </div>
          </form>
        </div>
      ) : null}

      {editUserId !== null ? (
        <div className="max-w-2xl border-y border-track-strong py-6">
          <h4 className="text-base font-semibold text-enamel-bright">Edit role</h4>
          <form onSubmit={(e) => { void handleEditRole(e) }} className="mt-4 space-y-5">
            <label className="block">
              <span className={fieldLabel}>Role</span>
              <select
                value={editRole} onChange={(e) => setEditRole(e.target.value as RoleOption)}
                className={`mt-2 w-full ${select}`}
              >
                {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
              </select>
            </label>
            {editError ? (
              <div className={notice.failed}>{editError}</div>
            ) : null}
            <div className="flex flex-wrap items-center gap-3">
              <button type="submit" disabled={editSubmitting} className={leverPrimary}>
                {editSubmitting ? 'Saving…' : 'Save role'}
              </button>
              <button type="button" onClick={() => setEditUserId(null)} className={leverSecondary}>
                Cancel
              </button>
            </div>
          </form>
        </div>
      ) : null}

      <div className="table-scroll border-y border-track-strong">
        {loading ? (
          <p className={`py-6 ${bodyText}`}>Loading users…</p>
        ) : users.length === 0 ? (
          <p className={`py-6 ${bodyText}`}>No admin users found.</p>
        ) : (
          <table className="w-full min-w-[48rem] text-sm">
            <thead>
              <tr className="border-b border-track-strong">
                <th className={`${tableHead} text-left`}>Username</th>
                <th className={`${tableHead} text-left`}>Role</th>
                <th className={`${tableHead} text-left`}>Status</th>
                <th className={`${tableHead} text-left`}>MFA</th>
                <th className={`${tableHead} pr-0 text-right`}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {users.map((user) => (
                <tr key={user.id} className={tableRow}>
                  <td className="py-3 pr-4 font-mono text-sm text-enamel-bright">{user.username}</td>
                  <td className="py-3 pr-4"><Plate>{user.role}</Plate></td>
                  <td className="py-3 pr-4">
                    <Lamp tone={user.disabled ? 'off' : 'neutral'}>{user.disabled ? 'disabled' : 'active'}</Lamp>
                  </td>
                  <td className="py-3 pr-4">
                    <span className={`font-mono text-xs ${user.mfa_enabled ? 'text-enamel-bright' : 'text-enamel-muted'}`}>
                      {user.mfa_enabled ? '✓ on' : '—'}
                    </span>
                  </td>
                  <td className="py-3 text-right">
                    {deleteConfirmId === user.id ? (
                      <span className="inline-flex items-center gap-2">
                        <span className="text-xs text-enamel-soft">Delete?</span>
                        <button type="button" onClick={() => { void handleDelete(user.id) }} className={leverDanger}>
                          Confirm
                        </button>
                        <button type="button" onClick={() => setDeleteConfirmId(null)} className={leverSecondary}>
                          Cancel
                        </button>
                      </span>
                    ) : (
                      <span className="inline-flex flex-wrap items-center justify-end gap-2">
                        <button type="button" onClick={() => openEdit(user)}
                          disabled={!canWrite}
                          title={!canWrite ? permissionTitle('adminUser.write') : undefined}
                          className={leverSecondary}>
                          Edit role
                        </button>
                        <button type="button" onClick={() => { void handleToggle(user) }}
                          disabled={!canWrite}
                          title={!canWrite ? permissionTitle('adminUser.write') : undefined}
                          className={leverSecondary}>
                          {user.disabled ? 'Enable' : 'Disable'}
                        </button>
                        <button type="button" onClick={() => { void handleResetPassword(user.id) }}
                          disabled={!canWrite}
                          title={!canWrite ? permissionTitle('adminUser.write') : undefined}
                          className={leverSecondary}>
                          Reset password
                        </button>
                        <button type="button" onClick={() => setDeleteConfirmId(user.id)}
                          disabled={!canWrite}
                          title={!canWrite ? permissionTitle('adminUser.write') : undefined}
                          className={leverDanger}>
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

// A one-time password: the caution notice that asks the operator to save it (never green: nothing was verified).
function PasswordNotice({ title, password, onDismiss }: { title: string; password: string; onDismiss: () => void }) {
  return (
    <div className={notice.caution}>
      <p className="font-semibold text-lamp">{title}</p>
      <p className="mt-1">This password will not be shown again.</p>
      <pre className={`mt-3 ${codeBlock}`}>{password}</pre>
      <button type="button" onClick={onDismiss} className={`mt-3 ${leverSecondary}`}>
        Dismiss
      </button>
    </div>
  )
}

export default AdminUsersPanel
