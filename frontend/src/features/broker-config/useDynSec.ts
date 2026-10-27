import { useCallback, useEffect, useRef, useState } from 'react'
import { authenticatedFetch, isUnauthorizedResponseError } from '../api/client'
import type {
  DynSecApplyRequest,
  DynSecApplyResult,
  DynSecDocument,
  DynSecGuard,
  DynSecIssue,
  DynSecPreviewResult,
  DynSecState,
} from './dynsecTypes'

function errorMessage(body: unknown, fallback: string) {
  if (body && typeof body === 'object' && 'error' in body && typeof body.error === 'string') {
    return body.error
  }
  return fallback
}

// guardOf reads the guard report of a response body, when it carries one.
function guardOf(body: unknown): DynSecGuard | null {
  if (body && typeof body === 'object' && 'guard' in body) {
    const guard = (body as { guard?: DynSecGuard }).guard
    if (guard && typeof guard === 'object') return guard
  }
  return null
}

// useDynSec drives the dynamic security editor (issue #304). It loads the
// security database of the plugin together with the report of the
// management channel, previews the edited document and applies the
// resulting plan.
export function useDynSec(opts: { token: string; onLogout: () => void }) {
  const { token, onLogout } = opts
  const [state, setState] = useState<DynSecState | null>(null)
  const [preview, setPreview] = useState<DynSecPreviewResult | null>(null)
  const [adoption, setAdoption] = useState<DynSecPreviewResult | null>(null)
  const [guard, setGuard] = useState<DynSecGuard | null>(null)
  const [previewError, setPreviewError] = useState('')
  const [applyError, setApplyError] = useState('')
  const [issues, setIssues] = useState<DynSecIssue[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [isPreviewing, setIsPreviewing] = useState(false)
  const [isApplying, setIsApplying] = useState(false)
  const [isAdopting, setIsAdopting] = useState(false)
  const [applied, setApplied] = useState(false)
  const previewRequestId = useRef(0)
  const applying = useRef(false)

  const refresh = useCallback(async () => {
    setIsLoading(true)
    try {
      const response = await authenticatedFetch('/api/v1/broker/dynsec', { token, onUnauthorized: onLogout })
      if (response.status === 404 || response.status === 503) {
        setState(null)
        return
      }
      if (!response.ok) {
        const body = await response.json().catch(() => null)
        throw new Error(errorMessage(body, 'Failed to load the dynamic security state.'))
      }
      const body = (await response.json()) as DynSecState
      setState(body)
      // The guard only means something when the plugin owns the access
      // control of the broker.
      setGuard(body.backend === 'dynamic-security' ? body.guard ?? null : null)
    } catch (error) {
      if (!isUnauthorizedResponseError(error)) setPreviewError(error instanceof Error ? error.message : 'Could not reach the server.')
    } finally {
      setIsLoading(false)
    }
  }, [token, onLogout])

  useEffect(() => {
    void Promise.resolve().then(refresh)
  }, [refresh])

  const requestPreview = useCallback(async (document: DynSecDocument) => {
    const requestId = ++previewRequestId.current
    setPreviewError('')
    setApplyError('')
    setIssues([])
    setApplied(false)
    setPreview(null)
    setIsPreviewing(true)
    try {
      const response = await authenticatedFetch('/api/v1/broker/dynsec/preview', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ document }),
      })
      const body = await response.json().catch(() => null) as DynSecPreviewResult | { error?: string; issues?: DynSecIssue[] } | null
      if (requestId !== previewRequestId.current) return
      if (!response.ok) {
        setIssues(body && 'issues' in body ? body.issues ?? [] : [])
        // A refused change carries the guard that refused it.
        const reported = guardOf(body)
        if (reported) setGuard(reported)
        setPreviewError(errorMessage(body, 'Preview failed.'))
        return
      }
      const result = body as DynSecPreviewResult
      setPreview(result)
      if (result.guard) setGuard(result.guard)
    } catch (error) {
      if (requestId === previewRequestId.current && !isUnauthorizedResponseError(error)) {
        setPreviewError(error instanceof Error ? error.message : 'Could not reach the server.')
      }
    } finally {
      if (requestId === previewRequestId.current) setIsPreviewing(false)
    }
  }, [token, onLogout])

  const apply = useCallback(async (req: DynSecApplyRequest): Promise<DynSecApplyResult | null> => {
    if (applying.current) return null
    applying.current = true
    setIsApplying(true)
    setApplyError('')
    try {
      const response = await authenticatedFetch('/api/v1/broker/dynsec/apply', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(req),
      })
      const body = await response.json().catch(() => null) as DynSecApplyResult | { error?: string } | null
      if (!response.ok) {
        const reported = guardOf(body)
        if (reported) setGuard(reported)
        setApplyError(errorMessage(body, 'Apply failed.'))
        return null
      }
      const result = body as DynSecApplyResult
      setPreview(null)
      setAdoption(null)
      setApplied(true)
      if (result.guard) setGuard(result.guard)
      await refresh()
      return result
    } catch (error) {
      if (!isUnauthorizedResponseError(error)) setApplyError(error instanceof Error ? error.message : 'Could not reach the server.')
      return null
    } finally {
      applying.current = false
      setIsApplying(false)
    }
  }, [token, onLogout, refresh])

  const adopt = useCallback(async (): Promise<DynSecPreviewResult | null> => {
    setIsAdopting(true)
    setApplyError('')
    setPreviewError('')
    try {
      const response = await authenticatedFetch('/api/v1/broker/dynsec/adopt', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
      })
      const body = await response.json().catch(() => null) as DynSecPreviewResult | { error?: string } | null
      if (!response.ok) {
        setApplyError(errorMessage(body, 'The adoption failed.'))
        return null
      }
      const result = body as DynSecPreviewResult
      setAdoption(result)
      if (result.guard) setGuard(result.guard)
      return result
    } catch (error) {
      if (!isUnauthorizedResponseError(error)) setApplyError(error instanceof Error ? error.message : 'Could not reach the server.')
      return null
    } finally {
      setIsAdopting(false)
    }
  }, [token, onLogout])

  return {
    state,
    preview,
    adoption,
    guard,
    previewError,
    applyError,
    issues,
    isLoading,
    isPreviewing,
    isApplying,
    isAdopting,
    applied,
    requestPreview,
    apply,
    adopt,
    refresh,
  }
}
