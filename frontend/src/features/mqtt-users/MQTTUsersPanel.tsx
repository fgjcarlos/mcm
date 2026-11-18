import { useState } from 'react'
import { ListControls } from '../lists/ListControls'
import { usePagedList } from '../lists/usePagedList'
import { authenticatedFetch, isForbiddenResponseError, isUnauthorizedResponseError } from '../api/client'
import { can, permissionTitle } from '../auth/permissions'
import { Lamp } from '../ui/primitives'
import { bodyText, codeBlock, leverDanger, leverSecondary, notice, tableHead, tableRow } from '../ui/styles'
import UserOnboardingFlow from './UserOnboardingFlow'

type MQTTUser = {
  id: number
  username: string
  disabled: boolean
  created_at: string
  updated_at: string
}

function MQTTUsersPanel({ token, onLogout, role = '', startOnboarding = false, onMutationStart, onMutation, onApplyStart, onApplySuccess }: { token: string; onLogout: () => void; role?: string; startOnboarding?: boolean; onMutationStart?: () => number; onMutation?: (mutationID: number) => void; onApplyStart?: () => number; onApplySuccess?: (mutationWatermark: number) => void }) {
  const canWrite = can(role, 'mqttUser.write')
  const canWriteRule = can(role, 'acl.write')
  const list = usePagedList<MQTTUser>({ path: '/api/v1/mqtt-users', rowsKey: 'users', token, onUnauthorized: onLogout, namespace: 'users', searchParam: 'q' })
  const users = list.rows
  // Errors of the row actions; the load error is shown by ListControls.
  const [error, setError] = useState('')
  // The integrated onboarding journey of issue #341: the user, its access
  // rule, the preview and the apply in one place. It opens at the user
  // step from "Add User" and at the rule step for an existing user. The
  // dashboard's "Connect an application" action (#354) opens it on mount.
  const [onboarding, setOnboarding] = useState<{ stage: 'user' | 'rule'; username: string } | null>(() => (startOnboarding && canWrite ? { stage: 'user', username: '' } : null))
  const [resetUserId, setResetUserId] = useState<number | null>(null)
  const [resetPassword, setResetPassword] = useState<string | null>(null)
  const [deleteConfirmId, setDeleteConfirmId] = useState<number | null>(null)
  const [togglingId, setTogglingId] = useState<number | null>(null)
  const fetchUsers = list.reload

  const handleToggle = async (user: MQTTUser) => {
    setTogglingId(user.id)
    const mutationID = onMutationStart?.() ?? 0
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
      onMutation?.(mutationID)
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
    const mutationID = onMutationStart?.() ?? 0
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
      onMutation?.(mutationID)
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) { setError((err as Error).message); return }
      setError('Could not reach the server.')
    }
  }

  const handleDelete = async (id: number) => {
    const mutationID = onMutationStart?.() ?? 0
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
      if (resetUserId === id) {
        setResetUserId(null)
        setResetPassword(null)
      }
      onMutation?.(mutationID)
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
          <h3 className="text-lg font-semibold text-enamel-bright">MQTT users</h3>
          <p className={`mt-1 max-w-[68ch] ${bodyText}`}>The MQTT identities Mosquitto authenticates.</p>
        </div>
        <button
          type="button"
          onClick={() => {
            setOnboarding({ stage: 'user', username: '' })
          }}
          disabled={!canWrite}
          title={!canWrite ? permissionTitle('mqttUser.write') : undefined}
          className={leverSecondary}
        >
          Add User
        </button>
      </div>

      {error ? (
        <div className={notice.failed}>{error}</div>
      ) : null}

      {onboarding ? (
        <UserOnboardingFlow
          token={token}
          onLogout={onLogout}
          role={role}
          initialStage={onboarding.stage}
          initialUsername={onboarding.username}
          onMutationStart={onMutationStart}
          onMutation={onMutation}
          onApplyStart={onApplyStart}
          onApplySuccess={onApplySuccess}
          onUserCreated={() => { fetchUsers() }}
          onClose={() => setOnboarding(null)}
        />
      ) : null}

      {resetUserId !== null && resetPassword !== null ? (
        <div className={notice.caution}>
          <p className="font-semibold text-lamp">Password reset — save this password</p>
          <p className="mt-1">This password will not be shown again.</p>
          <pre className={`mt-3 ${codeBlock}`}>{resetPassword}</pre>
          <button
            type="button"
            onClick={() => {
              setResetUserId(null)
              setResetPassword(null)
            }}
            className={`mt-3 ${leverSecondary}`}
          >
            Dismiss
          </button>
        </div>
      ) : null}

      <ListControls list={list} noun={{ one: 'user', many: 'users' }} searchLabel="Search users" />

      <div className="table-scroll border-y border-track-strong">
        {users.length === 0 && list.loading ? (
          <p className={`py-6 ${bodyText}`}>Loading users…</p>
        ) : users.length === 0 ? (
          list.error ? null : <p className={`py-6 ${bodyText}`}>{list.hasFilters ? 'No MQTT users match the search.' : 'No MQTT users configured yet.'}</p>
        ) : (
          <table className="w-full min-w-[48rem] text-sm">
            <thead>
              <tr className="border-b border-track-strong">
                <th className={`${tableHead} text-left`}>Username</th>
                <th className={`${tableHead} text-left`}>Status</th>
                <th className={`${tableHead} text-left`}>Created</th>
                <th className={`${tableHead} pr-0 text-right`}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {users.map((user) => (
                <tr key={user.id} className={tableRow}>
                  <td className="py-3 pr-4 font-mono text-sm text-enamel-bright">{user.username}</td>
                  <td className="py-3 pr-4">
                    <Lamp tone={user.disabled ? 'off' : 'neutral'}>{user.disabled ? 'disabled' : 'enabled'}</Lamp>
                  </td>
                  <td className="py-3 pr-4 font-mono text-xs tabular-nums text-enamel-muted">{new Date(user.created_at).toLocaleDateString()}</td>
                  <td className="py-3 text-right">
                    {deleteConfirmId === user.id ? (
                      <span className="inline-flex items-center gap-2">
                        <span className="text-xs text-enamel-soft">Delete?</span>
                        <button
                          type="button"
                          onClick={() => {
                            void handleDelete(user.id)
                          }}
                          className={leverDanger}
                        >
                          Confirm
                        </button>
                        <button
                          type="button"
                          onClick={() => setDeleteConfirmId(null)}
                          className={leverSecondary}
                        >
                          Cancel
                        </button>
                      </span>
                    ) : (
                      <span className="inline-flex flex-wrap items-center justify-end gap-2">
                        <button
                          type="button"
                          onClick={() => {
                            setOnboarding({ stage: 'rule', username: user.username })
                          }}
                          disabled={!canWriteRule}
                          title={!canWriteRule ? permissionTitle('acl.write') : undefined}
                          className={leverSecondary}
                        >
                          Complete setup
                        </button>
                        <button
                          type="button"
                          onClick={() => {
                            void handleToggle(user)
                          }}
                          disabled={togglingId === user.id || !canWrite}
                          title={!canWrite ? permissionTitle('mqttUser.write') : undefined}
                          className={leverSecondary}
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
                          className={leverSecondary}
                        >
                          Reset password
                        </button>
                        <button
                          type="button"
                          onClick={() => setDeleteConfirmId(user.id)}
                          disabled={!canWrite}
                          title={!canWrite ? permissionTitle('mqttUser.write') : undefined}
                          className={leverDanger}
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
