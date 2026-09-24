import { useCallback, useEffect, useState } from 'react'
import { authenticatedFetch, isUnauthorizedResponseError } from '../api/client'
import type { ListenerApplyRequest, ListenerApplyResult, ListenerPreviewResult, ListenerSpec } from './types'

type ListenerIssue = NonNullable<ListenerPreviewResult['issues']>[number]

function errorMessage(body: unknown, fallback: string) {
  if (body && typeof body === 'object' && 'error' in body && typeof body.error === 'string') {
    return body.error
  }
  return fallback
}

export function useListeners(opts: { token: string; onLogout: () => void }) {
  const { token, onLogout } = opts
  const [listeners, setListeners] = useState<ListenerSpec[]>([])
  const [previewResult, setPreviewResult] = useState<ListenerPreviewResult | null>(null)
  const [previewError, setPreviewError] = useState('')
  const [applyError, setApplyError] = useState('')
  const [isLoading, setIsLoading] = useState(true)
  const [issues, setIssues] = useState<ListenerIssue[]>([])
  const [applyResult, setApplyResult] = useState<ListenerApplyResult | null>(null)

  const refresh = useCallback(async () => {
    setIsLoading(true)
    try {
      const response = await authenticatedFetch('/api/v1/listeners', { token, onUnauthorized: onLogout })
      if (!response.ok) {
        const body = await response.json().catch(() => null)
        throw new Error(errorMessage(body, 'Failed to load listeners.'))
      }
      const body = await response.json() as { specs?: ListenerSpec[] }
      setListeners(body.specs ?? [])
    } catch (error) {
      if (!isUnauthorizedResponseError(error)) setPreviewError(error instanceof Error ? error.message : 'Could not reach the server.')
    } finally {
      setIsLoading(false)
    }
  }, [token, onLogout])

  useEffect(() => {
    void Promise.resolve().then(refresh)
  }, [refresh])

  const requestPreview = useCallback(async (specs: ListenerSpec[], confirm = false) => {
    setPreviewError('')
    setPreviewResult(null)
    setIssues([])
    setApplyResult(null)
    try {
      const response = await authenticatedFetch('/api/v1/listeners/preview', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ specs, confirm }),
      })
      const body = await response.json().catch(() => null) as ListenerPreviewResult | { error?: string; issues?: ListenerIssue[] } | null
      if (!response.ok) {
        const validationIssues = body && 'issues' in body ? body.issues ?? [] : []
        setIssues(validationIssues)
        setPreviewError(errorMessage(body, 'Preview failed.'))
        return
      }
      setPreviewResult(body as ListenerPreviewResult)
      setIssues((body as ListenerPreviewResult).issues ?? [])
    } catch (error) {
      if (!isUnauthorizedResponseError(error)) setPreviewError(error instanceof Error ? error.message : 'Could not reach the server.')
    }
  }, [token, onLogout])

  const apply = useCallback(async (req: ListenerApplyRequest) => {
    setApplyError('')
    setApplyResult(null)
    try {
      const response = await authenticatedFetch('/api/v1/listeners/apply', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(req),
      })
      const body = await response.json().catch(() => null) as ListenerApplyResult | { error?: string } | null
      if (!response.ok) {
        setApplyError(errorMessage(body, 'Apply failed.'))
        return
      }
      const result = body as ListenerApplyResult
      setApplyResult(result)
      if (result.applied) {
        setPreviewResult(null)
        await refresh()
      }
    } catch (error) {
      if (!isUnauthorizedResponseError(error)) setApplyError(error instanceof Error ? error.message : 'Could not reach the server.')
    }
  }, [token, onLogout, refresh])

  return {
    listeners,
    preview: previewResult,
    previewError,
    applyError,
    isLoading,
    requestPreview,
    apply,
    refresh,
    issues,
    applyResult,
  }
}
