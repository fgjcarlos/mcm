import { useCallback, useEffect, useState, type FormEvent } from 'react'
import type { ACLRule, MQTTUser } from '../types'
import { createACL, deleteACL, listACLs, listMQTTUsers, updateACL } from '../lib/api'

type Props = {
  token: string
}

export function ACLsPage({ token }: Props) {
  const [rules, setRules] = useState<ACLRule[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [mqttUsernames, setMqttUsernames] = useState<string[]>([])
  const [showForm, setShowForm] = useState(false)
  const [editRule, setEditRule] = useState<ACLRule | null>(null)
  const [formPrincipal, setFormPrincipal] = useState('')
  const [formTopicFilter, setFormTopicFilter] = useState('')
  const [formPermission, setFormPermission] = useState<ACLRule['permission']>('read')
  const [formDescription, setFormDescription] = useState('')
  const [formError, setFormError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
  const [refreshTick, setRefreshTick] = useState(0)

  const fetchRules = useCallback(() => { setRefreshTick((n) => n + 1) }, [])

  // Fetch MQTT usernames for principal datalist suggestions.
  // Failures degrade gracefully: plain text input with no suggestions shown.
  useEffect(() => {
    let cancelled = false
    listMQTTUsers(token)
      .then((users: MQTTUser[]) => {
        if (cancelled) return
        setMqttUsernames(users.map((u) => u.username))
      })
      .catch(() => {
        // Intentionally swallowed — datalist is a progressive enhancement.
      })
    return () => { cancelled = true }
  }, [token])

  useEffect(() => {
    let cancelled = false
    listACLs(token)
      .then((body) => {
        if (cancelled) return
        setRules(body.rules ?? [])
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

  const openCreate = () => {
    setEditRule(null)
    setFormPrincipal('')
    setFormTopicFilter('')
    setFormPermission('read')
    setFormDescription('')
    setFormError('')
    setShowForm(true)
  }

  const openEdit = (rule: ACLRule) => {
    setEditRule(rule)
    setFormPrincipal(rule.principal)
    setFormTopicFilter(rule.topic_filter)
    setFormPermission(rule.permission)
    setFormDescription(rule.description ?? '')
    setFormError('')
    setShowForm(true)
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting) return
    setSubmitting(true)
    setFormError('')
    try {
      const data = {
        principal: formPrincipal.trim(),
        topic_filter: formTopicFilter.trim(),
        permission: formPermission,
        description: formDescription.trim(),
      }
      if (editRule) {
        await updateACL(token, editRule.id, data)
      } else {
        await createACL(token, data)
      }
      setShowForm(false)
      fetchRules()
    } catch (err) {
      setFormError((err as Error).message ?? 'Request failed.')
    } finally {
      setSubmitting(false)
    }
  }

  const handleDelete = async (id: string) => {
    try {
      await deleteACL(token, id)
      setDeleteConfirmId(null)
      fetchRules()
    } catch (err) {
      setError((err as Error).message ?? 'Delete failed.')
    }
  }

  return (
    <section className="mt-8 space-y-6">
      <div className="flex items-center justify-between gap-4">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">ACL rules</p>
          <p className="mt-1 text-sm text-slate-300">{rules.length} rule{rules.length !== 1 ? 's' : ''} configured</p>
        </div>
        <button
          type="button"
          onClick={openCreate}
          className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400"
        >
          Add Rule
        </button>
      </div>

      {error ? (
        <div className="rounded-2xl border border-dashed border-amber-300/30 bg-amber-400/10 p-5 text-sm text-amber-100">{error}</div>
      ) : null}

      {showForm ? (
        <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">{editRule ? 'Edit rule' : 'New rule'}</p>
          <form onSubmit={(e) => { void handleSubmit(e) }} className="mt-4 space-y-4">
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Principal</span>
              <input
                type="text"
                required
                value={formPrincipal}
                onChange={(e) => setFormPrincipal(e.target.value)}
                placeholder="username or $client_id"
                list="mqtt-usernames-list"
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              />
              {mqttUsernames.length > 0 ? (
                <datalist id="mqtt-usernames-list">
                  {mqttUsernames.map((username) => (
                    <option key={username} value={username} />
                  ))}
                </datalist>
              ) : null}
            </label>
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Topic filter</span>
              <input
                type="text"
                required
                value={formTopicFilter}
                onChange={(e) => setFormTopicFilter(e.target.value)}
                placeholder="sensors/# or device/+/status"
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              />
              <span className="mt-1 block text-xs text-slate-400">MQTT wildcards: + (single level), # (multi-level, must be last)</span>
            </label>
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Permission</span>
              <select
                value={formPermission}
                onChange={(e) => setFormPermission(e.target.value as ACLRule['permission'])}
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              >
                <option value="read">read</option>
                <option value="write">write</option>
                <option value="readwrite">readwrite</option>
              </select>
            </label>
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Description (optional)</span>
              <input
                type="text"
                value={formDescription}
                onChange={(e) => setFormDescription(e.target.value)}
                placeholder="Short description for audit purposes"
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              />
            </label>
            {formError ? (
              <div className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">{formError}</div>
            ) : null}
            <div className="flex gap-3">
              <button
                type="submit"
                disabled={submitting}
                className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
              >
                {submitting ? 'Saving…' : editRule ? 'Update rule' : 'Create rule'}
              </button>
              <button
                type="button"
                onClick={() => setShowForm(false)}
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
          <p className="p-6 text-sm text-slate-400">Loading rules…</p>
        ) : rules.length === 0 ? (
          <p className="p-6 text-sm text-slate-400">No ACL rules configured yet.</p>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10">
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Principal</th>
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Topic filter</th>
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Permission</th>
                <th className="px-4 py-3 text-right text-xs uppercase tracking-[0.18em] text-slate-400">Actions</th>
              </tr>
            </thead>
            <tbody>
              {rules.map((rule) => (
                <tr key={rule.id} className="border-b border-white/5 last:border-0">
                  <td className="px-4 py-3 font-mono text-sm text-slate-200">{rule.principal}</td>
                  <td className="px-4 py-3 font-mono text-sm text-cyan-100">{rule.topic_filter}</td>
                  <td className="px-4 py-3">
                    <span className="rounded-full bg-cyan-400/10 px-2.5 py-1 font-mono text-xs text-cyan-200">{rule.permission}</span>
                  </td>
                  <td className="px-4 py-3 text-right">
                    {deleteConfirmId === rule.id ? (
                      <span className="inline-flex items-center gap-2">
                        <span className="text-xs text-slate-300">Delete?</span>
                        <button
                          type="button"
                          onClick={() => { void handleDelete(rule.id) }}
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
                          onClick={() => openEdit(rule)}
                          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white"
                        >
                          Edit
                        </button>
                        <button
                          type="button"
                          onClick={() => setDeleteConfirmId(rule.id)}
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
