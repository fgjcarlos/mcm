import { useCallback, useEffect, useRef, useState } from 'react'
import { authenticatedFetch, isUnauthorizedResponseError } from '../api/client'
import type {
  Bridge,
  BridgeApplyRequest,
  BridgeApplyResult,
  BridgeIssue,
  BridgePreviewResult,
} from './bridgeTypes'

function errorMessage(body: unknown, fallback: string) {
  if (body && typeof body === 'object' && 'error' in body && typeof body.error === 'string') {
    return body.error
  }
  return fallback
}

// useBridges drives the bridge editor (issue #303). It loads the bridge
// connections of the managed configuration, previews the edited set and
// applies the resulting revision.
export function useBridges(opts: { token: string; onLogout: () => void }) {
  const { token, onLogout } = opts
  const [bridges, setBridges] = useState<Bridge[]>([])
  const [preview, setPreview] = useState<BridgePreviewResult | null>(null)
  const [previewError, setPreviewError] = useState('')
  const [applyError, setApplyError] = useState('')
  const [issues, setIssues] = useState<BridgeIssue[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [isPreviewing, setIsPreviewing] = useState(false)
  const [isApplying, setIsApplying] = useState(false)
  const [applied, setApplied] = useState(false)
  const [appliedStatus, setAppliedStatus] = useState('')
  const previewRequestId = useRef(0)
  const applying = useRef(false)

  const refresh = useCallback(async () => {
    setIsLoading(true)
    try {
      const response = await authenticatedFetch('/api/v1/broker/bridges', { token, onUnauthorized: onLogout })
      if (response.status === 404 || response.status === 503) {
        setBridges([])
        return
      }
      if (!response.ok) {
        const body = await response.json().catch(() => null)
        throw new Error(errorMessage(body, 'Failed to load bridges.'))
      }
      const body = await response.json() as { bridges?: Bridge[] }
      setBridges(body.bridges ?? [])
    } catch (error) {
      if (!isUnauthorizedResponseError(error)) setPreviewError(error instanceof Error ? error.message : 'Could not reach the server.')
    } finally {
      setIsLoading(false)
    }
  }, [token, onLogout])

  useEffect(() => {
    void Promise.resolve().then(refresh)
  }, [refresh])

  const requestPreview = useCallback(async (desired: Bridge[]) => {
    const requestId = ++previewRequestId.current
    setPreviewError('')
    setApplyError('')
    setIssues([])
    setApplied(false)
    setPreview(null)
    setIsPreviewing(true)
    try {
      const response = await authenticatedFetch('/api/v1/broker/bridges/preview', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ bridges: desired }),
      })
      const body = await response.json().catch(() => null) as BridgePreviewResult | { error?: string; issues?: BridgeIssue[] } | null
      if (requestId !== previewRequestId.current) return
      if (!response.ok) {
        setIssues(body && 'issues' in body ? body.issues ?? [] : [])
        setPreviewError(errorMessage(body, 'Preview failed.'))
        return
      }
      const result = body as BridgePreviewResult
      setPreview(result)
      setIssues(result.bridge_issues ?? [])
    } catch (error) {
      if (requestId === previewRequestId.current && !isUnauthorizedResponseError(error)) {
        setPreviewError(error instanceof Error ? error.message : 'Could not reach the server.')
      }
    } finally {
      if (requestId === previewRequestId.current) setIsPreviewing(false)
    }
  }, [token, onLogout])

  const apply = useCallback(async (req: BridgeApplyRequest): Promise<BridgeApplyResult | null> => {
    if (applying.current) return null
    applying.current = true
    setIsApplying(true)
    setApplyError('')
    try {
      const response = await authenticatedFetch('/api/v1/broker/bridges/apply', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(req),
      })
      const body = await response.json().catch(() => null) as BridgeApplyResult | { error?: string } | null
      if (!response.ok) {
        setApplyError(errorMessage(body, 'Apply failed.'))
        return null
      }
      setPreview(null)
      setApplied(true)
      setAppliedStatus((body as BridgeApplyResult)?.status ?? '')
      await refresh()
      return body as BridgeApplyResult
    } catch (error) {
      if (!isUnauthorizedResponseError(error)) setApplyError(error instanceof Error ? error.message : 'Could not reach the server.')
      return null
    } finally {
      applying.current = false
      setIsApplying(false)
    }
  }, [token, onLogout, refresh])

  return {
    bridges,
    preview,
    previewError,
    applyError,
    issues,
    isLoading,
    isPreviewing,
    isApplying,
    applied,
    appliedStatus,
    requestPreview,
    apply,
    refresh,
  }
}
