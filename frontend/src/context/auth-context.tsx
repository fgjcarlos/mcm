import { createContext, useCallback, useContext, useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import type { AdminUser } from '../types'
import { getMe } from '../lib/api'

const tokenStorageKey = 'mcm_admin_token'

type AuthContextValue = {
  token: string | null
  currentUser: AdminUser | null
  login: (token: string, user: AdminUser) => void
  logout: () => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(() =>
    typeof window !== 'undefined' ? window.localStorage.getItem(tokenStorageKey) : null,
  )
  const [currentUser, setCurrentUser] = useState<AdminUser | null>(null)

  const logout = useCallback(() => {
    window.localStorage.removeItem(tokenStorageKey)
    setCurrentUser(null)
    setToken(null)
  }, [])

  const login = useCallback((newToken: string, user: AdminUser) => {
    window.localStorage.setItem(tokenStorageKey, newToken)
    setCurrentUser(user)
    setToken(newToken)
  }, [])

  useEffect(() => {
    if (!token) return

    let cancelled = false

    getMe(token)
      .then((user) => {
        if (!cancelled) setCurrentUser(user)
      })
      .catch((err: Error & { status?: number }) => {
        if (cancelled) return
        if (err.status === 401) {
          logout()
          return
        }
        logout()
      })

    return () => {
      cancelled = true
    }
  }, [token, logout])

  return (
    <AuthContext.Provider value={{ token, currentUser, login, logout }}>
      {children}
    </AuthContext.Provider>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within an AuthProvider')
  return ctx
}
