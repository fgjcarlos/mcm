import { useCallback, useState } from 'react'
import { authenticatedFetch, isForbiddenResponseError, isUnauthorizedResponseError } from '../api/client'
import type {
  BrokerConfig,
  BrokerConfigApplyRequest,
  BrokerConfigPreview,
  CatalogResponse,
} from './types'

type Status = 'idle' | 'loading' | 'previewing' | 'adoption_required' | 'applying'

export type BrokerConfigState = {
  config: BrokerConfig | null
  preview: BrokerConfigPreview | null
  catalog: CatalogResponse | null
  status: Status
  loading: boolean
  error: string
  load: () => Promise<void>
  importPreview: () => Promise<void>
  previewTyped: (body: string) => Promise<void>
  adopt: () => Promise<void>
  apply: (req: BrokerConfigApplyRequest) => Promise<void>
  loadCatalog: () => Promise<void>
}

function describeError(err: unknown, fallback: string): string {
  if (isUnauthorizedResponseError(err)) return ''
  if (isForbiddenResponseError(err)) return (err as Error).message
  if (err instanceof Error) return err.message || fallback
  return fallback
}

export function useBrokerConfig(token: string, onLogout: () => void): BrokerConfigState {
  const [config, setConfig] = useState<BrokerConfig | null>(null)
  const [preview, setPreview] = useState<BrokerConfigPreview | null>(null)
  const [catalog, setCatalog] = useState<CatalogResponse | null>(null)
  const [status, setStatus] = useState<Status>('idle')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const res = await authenticatedFetch('/api/v1/broker/config', { token, onUnauthorized: onLogout })
      if (res.status === 404 || res.status === 503) {
        setConfig(null)
        return
      }
      if (!res.ok) {
        const body = (await res.json().catch(() => null)) as { error?: string } | null
        setError(body?.error ?? 'Failed to load broker configuration.')
        return
      }
      setConfig((await res.json()) as BrokerConfig)
    } catch (err) {
      const msg = describeError(err, 'Could not reach the server.')
      if (msg) setError(msg)
    } finally {
      setLoading(false)
    }
  }, [token, onLogout])

  const importPreview = useCallback(async () => {
    setStatus('previewing')
    setError('')
    try {
      const res = await authenticatedFetch('/api/v1/broker/config/import', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({}),
      })
      if (!res.ok) {
        const body = (await res.json().catch(() => null)) as { error?: string } | null
        setError(body?.error ?? 'Preview failed.')
        setStatus('idle')
        return
      }
      const data = (await res.json()) as BrokerConfigPreview
      setPreview(data)
      if (data.requires_restart) setStatus('adoption_required')
      else setStatus('idle')
    } catch (err) {
      const msg = describeError(err, 'Could not reach the server.')
      if (msg) setError(msg)
      setStatus('idle')
    }
  }, [token, onLogout])

  const previewTyped = useCallback(
    async (body: string) => {
      setStatus('previewing')
      setError('')
      try {
        const res = await authenticatedFetch('/api/v1/broker/config/preview', {
          token,
          onUnauthorized: onLogout,
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ body }),
        })
        if (!res.ok) {
          const errBody = (await res.json().catch(() => null)) as { error?: string } | null
          setError(errBody?.error ?? 'Preview failed.')
          setStatus('idle')
          return
        }
        const data = (await res.json()) as BrokerConfigPreview
        setPreview(data)
        if (data.requires_restart) setStatus('adoption_required')
        else setStatus('idle')
      } catch (err) {
        const msg = describeError(err, 'Could not reach the server.')
        if (msg) setError(msg)
        setStatus('idle')
      }
    },
    [token, onLogout],
  )

  const adopt = useCallback(async () => {
    setError('')
    try {
      await authenticatedFetch('/api/v1/broker/config/adopt', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({}),
      })
      await importPreview()
    } catch (err) {
      const msg = describeError(err, 'Adoption failed.')
      if (msg) setError(msg)
    }
  }, [token, onLogout, importPreview])

  const apply = useCallback(
    async (req: BrokerConfigApplyRequest) => {
      setStatus('applying')
      setError('')
      try {
        const res = await authenticatedFetch('/api/v1/broker/config/apply', {
          token,
          onUnauthorized: onLogout,
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(req),
        })
        if (res.status === 409) {
          const body = (await res.json().catch(() => null)) as { error?: string; directives?: string } | null
          setError(body?.error ?? 'Apply requires broker restart.')
          setStatus('adoption_required')
          return
        }
        if (!res.ok) {
          const body = (await res.json().catch(() => null)) as { error?: string } | null
          setError(body?.error ?? 'Apply failed.')
          setStatus('idle')
          return
        }
        setPreview(null)
        setStatus('idle')
        await load()
      } catch (err) {
        const msg = describeError(err, 'Could not reach the server.')
        if (msg) setError(msg)
        setStatus('idle')
      }
    },
    [token, onLogout, load],
  )

  const loadCatalog = useCallback(async () => {
    try {
      const res = await authenticatedFetch('/api/v1/broker/config/catalog', { token, onUnauthorized: onLogout })
      if (!res.ok) return
      setCatalog((await res.json()) as CatalogResponse)
    } catch (err) {
      if (isUnauthorizedResponseError(err) || isForbiddenResponseError(err)) return
      // Catalog is advisory — don't surface as a panel error.
    }
  }, [token, onLogout])

  return { config, preview, catalog, status, loading, error, load, importPreview, previewTyped, adopt, apply, loadCatalog }
}
