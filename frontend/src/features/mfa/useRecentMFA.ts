import { useEffect, useState } from 'react'

type Proof = { session: string; value: string; expires: number }

export function useRecentMFA(token: string, onLogout: () => void) {
  const [confirmation, setConfirmation] = useState<Proof | null>(null)
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const proof = confirmation?.session === token ? confirmation.value : ''

  useEffect(() => {
    if (!confirmation) return
    const timer = window.setTimeout(() => setConfirmation(null), Math.max(0, confirmation.expires - Date.now()))
    return () => window.clearTimeout(timer)
  }, [confirmation])

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
      const expires = Date.parse(body.expires_at ?? '')
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

  return { proof, code, setCode, busy, error, confirm }
}
