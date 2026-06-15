import { useState, type FormEvent } from 'react'
import { QRCodeSVG } from 'qrcode.react'
import type { AdminUser } from '../auth/useAuthSession'
import { setupMFA, verifyMFA, disableMFA, isUnauthorizedResponseError } from './api'

type Props = {
  token: string
  currentUser: AdminUser | null
  onLogout: () => void
  onMFAChange?: () => void
}

type SetupData = {
  otpauth_url: string
  secret: string
  recovery_codes: string[]
}

function formatSecret(secret: string): string {
  return secret.replace(/(.{4})/g, '$1 ').trim()
}

function AccountSecurityPanel({ token, currentUser, onLogout, onMFAChange }: Props) {
  // Enrollment flow state
  const [setupData, setSetupData] = useState<SetupData | null>(null)
  const [setupLoading, setSetupLoading] = useState(false)
  const [setupError, setSetupError] = useState('')

  // Verify step state
  const [verifyCode, setVerifyCode] = useState('')
  const [savedCodesChecked, setSavedCodesChecked] = useState(false)
  const [verifySubmitting, setVerifySubmitting] = useState(false)
  const [verifyError, setVerifyError] = useState('')
  const [enableSuccess, setEnableSuccess] = useState(false)

  // Disable flow state
  const [showDisableForm, setShowDisableForm] = useState(false)
  const [disablePassword, setDisablePassword] = useState('')
  const [disableSubmitting, setDisableSubmitting] = useState(false)
  const [disableError, setDisableError] = useState('')
  const [disableSuccess, setDisableSuccess] = useState(false)

  if (!currentUser) {
    return (
      <section className="mt-8 space-y-4">
        <p className="text-sm text-slate-400">Loading account information…</p>
      </section>
    )
  }

  const handleStartSetup = async () => {
    setSetupLoading(true)
    setSetupError('')
    try {
      const data = await setupMFA(token, onLogout)
      setSetupData(data)
      setVerifyCode('')
      setSavedCodesChecked(false)
      setVerifyError('')
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      setSetupError((err as Error).message)
    } finally {
      setSetupLoading(false)
    }
  }

  const handleCancelSetup = () => {
    setSetupData(null)
    setVerifyCode('')
    setSavedCodesChecked(false)
    setVerifyError('')
    setSetupError('')
  }

  const handleVerify = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (verifySubmitting) return
    setVerifySubmitting(true)
    setVerifyError('')
    try {
      await verifyMFA(token, verifyCode.trim(), onLogout)
      // Clear sensitive data from state immediately after success
      setSetupData(null)
      setVerifyCode('')
      setSavedCodesChecked(false)
      setEnableSuccess(true)
      onMFAChange?.()
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      setVerifyError((err as Error).message)
    } finally {
      setVerifySubmitting(false)
    }
  }

  const handleCopySecret = async () => {
    if (!setupData) return
    await navigator.clipboard.writeText(setupData.secret).catch(() => null)
  }

  const handleCopyCodes = async () => {
    if (!setupData) return
    await navigator.clipboard.writeText(setupData.recovery_codes.join('\n')).catch(() => null)
  }

  const handleDownloadCodes = () => {
    if (!setupData) return
    const blob = new Blob([setupData.recovery_codes.join('\n')], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'mcm-recovery-codes.txt'
    a.click()
    URL.revokeObjectURL(url)
  }

  const handleDisable = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (disableSubmitting) return
    setDisableSubmitting(true)
    setDisableError('')
    try {
      await disableMFA(token, disablePassword, onLogout)
      setDisablePassword('')
      setShowDisableForm(false)
      setDisableSuccess(true)
      onMFAChange?.()
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      setDisableError((err as Error).message)
    } finally {
      setDisableSubmitting(false)
    }
  }

  // --- State A: MFA disabled, no active setup ---
  if (!currentUser.mfa_enabled && !setupData && !enableSuccess) {
    return (
      <section className="mt-8 space-y-4">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Two-factor authentication</p>
          <p className="mt-1 text-sm text-slate-300">MFA is not enabled on your account.</p>
        </div>

        {disableSuccess ? (
          <div role="alert" className="rounded-2xl border border-cyan-300/30 bg-cyan-400/10 p-5">
            <p className="text-xs font-semibold uppercase tracking-[0.18em] text-cyan-200">MFA removed</p>
            <p className="mt-2 text-sm text-slate-300">Two-factor authentication has been removed from your account.</p>
          </div>
        ) : null}

        {setupError ? (
          <div role="alert" className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">
            {setupError}
          </div>
        ) : null}

        <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
          <p className="text-sm text-slate-300">
            Protect your account with a time-based one-time password (TOTP) authenticator app.
            You will need an app such as Google Authenticator, Authy, or 1Password.
          </p>
          <button
            type="button"
            onClick={() => { setDisableSuccess(false); void handleStartSetup() }}
            disabled={setupLoading}
            className="mt-4 rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
          >
            {setupLoading ? 'Setting up…' : 'Enable MFA'}
          </button>
        </div>
      </section>
    )
  }

  // --- State A2: setup in progress (QR + codes + verify) ---
  if (!currentUser.mfa_enabled && setupData) {
    return (
      <section className="mt-8 space-y-6">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Set up two-factor authentication</p>
          <p className="mt-1 text-sm text-slate-300">Scan the QR code or enter the secret manually in your authenticator app.</p>
        </div>

        {/* QR code */}
        <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6 flex flex-col items-start gap-4">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-cyan-300">Step 1 — Scan with your authenticator app</p>
          <div className="rounded-xl bg-white p-3" role="img" aria-label="MFA QR code">
            <QRCodeSVG value={setupData.otpauth_url} size={180} />
          </div>

          <div className="w-full">
            <p className="text-xs uppercase tracking-[0.18em] text-slate-400">Or enter the secret manually</p>
            <div className="mt-2 flex items-center gap-2 flex-wrap">
              <pre className="rounded-xl bg-slate-950/60 px-4 py-2 font-mono text-sm text-cyan-100 select-all">
                {formatSecret(setupData.secret)}
              </pre>
              <button
                type="button"
                onClick={() => { void handleCopySecret() }}
                className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white"
              >
                Copy
              </button>
            </div>
          </div>
        </div>

        {/* Recovery codes */}
        <div className="rounded-2xl border border-amber-300/30 bg-amber-400/10 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">
            Step 2 — Save your recovery codes
          </p>
          <p className="mt-2 text-sm text-slate-300">
            These 10 codes can each be used once if you lose access to your authenticator.
            They will <strong className="text-white">not</strong> be shown again after you complete setup.
          </p>
          <div
            aria-label="Recovery codes"
            className="mt-4 grid grid-cols-2 gap-2"
          >
            {setupData.recovery_codes.map((code) => (
              <pre key={code} className="rounded-xl bg-slate-950/60 px-3 py-2 font-mono text-xs text-amber-100">
                {code}
              </pre>
            ))}
          </div>
          <div className="mt-4 flex gap-2 flex-wrap">
            <button
              type="button"
              onClick={() => { void handleCopyCodes() }}
              className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white"
            >
              Copy all
            </button>
            <button
              type="button"
              onClick={handleDownloadCodes}
              className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white"
            >
              Download
            </button>
          </div>
          <label className="mt-4 flex items-center gap-2 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={savedCodesChecked}
              onChange={(e) => setSavedCodesChecked(e.target.checked)}
              className="h-4 w-4 rounded border-white/20 bg-slate-950/60 accent-cyan-500"
            />
            <span className="text-sm text-slate-300">I have saved my recovery codes in a safe place</span>
          </label>
        </div>

        {/* Verify step */}
        <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-cyan-300">Step 3 — Confirm with a code</p>
          <p className="mt-2 text-sm text-slate-300">
            Enter the 6-digit code from your authenticator app to verify setup.
          </p>
          <form onSubmit={(e) => { void handleVerify(e) }} className="mt-4 space-y-4">
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Authentication code</span>
              <input
                type="text"
                inputMode="numeric"
                autoComplete="one-time-code"
                required
                maxLength={6}
                value={verifyCode}
                onChange={(e) => setVerifyCode(e.target.value.replace(/\D/g, ''))}
                placeholder="000000"
                className="mt-2 w-full max-w-xs rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 font-mono text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              />
            </label>

            {verifyError ? (
              <div role="alert" aria-live="polite" className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">
                {verifyError}
              </div>
            ) : null}

            <div className="flex gap-3">
              <button
                type="submit"
                disabled={verifySubmitting || !savedCodesChecked || verifyCode.length < 6}
                className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
              >
                {verifySubmitting ? 'Verifying…' : 'Confirm & enable'}
              </button>
              <button
                type="button"
                onClick={handleCancelSetup}
                className="rounded-xl border border-white/10 px-4 py-2 text-sm text-slate-300 transition hover:border-white/20 hover:text-white"
              >
                Cancel
              </button>
            </div>
          </form>
        </div>
      </section>
    )
  }

  // --- State A3: just enabled successfully (mfa_enabled not yet reflected) ---
  if (!currentUser.mfa_enabled && enableSuccess) {
    return (
      <section className="mt-8 space-y-4">
        <div className="rounded-2xl border border-emerald-300/30 bg-emerald-400/10 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-emerald-200">MFA enabled</p>
          <p className="mt-2 text-sm text-slate-300">
            Two-factor authentication has been enabled on your account.
            You will need your authenticator app on your next login.
          </p>
        </div>
      </section>
    )
  }

  // --- State B: MFA enabled ---
  return (
    <section className="mt-8 space-y-4">
      <div>
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Two-factor authentication</p>
        <p className="mt-1 text-sm text-slate-300">
          <span className="inline-flex items-center gap-1.5">
            <span className="inline-block h-2 w-2 rounded-full bg-emerald-400" aria-hidden="true" />
            MFA is enabled on your account.
          </span>
        </p>
      </div>

      {disableSuccess ? (
        <div role="alert" className="rounded-2xl border border-emerald-300/30 bg-emerald-400/10 p-5">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-emerald-200">MFA disabled</p>
          <p className="mt-2 text-sm text-slate-300">Two-factor authentication has been removed from your account.</p>
        </div>
      ) : null}

      {!showDisableForm ? (
        <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
          <p className="text-sm text-slate-300">
            Your account is protected with a TOTP authenticator app.
          </p>
          <button
            type="button"
            onClick={() => {
              setShowDisableForm(true)
              setDisableError('')
              setDisablePassword('')
            }}
            className="mt-4 rounded-xl border border-rose-300/20 bg-rose-400/10 px-4 py-2 text-sm font-semibold text-rose-200 transition hover:bg-rose-400/20"
          >
            Disable MFA
          </button>
        </div>
      ) : (
        <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-rose-300">Disable two-factor authentication</p>
          <p className="mt-2 text-sm text-slate-300">
            Enter your account password to confirm you want to remove MFA protection.
          </p>
          <form onSubmit={(e) => { void handleDisable(e) }} className="mt-4 space-y-4">
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Account password</span>
              <input
                type="password"
                required
                autoComplete="current-password"
                value={disablePassword}
                onChange={(e) => setDisablePassword(e.target.value)}
                placeholder="Enter your password"
                className="mt-2 w-full max-w-xs rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              />
            </label>

            {disableError ? (
              <div role="alert" aria-live="polite" className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">
                {disableError}
              </div>
            ) : null}

            <div className="flex gap-3">
              <button
                type="submit"
                disabled={disableSubmitting}
                className="rounded-xl border border-rose-300/20 bg-rose-400/10 px-4 py-2 text-sm font-semibold text-rose-200 transition hover:bg-rose-400/20 disabled:opacity-50"
              >
                {disableSubmitting ? 'Disabling…' : 'Confirm disable'}
              </button>
              <button
                type="button"
                onClick={() => {
                  setShowDisableForm(false)
                  setDisableError('')
                  setDisablePassword('')
                }}
                className="rounded-xl border border-white/10 px-4 py-2 text-sm text-slate-300 transition hover:border-white/20 hover:text-white"
              >
                Cancel
              </button>
            </div>
          </form>
        </div>
      )}
    </section>
  )
}

export default AccountSecurityPanel
