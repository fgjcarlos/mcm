import { createContext, useCallback, useContext, useEffect, useState } from 'react'
import { isForbiddenResponseError } from '../api/client'

export type Proof = { session: string; value: string; expires: number }
type Store = [Proof | null, (proof: Proof | null) => void]

// Mirrors auth.ActionMFATTL (internal/auth/action_mfa.go:12); used only if the server omits expires_at.
const ACTION_MFA_TTL_MS = 5 * 60_000

// One proof for the signed-in session, shared by every panel. Without a
// provider (isolated panel tests) each hook keeps its own proof.
export const RecentMFAContext = createContext<Store | null>(null)

// requireRecentMFA answers 403 "recent MFA verification required" (internal/server/recent_mfa.go).
export function isMFARejection(err: unknown) {
  return isForbiddenResponseError(err) && (err as Error).message === 'recent MFA verification required'
}

export function useRecentMFA(token: string, onLogout: () => void) {
  const shared = useContext(RecentMFAContext)
  const local = useState<Proof | null>(null)
  const [confirmation, setConfirmation] = shared ?? local
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // Render must not read the clock, so read it once per mount: a proof that expired while
  // no panel was mounted is invalid on arrival; while mounted, the timer below clears it.
  const [mountedAt] = useState(Date.now)
  const valid = confirmation?.session === token && confirmation.expires > mountedAt
  const proof = valid ? confirmation.value : ''
  const expires = valid ? confirmation.expires : 0
  const clear = useCallback(() => setConfirmation(null), [setConfirmation])

  useEffect(() => {
    if (!confirmation) return
    const timer = window.setTimeout(() => setConfirmation(null), Math.max(0, confirmation.expires - Date.now()))
    return () => window.clearTimeout(timer)
  }, [confirmation, setConfirmation])

  async function confirm() {
    setBusy(true)
    setError('')
    try {
      // An invalid TOTP also returns 401. Unlike an expired session, it must
      // not log the operator out and discard their unsaved configuration.
      const response = await fetch('/api/v1/auth/mfa/step-up', {
        method: 'POST',
        headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ code: code.trim() }),
      })
      const body = await response.json() as { proof?: string; expires_at?: string; error?: string }
      if (!response.ok) {
        if (response.status === 401 && body.error !== 'invalid or reused mfa code') onLogout()
        throw new Error(body.error || 'MFA verification failed.')
      }
      const expires = body.expires_at ? Date.parse(body.expires_at) : Date.now() + ACTION_MFA_TTL_MS
      if (!body.proof || !Number.isFinite(expires) || expires <= Date.now()) throw new Error('Invalid MFA confirmation response.')
      setConfirmation({ session: token, value: body.proof, expires })
      setCode('')
    } catch (err) {
      setConfirmation(null)
      setError(err instanceof Error ? err.message : 'MFA verification failed.')
    } finally {
      setBusy(false)
    }
  }

  return { proof, expires, code, setCode, busy, error, confirm, clear }
}
