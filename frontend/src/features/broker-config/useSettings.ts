import { useCallback, useEffect, useRef, useState } from 'react'
import { authenticatedFetch, isUnauthorizedResponseError } from '../api/client'
import type {
  BrokerSettings,
  SettingsApplyRequest,
  SettingsDocument,
  SettingsIssue,
  SettingsPreview,
} from './settingsTypes'

function errorMessage(body: unknown, fallback: string) {
  if (body && typeof body === 'object' && 'error' in body && typeof body.error === 'string') {
    return body.error
  }
  return fallback
}

// useSettings drives the broker settings editor (issue #302). It loads the
// current settings together with the catalog description of the form,
// previews the edited document and applies the resulting revision through
// the shared broker-config apply endpoint.
export function useSettings(opts: { token: string; onLogout: () => void }) {
  const { token, onLogout } = opts
  const [settings, setSettings] = useState<BrokerSettings | null>(null)
  const [preview, setPreview] = useState<SettingsPreview | null>(null)
  const [previewError, setPreviewError] = useState('')
  const [applyError, setApplyError] = useState('')
  const [issues, setIssues] = useState<SettingsIssue[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [isPreviewing, setIsPreviewing] = useState(false)
  const [isApplying, setIsApplying] = useState(false)
  const [applied, setApplied] = useState(false)
  const previewRequestId = useRef(0)
  const applying = useRef(false)

  const refresh = useCallback(async () => {
    setIsLoading(true)
    try {
      const response = await authenticatedFetch('/api/v1/broker/settings', { token, onUnauthorized: onLogout })
      if (response.status === 404 || response.status === 503) {
        setSettings(null)
        return
      }
      if (!response.ok) {
        const body = await response.json().catch(() => null)
        throw new Error(errorMessage(body, 'Failed to load broker settings.'))
      }
      setSettings((await response.json()) as BrokerSettings)
    } catch (error) {
      if (!isUnauthorizedResponseError(error)) setPreviewError(error instanceof Error ? error.message : 'Could not reach the server.')
    } finally {
      setIsLoading(false)
    }
  }, [token, onLogout])

  useEffect(() => {
    void Promise.resolve().then(refresh)
  }, [refresh])

  const requestPreview = useCallback(async (document: SettingsDocument) => {
    const requestId = ++previewRequestId.current
    setPreviewError('')
    setApplyError('')
    setIssues([])
    setApplied(false)
    setPreview(null)
    setIsPreviewing(true)
    try {
      const response = await authenticatedFetch('/api/v1/broker/settings/preview', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(document),
      })
      const body = await response.json().catch(() => null) as SettingsPreview | { error?: string; issues?: SettingsIssue[] } | null
      if (requestId !== previewRequestId.current) return
      if (!response.ok) {
        setIssues(body && 'issues' in body ? body.issues ?? [] : [])
        setPreviewError(errorMessage(body, 'Preview failed.'))
        return
      }
      setPreview(body as SettingsPreview)
    } catch (error) {
      if (requestId === previewRequestId.current && !isUnauthorizedResponseError(error)) {
        setPreviewError(error instanceof Error ? error.message : 'Could not reach the server.')
      }
    } finally {
      if (requestId === previewRequestId.current) setIsPreviewing(false)
    }
  }, [token, onLogout])

  const apply = useCallback(async (req: SettingsApplyRequest): Promise<boolean> => {
    if (applying.current) return false
    applying.current = true
    setIsApplying(true)
    setApplyError('')
    try {
      const response = await authenticatedFetch('/api/v1/broker/config/apply', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(req),
      })
      const body = await response.json().catch(() => null) as { error?: string } | null
      if (!response.ok) {
        setApplyError(errorMessage(body, 'Apply failed.'))
        return false
      }
      setPreview(null)
      setApplied(true)
      await refresh()
      return true
    } catch (error) {
      if (!isUnauthorizedResponseError(error)) setApplyError(error instanceof Error ? error.message : 'Could not reach the server.')
      return false
    } finally {
      applying.current = false
      setIsApplying(false)
    }
  }, [token, onLogout, refresh])

  return {
    settings,
    preview,
    previewError,
    applyError,
    issues,
    isLoading,
    isPreviewing,
    isApplying,
    applied,
    requestPreview,
    apply,
    refresh,
  }
}
