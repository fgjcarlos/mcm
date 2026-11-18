import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { isForbiddenResponseError, isUnauthorizedResponseError } from '../api/client'
import { adoptACLSource, fetchACLSource } from './api'
import type { ACLSource } from './types'
import { Section } from '../ui/primitives'
import { bodyText, codeBlock, fieldHint, fieldLabel, input, leverSecondary, notice, textarea } from '../ui/styles'

// ACLSourcePanel brings an existing Mosquitto ACL file under the
// management of MCM.
//
// Adopting keeps the file verbatim: the deploy composes the managed rules
// into it instead of replacing it, so a rule MCM does not model survives.
// The preview of the deploy reports the access the composition would drop,
// and an apply that would drop access MCM can neither reproduce nor
// attribute to a managed MQTT user is refused.
function ACLSourcePanel({ token, onLogout, canWrite, onMutationStart, onMutation }: { token: string; onLogout: () => void; canWrite: boolean; onMutationStart?: () => number; onMutation?: (mutationID: number) => void }) {
  const [source, setSource] = useState<ACLSource | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [formPath, setFormPath] = useState('')
  const [formBody, setFormBody] = useState('')
  const [formError, setFormError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [refreshTick, setRefreshTick] = useState(0)

  const reload = useCallback(() => setRefreshTick((n) => n + 1), [])

  useEffect(() => {
    let cancelled = false
    fetchACLSource(token, onLogout)
      .then((result) => {
        if (cancelled) return
        setSource(result)
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

  const handleAdopt = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting || !canWrite) return
    setSubmitting(true)
    setFormError('')
    const mutationID = onMutationStart?.() ?? 0
    try {
      const result = await adoptACLSource(token, onLogout, {
        path: formPath.trim() || undefined,
        body: formBody.length > 0 ? formBody : undefined,
      })
      setSource(result)
      setFormBody('')
      setFormPath('')
      // The adopted source changes what the deploy renders, so the change
      // stays pending until an apply writes it to the broker.
      onMutation?.(mutationID)
      reload()
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) {
        setFormError((err as Error).message)
        return
      }
      setFormError((err as Error).message || 'Could not reach the server.')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Section
      title="Existing ACL file"
      description="Adopt an ACL file that already exists on the broker. MCM keeps its content verbatim and composes the managed rules into it, so nothing is lost."
    >
      {error ? (
        <div className={`mt-4 ${notice.failed}`}>{error}</div>
      ) : null}

      {loading ? (
        <p className={`mt-4 ${bodyText}`}>Loading the adopted ACL source…</p>
      ) : source?.adopted ? (
        <div className="mt-4">
          <p className="text-sm text-enamel">
            The ACL file <span className="font-mono text-enamel-bright">{source.path}</span> is adopted.
          </p>
          {source.content_hash ? <p className="mt-1 break-all font-mono text-xs text-enamel-muted">sha256 {source.content_hash}</p> : null}
          {source.body ? (
            <pre className={`mt-3 max-h-64 ${codeBlock}`}>
              {source.body}
            </pre>
          ) : null}
        </div>
      ) : (
        <p className={`mt-4 ${notice.neutral}`}>
          No ACL file was adopted yet. The deploy writes the ACL file MCM manages.
        </p>
      )}

      <form onSubmit={handleAdopt} className="mt-6 max-w-2xl border-t border-track pt-4">
        <h4 className="text-sm font-semibold text-enamel-bright">Adopt an ACL file</h4>
        <div className="mt-4 space-y-5">
          <label className="block">
            <span className={fieldLabel}>Path (optional)</span>
            <input
              type="text"
              value={formPath}
              onChange={(e) => setFormPath(e.target.value)}
              placeholder="/mosquitto/config/acl"
              className={`mt-2 w-full font-mono ${input}`}
            />
            <span className={`mt-1 block ${fieldHint}`}>
              The file to take over. Leave it empty to adopt the ACL file MCM manages, and leave the content empty to read
              the file from that path.
            </span>
          </label>
          <label className="block">
            <span className={fieldLabel}>Content (optional)</span>
            <textarea
              value={formBody}
              onChange={(e) => setFormBody(e.target.value)}
              rows={6}
              placeholder={'user alice\ntopic read sensors/#\npattern readwrite devices/%u/data'}
              className={`mt-2 w-full ${textarea}`}
            />
          </label>
          {formError ? (
            <div className={notice.failed}>{formError}</div>
          ) : null}
          <div className="flex flex-wrap items-center gap-3">
            <button
              type="submit"
              disabled={submitting || !canWrite}
              title={!canWrite ? 'Requires the operator role' : undefined}
              className={leverSecondary}
            >
              {submitting ? 'Adopting…' : 'Adopt ACL file'}
            </button>
            <button
              type="button"
              onClick={reload}
              className={leverSecondary}
            >
              Refresh
            </button>
          </div>
        </div>
      </form>
    </Section>
  )
}

export default ACLSourcePanel
