import { useCallback, useEffect, useState } from 'react'
import { authenticatedFetch } from '../api/client'

export type AdminUser = {
  id: number
  username: string
  disabled: boolean
  role: 'viewer' | 'auditor' | 'operator' | 'admin' | string
  mfa_enabled: boolean
  created_at: string
  updated_at: string
}

const tokenStorageKey = 'mcm_admin_token'

export function useAuthSession() {
  const [token, setToken] = useState<string | null>(() =>
    typeof window !== 'undefined' ? window.localStorage.getItem(tokenStorageKey) : null,
  )
  const [currentUser, setCurrentUser] = useState<AdminUser | null>(null)

  const handleLogin = useCallback((newToken: string, user: AdminUser) => {
    window.localStorage.setItem(tokenStorageKey, newToken)
    setCurrentUser(user)
    setToken(newToken)
  }, [])

  const handleLogout = useCallback(() => {
    window.localStorage.removeItem(tokenStorageKey)
    setCurrentUser(null)
    setToken(null)
  }, [])

  useEffect(() => {
    if (!token) return

    let cancelled = false
    authenticatedFetch('/api/v1/auth/me', { token, onUnauthorized: handleLogout })
      .then(async (response) => {
        if (cancelled) return
        if (!response.ok) {
          throw new Error('Failed to verify session.')
        }
        const user = (await response.json()) as AdminUser
        if (!cancelled) {
          setCurrentUser(user)
        }
      })
      .catch(() => {
        if (!cancelled) handleLogout()
      })

    return () => {
      cancelled = true
    }
  }, [token, handleLogout])

  const refreshCurrentUser = useCallback(() => {
    if (!token) return
    let cancelled = false
    authenticatedFetch('/api/v1/auth/me', { token, onUnauthorized: handleLogout })
      .then(async (response) => {
        if (cancelled) return
        if (!response.ok) throw new Error('Failed to refresh session.')
        const user = (await response.json()) as AdminUser
        if (!cancelled) {
          setCurrentUser(user)
        }
      })
      .catch(() => {
        if (!cancelled) handleLogout()
      })
    return () => {
      cancelled = true
    }
  }, [token, handleLogout])

  const isRestoring = token !== null && currentUser === null

  return { token, currentUser, isRestoring, handleLogin, handleLogout, refreshCurrentUser }
}
