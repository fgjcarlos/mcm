import { useCallback, useRef, useState } from 'react'
import { authenticatedFetch, isUnauthorizedResponseError } from '../api/client'
import type { BrokerConfig, BrokerConfigApplyRequest, BrokerConfigPreview, CatalogResponse, ConfigApplyResult, Revision, ValidationIssue } from './types'

type Status = 'idle' | 'previewing' | 'applying'
type Draft = { body: string; baseHash: string }

export function useBrokerConfig(token: string, onLogout: () => void) {
  const [config, setConfig] = useState<BrokerConfig | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [preview, setPreview] = useState<BrokerConfigPreview | null>(null)
  const [catalog, setCatalog] = useState<CatalogResponse | null>(null)
  const [revisions, setRevisions] = useState<Revision[]>([])
  const [result, setResult] = useState<ConfigApplyResult | null>(null)
  const [issues, setIssues] = useState<ValidationIssue[]>([])
  const [status, setStatus] = useState<Status>('idle')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const generation = useRef(0)

  const request = useCallback(async (path: string, init?: RequestInit) => {
    const response = await authenticatedFetch(`/api/v1/broker/config${path}`, { token, onUnauthorized: onLogout, ...init })
    const body = await response.json()
    if (!response.ok) {
      if (Array.isArray(body.validation_issues)) setIssues(body.validation_issues)
      throw new Error(body.error || 'Configuration request failed.')
    }
    return body
  }, [token, onLogout])

  const report = (err: unknown) => {
    if (!isUnauthorizedResponseError(err)) setError(err instanceof Error ? err.message : 'Could not reach the server.')
  }

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const current = await request('') as BrokerConfig
      setConfig(current)
      // Refreshing disk state must never overwrite an unsaved draft/base.
      setDraft((previous) => previous ?? { body: current.body, baseHash: current.hash })
    } catch (err) {
      if (!isUnauthorizedResponseError(err)) setError(err instanceof Error ? err.message : 'Could not load configuration.')
    } finally { setLoading(false) }
  }, [request])

  const loadCatalog = useCallback(async () => {
    try { setCatalog(await request('/catalog') as CatalogResponse) }
    catch (err) { if (!isUnauthorizedResponseError(err)) setError(err instanceof Error ? err.message : 'Could not load catalog.') }
  }, [request])

  const loadHistory = useCallback(async () => {
    try { setRevisions((await request('/revisions')).revisions ?? []) }
    catch (err) { if (!isUnauthorizedResponseError(err)) setError(err instanceof Error ? err.message : 'Could not load history.') }
  }, [request])

  function editDraft(body: string) {
    generation.current += 1
    setDraft((previous) => ({ body, baseHash: previous?.baseHash ?? config?.hash ?? '' }))
    setPreview(null)
    setIssues([])
    setError('')
  }

  function resetDraft() {
    if (!config) return
    generation.current += 1
    setDraft({ body: config.body, baseHash: config.hash })
    setPreview(null)
    setIssues([])
    setError('')
  }

  async function prepare(path: string, body: unknown, proof?: string) {
    const requestedGeneration = ++generation.current
    setStatus('previewing')
    setError('')
    setIssues([])
    setPreview(null)
    try {
      const prepared = await request(path, { method: 'POST', headers: { 'Content-Type': 'application/json', ...(proof ? { 'X-MCM-MFA': proof } : {}) }, body: JSON.stringify(body) }) as BrokerConfigPreview
      if (generation.current !== requestedGeneration) return
      setPreview(prepared)
      setDraft({ body: prepared.conf_body, baseHash: prepared.base_conf_hash })
      await loadHistory()
    } catch (err) { report(err) }
    finally { setStatus('idle') }
  }

  async function apply(req: BrokerConfigApplyRequest, proof: string) {
    if (!proof || !preview || draft?.body !== preview.conf_body || draft.baseHash !== preview.base_conf_hash) {
      setError('Validate the current draft and confirm MFA before applying.')
      return
    }
    setStatus('applying')
    setError('')
    try {
      const outcome = await request('/apply', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-MCM-MFA': proof }, body: JSON.stringify(req) }) as ConfigApplyResult
      setResult(outcome)
      setPreview(null) // Every attempt consumes the revision, including rollback.
      if (outcome.status === 'active_verified') setDraft(null)
      await Promise.all([load(), loadHistory()])
    } catch (err) { report(err) }
    finally { setStatus('idle') }
  }

  async function exportCopy(proof: string) {
    setError('')
    try {
      const copy = await request('/copy', { headers: { 'X-MCM-MFA': proof } })
      const url = URL.createObjectURL(new Blob([JSON.stringify(copy, null, 2)], { type: 'application/json' }))
      const link = document.createElement('a')
      link.href = url
      link.download = 'mosquitto-config.json'
      link.click()
      URL.revokeObjectURL(url)
    } catch (err) { report(err) }
  }

  async function importCopy(text: string, proof: string) {
    try { await prepare('/copy', JSON.parse(text), proof) }
    catch { setError('Invalid configuration copy JSON.') }
  }

  return {
    config, draft, preview, catalog, revisions, result, issues, status, loading, error,
    load, loadCatalog, loadHistory, editDraft, resetDraft, apply, exportCopy,
    importCopyFile: async (file: File, proof: string) => {
      try { await importCopy(await file.text(), proof) } catch (err) { report(err) }
    },
    importPreview: () => prepare('/import', {}),
    previewTyped: () => prepare('/preview', { body: draft?.body ?? '', base_hash: draft?.baseHash ?? '' }),
    recover: (id: string, proof: string) => prepare(`/revisions/${encodeURIComponent(id)}/recover`, { base_hash: config?.hash ?? '' }, proof),
  }
}
