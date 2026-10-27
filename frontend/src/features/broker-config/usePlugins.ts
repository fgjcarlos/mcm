import { useCallback, useEffect, useRef, useState } from 'react'
import { authenticatedFetch, isUnauthorizedResponseError } from '../api/client'
import type {
  Plugin,
  PluginApplyRequest,
  PluginApplyResult,
  PluginIssue,
  PluginPreviewResult,
  PluginsResponse,
} from './pluginTypes'

function errorMessage(body: unknown, fallback: string) {
  if (body && typeof body === 'object' && 'error' in body && typeof body.error === 'string') {
    return body.error
  }
  return fallback
}

// usePlugins drives the plugin editor (issue #304). It loads the catalog of
// the plugins MCM supports for the broker version together with the plugins
// the broker loads, previews the edited document and applies the resulting
// revision.
export function usePlugins(opts: { token: string; onLogout: () => void }) {
  const { token, onLogout } = opts
  const [plugins, setPlugins] = useState<PluginsResponse | null>(null)
  const [preview, setPreview] = useState<PluginPreviewResult | null>(null)
  const [previewError, setPreviewError] = useState('')
  const [applyError, setApplyError] = useState('')
  const [issues, setIssues] = useState<PluginIssue[]>([])
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
      const response = await authenticatedFetch('/api/v1/broker/plugins', { token, onUnauthorized: onLogout })
      if (response.status === 404 || response.status === 503) {
        setPlugins(null)
        return
      }
      if (!response.ok) {
        const body = await response.json().catch(() => null)
        throw new Error(errorMessage(body, 'Failed to load the plugins.'))
      }
      setPlugins((await response.json()) as PluginsResponse)
    } catch (error) {
      if (!isUnauthorizedResponseError(error)) setPreviewError(error instanceof Error ? error.message : 'Could not reach the server.')
    } finally {
      setIsLoading(false)
    }
  }, [token, onLogout])

  useEffect(() => {
    void Promise.resolve().then(refresh)
  }, [refresh])

  const requestPreview = useCallback(async (desired: Plugin[]) => {
    const requestId = ++previewRequestId.current
    setPreviewError('')
    setApplyError('')
    setIssues([])
    setApplied(false)
    setPreview(null)
    setIsPreviewing(true)
    try {
      const response = await authenticatedFetch('/api/v1/broker/plugins/preview', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ plugins: desired }),
      })
      const body = await response.json().catch(() => null) as PluginPreviewResult | { error?: string; issues?: PluginIssue[] } | null
      if (requestId !== previewRequestId.current) return
      if (!response.ok) {
        setIssues(body && 'issues' in body ? body.issues ?? [] : [])
        setPreviewError(errorMessage(body, 'Preview failed.'))
        return
      }
      setPreview(body as PluginPreviewResult)
    } catch (error) {
      if (requestId === previewRequestId.current && !isUnauthorizedResponseError(error)) {
        setPreviewError(error instanceof Error ? error.message : 'Could not reach the server.')
      }
    } finally {
      if (requestId === previewRequestId.current) setIsPreviewing(false)
    }
  }, [token, onLogout])

  const apply = useCallback(async (req: PluginApplyRequest): Promise<PluginApplyResult | null> => {
    if (applying.current) return null
    applying.current = true
    setIsApplying(true)
    setApplyError('')
    try {
      const response = await authenticatedFetch('/api/v1/broker/plugins/apply', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(req),
      })
      const body = await response.json().catch(() => null) as PluginApplyResult | { error?: string } | null
      if (!response.ok) {
        setApplyError(errorMessage(body, 'Apply failed.'))
        return null
      }
      setPreview(null)
      setApplied(true)
      setAppliedStatus((body as PluginApplyResult)?.status ?? '')
      await refresh()
      return body as PluginApplyResult
    } catch (error) {
      if (!isUnauthorizedResponseError(error)) setApplyError(error instanceof Error ? error.message : 'Could not reach the server.')
      return null
    } finally {
      applying.current = false
      setIsApplying(false)
    }
  }, [token, onLogout, refresh])

  return {
    plugins,
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
