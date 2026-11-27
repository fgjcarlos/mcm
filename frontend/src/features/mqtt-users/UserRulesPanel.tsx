import { useEffect, useState, type FormEvent } from 'react'
import { authenticatedFetch, isForbiddenResponseError, isUnauthorizedResponseError } from '../api/client'
import { can, permissionTitle, requiredRoleFor } from '../auth/permissions'
import { PendingChangesAction } from '../deploy/PendingChangesAction'
import { PERMISSION_LABELS, type ACLPermission, type ACLRule, type ACLScope } from '../acl/types'
import { Plate } from '../ui/primitives'
import { bodyText, fieldHint, fieldLabel, input, leverDanger, leverPrimary, leverSecondary, notice, tableHead, tableRow } from '../ui/styles'

const PAGE = 100

type Draft = { id: string | null; topic: string; permission: ACLPermission; scope: ACLScope; listenerID: string; description: string }

const emptyDraft: Draft = { id: null, topic: '', permission: 'read', scope: 'global', listenerID: '', description: '' }

// Reads every page of this username's exact principal match.
async function fetchDirectRules(token: string, onLogout: () => void, username: string) {
  const direct: ACLRule[] = []
  for (let offset = 0; ; offset += PAGE) {
    let response: Response
    try {
      response = await authenticatedFetch(`/api/v1/acls?principal=${encodeURIComponent(username)}&limit=${PAGE}&offset=${offset}`, { token, onUnauthorized: onLogout })
    } catch (err) {
      if (isUnauthorizedResponseError(err) || isForbiddenResponseError(err)) throw err
      throw new Error('Could not reach the server.', { cause: err })
    }
    if (!response.ok) throw new Error(await errorText(response, 'Could not load the rules.'))
    const page = (await response.json()) as { rules: ACLRule[]; total: number }
    direct.push(...page.rules)
    if (page.rules.length === 0 || offset + page.rules.length >= page.total) return direct
  }
}

async function errorText(response: Response, fallback: string) {
  const body = (await response.json().catch(() => null)) as { error?: string; details?: string[] } | null
  return body?.details?.join('; ') ?? body?.error ?? fallback
}

// UserRulesPanel shows and edits the ACL rules written for one MQTT username
// (#360). The identity comes from the account it was opened from, so a save
// can neither ask for it again nor touch the rules of another account.
export default function UserRulesPanel({ token, onLogout, role, username, onMutationStart, onMutation, onReviewChanges, onClose }: {
  token: string
  onLogout: () => void
  role: string
  username: string
  onMutationStart?: () => number
  onMutation?: (mutationID: number) => void
  onReviewChanges?: () => void
  onClose: () => void
}) {
  const canWrite = can(role, 'acl.write')
  const [rules, setRules] = useState<ACLRule[] | null>(null)
  const [error, setError] = useState('')
  const [draft, setDraft] = useState<Draft | null>(null)
  const [formError, setFormError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [removeId, setRemoveId] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [conflict, setConflict] = useState(false)

  // Bumped after every save so the list is read again from the server.
  const [revision, setRevision] = useState(0)

  useEffect(() => {
    let cancelled = false
    fetchDirectRules(token, onLogout, username)
      .then((direct) => {
        if (cancelled) return
        setError('')
        setRules(direct)
      })
      .catch((err: unknown) => {
        if (cancelled || isUnauthorizedResponseError(err)) return
        setError(err instanceof Error ? err.message : 'Could not reach the server.')
      })
    return () => { cancelled = true }
  }, [token, onLogout, username, revision])

  // Every save goes through the deploy-pending bookkeeping of App; nothing is applied here.
  const mutate = async (url: string, method: string, body: string | undefined, fail: (message: string) => void) => {
    const mutationID = onMutationStart?.() ?? 0
    try {
      const response = await authenticatedFetch(url, { token, onUnauthorized: onLogout, method, headers: body ? { 'Content-Type': 'application/json' } : undefined, body })
      if (!response.ok && response.status !== 204) {
        if (response.status === 409 && method !== 'POST') {
          setConflict(true)
          setDraft(null)
          setRemoveId(null)
          setError('')
          setRules(null)
          setRevision((value) => value + 1)
          return false
        }
        fail(await errorText(response, 'Request failed.'))
        return false
      }
      onMutation?.(mutationID)
      setSaved(true)
      setRevision((value) => value + 1)
      return true
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return false
      fail(isForbiddenResponseError(err) ? (err as Error).message : 'Could not reach the server.')
      return false
    }
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!draft || submitting) return
    setSubmitting(true)
    setFormError('')
    const body = JSON.stringify({
      principal: username,
      ...(draft.id ? { expected_principal: username } : {}),
      selector: 'user',
      topic_filter: draft.topic.trim(),
      permission: draft.permission,
      scope: draft.scope,
      listener_id: draft.scope === 'listener' ? draft.listenerID.trim() : '',
      description: draft.description.trim(),
    })
    const ok = await mutate(draft.id ? `/api/v1/acls/${draft.id}` : '/api/v1/acls', draft.id ? 'PUT' : 'POST', body, setFormError)
    setSubmitting(false)
    if (ok) setDraft(null)
  }

  const handleRemove = async (id: string) => {
    if (await mutate(`/api/v1/acls/${id}?expected_principal=${encodeURIComponent(username)}`, 'DELETE', undefined, setError)) setRemoveId(null)
  }

  const headingId = `user-rules-${username}`
  return (
    <section aria-labelledby={headingId} className="border-y border-track-strong py-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h4 id={headingId} className="text-base font-semibold text-enamel-bright">
            Direct ACL rules of <span className="font-mono">{username}</span>
          </h4>
          <p className={`mt-1 max-w-[68ch] ${bodyText}`}>
            The rules written for this username. They are not its effective permissions: pattern rules and the ACL file
            of each listener also decide its access, and the effective permissions in the ACLs view evaluate them all.
          </p>
          {!canWrite ? <p className={`mt-1 max-w-[68ch] ${bodyText}`}>Changing ACL rules needs the {requiredRoleFor('acl.write')} role or higher.</p> : null}
        </div>
        <div className="flex flex-wrap gap-2">
          <button
            type="button"
            onClick={() => { setDraft(emptyDraft); setFormError('') }}
            disabled={!canWrite}
            title={!canWrite ? permissionTitle('acl.write') : undefined}
            className={leverSecondary}
          >
            Add rule
          </button>
          <button type="button" onClick={onClose} className={leverSecondary}>Close</button>
        </div>
      </div>

      {conflict ? <div role="status" className={`mt-4 ${notice.caution}`}>This rule no longer belongs to this user. No change was made. Check the refreshed list before trying again.</div> : null}
      {error ? <div role="alert" className={`mt-4 ${notice.failed}`}>{error}</div> : null}

      {saved ? (
        <div role="status" className={`mt-4 ${notice.caution}`}>
          <p className="text-sm text-enamel">Saved in MCM, not applied to Mosquitto yet. The review covers every pending change, not only this user&apos;s.</p>
          {onReviewChanges ? <PendingChangesAction role={role} onReview={onReviewChanges} /> : null}
        </div>
      ) : null}

      {draft ? (
        <form onSubmit={handleSubmit} className="mt-4 max-w-2xl space-y-5">
          <p className={bodyText}>
            {draft.id ? 'Edit rule' : 'New rule'} for <span className="font-mono text-enamel-bright">{username}</span>
          </p>
          <label className="block">
            <span className={fieldLabel}>Topic filter</span>
            <input type="text" required value={draft.topic} onChange={(e) => setDraft({ ...draft, topic: e.target.value })} placeholder="sensors/# or device/+/status" className={`mt-2 w-full ${input}`} />
            <span className={`mt-1 block ${fieldHint}`}>MQTT wildcards: + (single level), # (multi-level, must be last)</span>
          </label>
          <label className="block">
            <span className={fieldLabel}>Permission</span>
            <select value={draft.permission} onChange={(e) => setDraft({ ...draft, permission: e.target.value as ACLPermission })} className={`mt-2 w-full ${input}`}>
              <option value="read">read</option>
              <option value="write">write</option>
              <option value="readwrite">readwrite</option>
              <option value="deny">deny</option>
            </select>
          </label>
          <label className="block">
            <span className={fieldLabel}>ACL file</span>
            <select value={draft.scope} onChange={(e) => setDraft({ ...draft, scope: e.target.value as ACLScope })} className={`mt-2 w-full ${input}`}>
              <option value="global">Broker-wide &mdash; the rule applies to every listener</option>
              <option value="listener">One listener &mdash; the rule applies to that listener only</option>
            </select>
          </label>
          {draft.scope === 'listener' ? (
            <label className="block">
              <span className={fieldLabel}>Listener</span>
              <input type="text" required value={draft.listenerID} onChange={(e) => setDraft({ ...draft, listenerID: e.target.value })} placeholder="tls" className={`mt-2 w-full ${input}`} />
            </label>
          ) : null}
          <label className="block">
            <span className={fieldLabel}>Description (optional)</span>
            <input type="text" value={draft.description} onChange={(e) => setDraft({ ...draft, description: e.target.value })} className={`mt-2 w-full ${input}`} />
          </label>
          {formError ? <div role="alert" className={notice.failed}>{formError}</div> : null}
          <div className="flex flex-wrap items-center gap-3">
            <button type="submit" disabled={submitting || !canWrite} className={leverPrimary}>{submitting ? 'Saving…' : 'Save rule'}</button>
            <button type="button" onClick={() => setDraft(null)} className={leverSecondary}>Cancel</button>
          </div>
        </form>
      ) : null}

      {rules === null ? (
        error ? null : <p className={`mt-4 ${bodyText}`}>Loading rules…</p>
      ) : rules.length === 0 ? (
        <p className={`mt-4 ${bodyText}`}>{username} has no direct ACL rule.</p>
      ) : (
        <div className="table-scroll mt-4">
          <table className="w-full min-w-[40rem] text-sm">
            <thead>
              <tr className="border-b border-track-strong">
                <th className={`${tableHead} text-left`}>Topic filter</th>
                <th className={`${tableHead} text-left`}>Permission</th>
                <th className={`${tableHead} text-left`}>ACL file</th>
                <th className={`${tableHead} pr-0 text-right`}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {rules.map((rule) => (
                <tr key={rule.id} className={tableRow}>
                  <td className="py-3 pr-4 font-mono text-sm text-enamel-bright">{rule.topic_filter}</td>
                  <td className="py-3 pr-4"><Plate>{PERMISSION_LABELS[rule.permission]}</Plate></td>
                  <td className="py-3 pr-4 font-mono text-xs text-enamel-soft">{rule.scope === 'listener' ? rule.listener_id : 'broker-wide'}</td>
                  <td className="py-3 text-right">
                    {removeId === rule.id ? (
                      <span className="inline-flex items-center gap-2">
                        <span className="text-xs text-enamel-soft">Remove?</span>
                        <button type="button" onClick={() => { void handleRemove(rule.id) }} className={leverDanger}>Confirm</button>
                        <button type="button" onClick={() => setRemoveId(null)} className={leverSecondary}>Cancel</button>
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-2">
                        <button
                          type="button"
                          aria-label={`Edit rule ${rule.topic_filter}`}
                          onClick={() => { setDraft({ id: rule.id, topic: rule.topic_filter, permission: rule.permission, scope: rule.scope ?? 'global', listenerID: rule.listener_id ?? '', description: rule.description ?? '' }); setFormError('') }}
                          disabled={!canWrite}
                          title={!canWrite ? permissionTitle('acl.write') : undefined}
                          className={leverSecondary}
                        >
                          Edit
                        </button>
                        <button
                          type="button"
                          aria-label={`Remove rule ${rule.topic_filter}`}
                          onClick={() => setRemoveId(rule.id)}
                          disabled={!canWrite}
                          title={!canWrite ? permissionTitle('acl.write') : undefined}
                          className={leverDanger}
                        >
                          Remove
                        </button>
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
