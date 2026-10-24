import { useCallback, useEffect, useRef, useState } from 'react'
import { authenticatedFetch, isUnauthorizedResponseError } from '../api/client'
import type {
  CertificateActionRequest,
  CertificateApplyResult,
  CertificateListResponse,
  CertificatePreview,
  CertificateProfile,
  CertificateRevision,
  CertificateRevisionRequest,
} from './certificateTypes'

function errorMessage(body: unknown, fallback: string): string {
  if (body && typeof body === 'object' && 'error' in body && typeof body.error === 'string') {
    return body.error
  }
  return fallback
}

function issueMessages(body: unknown): string[] {
  if (!body || typeof body !== 'object' || !('issues' in body)) return []
  const issues = (body as { issues?: unknown }).issues
  if (!Array.isArray(issues)) return []
  return issues
    .map((issue) => {
      if (issue && typeof issue === 'object' && 'message' in issue && typeof issue.message === 'string') {
        return issue.message
      }
      return ''
    })
    .filter((message) => message.length > 0)
}

type CertificatesOptions = { token: string; onLogout: () => void }

/**
 * useCertificateProfiles loads the certificate profiles of the broker.
 *
 * The certificate surface is optional: when MCM has no broker config
 * directory the API answers 503 and the panel reports the surface as
 * disabled instead of failing.
 */
export function useCertificateProfiles(opts: CertificatesOptions) {
  const { token, onLogout } = opts
  const [profiles, setProfiles] = useState<CertificateProfile[]>([])
  const [error, setError] = useState('')
  const [isLoading, setIsLoading] = useState(true)
  const [isDisabled, setIsDisabled] = useState(false)

  const refresh = useCallback(async () => {
    setIsLoading(true)
    try {
      const response = await authenticatedFetch('/api/v1/broker/certificates', { token, onUnauthorized: onLogout })
      if (response.status === 503) {
        setProfiles([])
        setIsDisabled(true)
        setError('')
        return
      }
      if (!response.ok) {
        const body = await response.json().catch(() => null)
        throw new Error(errorMessage(body, 'Failed to load certificate profiles.'))
      }
      const body = await response.json() as CertificateListResponse
      setProfiles(body.profiles ?? [])
      setIsDisabled(false)
      setError('')
    } catch (loadError) {
      if (!isUnauthorizedResponseError(loadError)) {
        setError(loadError instanceof Error ? loadError.message : 'Could not reach the server.')
      }
    } finally {
      setIsLoading(false)
    }
  }, [token, onLogout])

  useEffect(() => {
    void Promise.resolve().then(refresh)
  }, [refresh])

  return { profiles, error, isLoading, isDisabled, refresh }
}

/**
 * useCertificates drives the certificate lifecycle from the UI.
 *
 * Uploading a revision, previewing it and activating it are separate
 * actions: the operator always sees what an activation changes before the
 * broker restarts.
 */
export function useCertificates(opts: CertificatesOptions) {
  const { token, onLogout } = opts
  const { profiles, error: listError, isLoading, isDisabled, refresh } = useCertificateProfiles(opts)
  const [uploadError, setUploadError] = useState('')
  const [uploadIssues, setUploadIssues] = useState<string[]>([])
  const [uploadedRevision, setUploadedRevision] = useState<CertificateRevision | null>(null)
  const [isUploading, setIsUploading] = useState(false)
  const [preview, setPreview] = useState<CertificatePreview | null>(null)
  const [previewError, setPreviewError] = useState('')
  const [isPreviewing, setIsPreviewing] = useState(false)
  const [applyResult, setApplyResult] = useState<CertificateApplyResult | null>(null)
  const [applyError, setApplyError] = useState('')
  const [isApplying, setIsApplying] = useState(false)
  const requestId = useRef(0)

  const uploadRevision = useCallback(async (profile: string, request: CertificateRevisionRequest) => {
    const currentRequest = ++requestId.current
    setUploadError('')
    setUploadIssues([])
    setUploadedRevision(null)
    setPreview(null)
    setApplyResult(null)
    setApplyError('')
    setIsUploading(true)
    try {
      const response = await authenticatedFetch(`/api/v1/broker/certificates/profiles/${encodeURIComponent(profile)}/revisions`, {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(request),
      })
      const body = await response.json().catch(() => null) as CertificateRevision | { error?: string } | null
      if (currentRequest !== requestId.current) return null
      if (!response.ok) {
        const issues = issueMessages(body)
        setUploadIssues(issues)
        setUploadError(errorMessage(body, 'The certificate material was rejected.'))
        return null
      }
      setUploadedRevision(body as CertificateRevision)
      await refresh()
      return body as CertificateRevision
    } catch (error) {
      if (currentRequest === requestId.current && !isUnauthorizedResponseError(error)) {
        setUploadError(error instanceof Error ? error.message : 'Could not reach the server.')
      }
      return null
    } finally {
      if (currentRequest === requestId.current) setIsUploading(false)
    }
  }, [token, onLogout, refresh])

  const requestPreview = useCallback(async (request: CertificateActionRequest) => {
    const currentRequest = ++requestId.current
    setPreviewError('')
    setPreview(null)
    setIsPreviewing(true)
    try {
      const response = await authenticatedFetch('/api/v1/broker/certificates/preview', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(request),
      })
      const body = await response.json().catch(() => null) as CertificatePreview | { error?: string } | null
      if (currentRequest !== requestId.current) return null
      if (!response.ok) {
        setPreviewError(errorMessage(body, 'The certificate preview failed.'))
        return null
      }
      setPreview(body as CertificatePreview)
      return body as CertificatePreview
    } catch (error) {
      if (currentRequest === requestId.current && !isUnauthorizedResponseError(error)) {
        setPreviewError(error instanceof Error ? error.message : 'Could not reach the server.')
      }
      return null
    } finally {
      if (currentRequest === requestId.current) setIsPreviewing(false)
    }
  }, [token, onLogout])

  const applyRevision = useCallback(async (request: CertificateActionRequest) => {
    setApplyError('')
    setApplyResult(null)
    setIsApplying(true)
    try {
      const response = await authenticatedFetch('/api/v1/broker/certificates/apply', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(request),
      })
      const body = await response.json().catch(() => null) as CertificateApplyResult | { error?: string } | null
      if (!response.ok) {
        setApplyError(errorMessage(body, 'The certificate activation failed.'))
        await refresh()
        return null
      }
      setApplyResult(body as CertificateApplyResult)
      await refresh()
      return body as CertificateApplyResult
    } catch (error) {
      if (!isUnauthorizedResponseError(error)) {
        setApplyError(error instanceof Error ? error.message : 'Could not reach the server.')
      }
      return null
    } finally {
      setIsApplying(false)
    }
  }, [token, onLogout, refresh])

  return {
    profiles,
    listError,
    isLoading,
    isDisabled,
    uploadError,
    uploadIssues,
    uploadedRevision,
    isUploading,
    preview,
    previewError,
    isPreviewing,
    applyResult,
    applyError,
    isApplying,
    uploadRevision,
    requestPreview,
    applyRevision,
    refresh,
  }
}
