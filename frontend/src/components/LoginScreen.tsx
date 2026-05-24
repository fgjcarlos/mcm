import { useState, type FormEvent } from 'react'
import type { AdminUser } from '../types'
import { login, loginMFA } from '../lib/api'

type Props = {
  onLogin: (token: string, user: AdminUser) => void
}

export function LoginScreen({ onLogin }: Props) {
  const [username, setUsername] = useState<string>('')
  const [password, setPassword] = useState<string>('')
  const [submitting, setSubmitting] = useState<boolean>(false)
  const [error, setError] = useState<string>('')
  const [mfaChallenge, setMfaChallenge] = useState<string>('')
  const [mfaCode, setMfaCode] = useState<string>('')

  const completeMFA = async (challenge: string, code: string) => {
    try {
      const completed = await loginMFA(challenge, code.trim())
      if (!completed.token || !completed.user) {
        setError('MFA verification did not return a session token.')
        return
      }
      onLogin(completed.token, completed.user)
    } catch (err) {
      const typedErr = err as Error & { status?: number }
      if (typedErr.status === 401) {
        setError('Invalid MFA code. Try again or use a recovery code.')
        return
      }
      setError(typedErr.message ?? 'MFA verification failed.')
    }
  }

  const handlePasswordSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting) return
    setSubmitting(true)
    setError('')
    try {
      const body = await login(username.trim(), password)
      if (body.mfa_required && body.mfa_challenge) {
        setMfaChallenge(body.mfa_challenge)
        return
      }
      if (body.token && body.user) {
        onLogin(body.token, body.user)
        return
      }
      setError('Login response was incomplete. Please retry.')
    } catch (err) {
      const typedErr = err as Error & { status?: number }
      if (typedErr.status === 401) {
        setError('Invalid username or password.')
        return
      }
      setError(typedErr.message ?? 'Login failed. Please try again.')
    } finally {
      setSubmitting(false)
    }
  }

  const handleMFASubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting) return
    setSubmitting(true)
    setError('')
    try {
      await completeMFA(mfaChallenge, mfaCode)
    } catch {
      setError('Could not reach the server. Check the MCM service and try again.')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-[radial-gradient(circle_at_top,#1d4e89_0%,#0f172a_38%,#020617_100%)] px-4 py-12 text-slate-100">
      <div className="w-full max-w-md rounded-[2rem] border border-white/10 bg-slate-950/65 p-8 shadow-2xl shadow-slate-950/40 backdrop-blur">
        <div className="border-b border-white/10 pb-6">
          <p className="text-xs font-semibold uppercase tracking-[0.3em] text-cyan-300">MCM</p>
          <h1 className="mt-2 text-3xl font-semibold tracking-tight text-white">
            {mfaChallenge ? 'Verify MFA' : 'Sign in'}
          </h1>
          <p className="mt-2 text-sm text-slate-400">
            {mfaChallenge
              ? 'Enter the 6-digit code from your authenticator app, or one of your recovery codes.'
              : 'Authenticate with an admin user to access the Mosquitto Control Manager dashboard.'}
          </p>
        </div>

        {mfaChallenge ? (
          <form onSubmit={(e) => { void handleMFASubmit(e) }} className="mt-6 space-y-4">
            <label className="block">
              <span className="text-xs font-semibold uppercase tracking-[0.22em] text-slate-300">Code</span>
              <input
                type="text"
                inputMode="text"
                autoComplete="one-time-code"
                required
                autoFocus
                value={mfaCode}
                onChange={(event) => setMfaCode(event.target.value)}
                className="mt-2 w-full rounded-2xl border border-white/10 bg-slate-950/60 px-4 py-3 text-sm tracking-[0.3em] text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              />
            </label>

            {error ? (
              <div role="alert" className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">
                {error}
              </div>
            ) : null}

            <button
              type="submit"
              disabled={submitting}
              className="w-full rounded-2xl bg-cyan-400 px-4 py-3 text-sm font-semibold uppercase tracking-[0.22em] text-slate-950 transition hover:bg-cyan-300 disabled:cursor-not-allowed disabled:bg-cyan-400/50"
            >
              {submitting ? 'Verifying…' : 'Verify and sign in'}
            </button>
            <button
              type="button"
              onClick={() => {
                setMfaChallenge('')
                setMfaCode('')
                setError('')
              }}
              className="w-full text-xs uppercase tracking-[0.22em] text-slate-400 hover:text-white"
            >
              ← Back to sign in
            </button>
          </form>
        ) : (
          <form onSubmit={(e) => { void handlePasswordSubmit(e) }} className="mt-6 space-y-4">
            <label className="block">
              <span className="text-xs font-semibold uppercase tracking-[0.22em] text-slate-300">Username</span>
              <input
                type="text"
                autoComplete="username"
                required
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                className="mt-2 w-full rounded-2xl border border-white/10 bg-slate-950/60 px-4 py-3 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              />
            </label>

            <label className="block">
              <span className="text-xs font-semibold uppercase tracking-[0.22em] text-slate-300">Password</span>
              <input
                type="password"
                autoComplete="current-password"
                required
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                className="mt-2 w-full rounded-2xl border border-white/10 bg-slate-950/60 px-4 py-3 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              />
            </label>

            {error ? (
              <div role="alert" className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">
                {error}
              </div>
            ) : null}

            <button
              type="submit"
              disabled={submitting}
              className="w-full rounded-2xl bg-cyan-400 px-4 py-3 text-sm font-semibold uppercase tracking-[0.22em] text-slate-950 transition hover:bg-cyan-300 disabled:cursor-not-allowed disabled:bg-cyan-400/50"
            >
              {submitting ? 'Signing in…' : 'Sign in'}
            </button>
          </form>
        )}
      </div>
    </div>
  )
}
