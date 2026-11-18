import { useState, type FormEvent } from 'react'
import { QRCodeSVG } from 'qrcode.react'
import type { AdminUser } from '../auth/useAuthSession'
import { setupMFA, verifyMFA, disableMFA, isUnauthorizedResponseError } from './api'
import RecentMFAConfirmation from './RecentMFAConfirmation'
import { useRecentMFA } from './useRecentMFA'
import { Section } from '../ui/primitives'
import { bodyText, fieldLabel, input, lampDot, leverDanger, leverPrimary, leverSecondary, notice } from '../ui/styles'

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

  // Recent-MFA proof for the sensitive operations that will require it.
  const recentMFA = useRecentMFA(token, onLogout)

  if (!currentUser) {
    return (
      <section className="mt-8 space-y-4">
        <p className={bodyText}>Loading account information…</p>
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
      <Section title="Two-factor authentication" description="MFA is not enabled on your account." ruled={false} className="mt-8">
        {disableSuccess ? (
          <div role="alert" className={`mt-4 ${notice.neutral}`}>
            <p className="font-semibold text-enamel-bright">MFA removed</p>
            <p className="mt-1">Two-factor authentication has been removed from your account.</p>
          </div>
        ) : null}

        {setupError ? (
          <div role="alert" className={`mt-4 ${notice.failed}`}>
            {setupError}
          </div>
        ) : null}

        <p className={`mt-4 max-w-[68ch] ${bodyText}`}>
          Protect your account with a time-based one-time password (TOTP) authenticator app.
          You will need an app such as Google Authenticator, Authy, or 1Password.
        </p>
        <button
          type="button"
          onClick={() => { setDisableSuccess(false); void handleStartSetup() }}
          disabled={setupLoading}
          className={`mt-4 ${leverPrimary}`}
        >
          {setupLoading ? 'Setting up…' : 'Enable MFA'}
        </button>
      </Section>
    )
  }

  // --- State A2: setup in progress (QR + codes + verify) ---
  if (!currentUser.mfa_enabled && setupData) {
    return (
      <Section
        title="Set up two-factor authentication"
        description="Scan the QR code or enter the secret manually in your authenticator app."
        ruled={false}
        className="mt-8"
      >
        <div className="mt-6 space-y-8">
          {/* QR code */}
          <Section level={4} title="Step 1 — Scan with your authenticator app">
            <div className="mt-4 inline-block rounded-plate bg-white p-3" role="img" aria-label="MFA QR code">
              <QRCodeSVG value={setupData.otpauth_url} size={180} />
            </div>

            <div className="mt-4">
              <p className={fieldLabel}>Or enter the secret manually</p>
              <div className="mt-2 flex flex-wrap items-center gap-2">
                <pre className="select-all rounded-plate border border-track-strong bg-frame-raised px-4 py-2 font-mono text-sm text-enamel-bright">
                  {formatSecret(setupData.secret)}
                </pre>
                <button
                  type="button"
                  onClick={() => { void handleCopySecret() }}
                  className={leverSecondary}
                >
                  Copy
                </button>
              </div>
            </div>
          </Section>

          {/* Recovery codes */}
          <Section level={4} title="Step 2 — Save your recovery codes">
            <div className={`mt-3 ${notice.caution}`}>
              <p>
                These 10 codes can each be used once if you lose access to your authenticator.
                They will <strong className="text-enamel-bright">not</strong> be shown again after you complete setup.
              </p>
              <div
                aria-label="Recovery codes"
                className="mt-4 grid max-w-md grid-cols-2 gap-2"
              >
                {setupData.recovery_codes.map((code) => (
                  <pre key={code} className="rounded-plate border border-track-strong bg-frame-raised px-3 py-2 font-mono text-xs text-enamel-bright">
                    {code}
                  </pre>
                ))}
              </div>
              <div className="mt-4 flex flex-wrap gap-2">
                <button
                  type="button"
                  onClick={() => { void handleCopyCodes() }}
                  className={leverSecondary}
                >
                  Copy all
                </button>
                <button
                  type="button"
                  onClick={handleDownloadCodes}
                  className={leverSecondary}
                >
                  Download
                </button>
              </div>
              <label className="mt-4 flex cursor-pointer select-none items-center gap-2">
                <input
                  type="checkbox"
                  checked={savedCodesChecked}
                  onChange={(e) => setSavedCodesChecked(e.target.checked)}
                  className="size-4 accent-lamp"
                />
                <span className="text-sm text-enamel">I have saved my recovery codes in a safe place</span>
              </label>
            </div>
          </Section>

          {/* Verify step */}
          <Section
            level={4}
            title="Step 3 — Confirm with a code"
            description="Enter the 6-digit code from your authenticator app to verify setup."
          >
            <form onSubmit={(e) => { void handleVerify(e) }} className="mt-4 space-y-5">
              <label className="block">
                <span className={fieldLabel}>Authentication code</span>
                <input
                  type="text"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  required
                  maxLength={6}
                  value={verifyCode}
                  onChange={(e) => setVerifyCode(e.target.value.replace(/\D/g, ''))}
                  placeholder="000000"
                  className={`mt-2 w-full max-w-xs font-mono tracking-[0.2em] ${input}`}
                />
              </label>

              {verifyError ? (
                <div role="alert" aria-live="polite" className={notice.failed}>
                  {verifyError}
                </div>
              ) : null}

              <div className="flex flex-wrap items-center gap-3">
                <button
                  type="submit"
                  disabled={verifySubmitting || !savedCodesChecked || verifyCode.length < 6}
                  className={leverPrimary}
                >
                  {verifySubmitting ? 'Verifying…' : 'Confirm & enable'}
                </button>
                <button
                  type="button"
                  onClick={handleCancelSetup}
                  className={leverSecondary}
                >
                  Cancel
                </button>
              </div>
            </form>
          </Section>
        </div>
      </Section>
    )
  }

  // --- State A3: just enabled successfully (mfa_enabled not yet reflected) ---
  if (!currentUser.mfa_enabled && enableSuccess) {
    return (
      <section className="mt-8">
        <div className={notice.neutral}>
          <p className="font-semibold text-enamel-bright">MFA enabled</p>
          <p className="mt-1">
            Two-factor authentication has been enabled on your account.
            You will need your authenticator app on your next login.
          </p>
        </div>
      </section>
    )
  }

  // --- State B: MFA enabled ---
  return (
    <Section
      title="Two-factor authentication"
      description={
        <span className="inline-flex items-center gap-2">
          <span aria-hidden="true" className={`size-2.5 shrink-0 rounded-full ${lampDot.neutral}`} />
          MFA is enabled on your account.
        </span>
      }
      ruled={false}
      className="mt-8"
    >
      <div className="mt-4 space-y-6">
        {disableSuccess ? (
          <div role="alert" className={notice.neutral}>
            <p className="font-semibold text-enamel-bright">MFA disabled</p>
            <p className="mt-1">Two-factor authentication has been removed from your account.</p>
          </div>
        ) : null}

        <RecentMFAConfirmation state={recentMFA} />

        {!showDisableForm ? (
          <div className="border-t border-track pt-4">
            <p className={bodyText}>
              Your account is protected with a TOTP authenticator app.
            </p>
            <button
              type="button"
              onClick={() => {
                setShowDisableForm(true)
                setDisableError('')
                setDisablePassword('')
              }}
              className={`mt-4 ${leverDanger}`}
            >
              Disable MFA
            </button>
          </div>
        ) : (
          <Section
            level={4}
            title="Disable two-factor authentication"
            description="Enter your account password to confirm you want to remove MFA protection."
          >
            <form onSubmit={(e) => { void handleDisable(e) }} className="mt-4 space-y-5">
              <label className="block">
                <span className={fieldLabel}>Account password</span>
                <input
                  type="password"
                  required
                  autoComplete="current-password"
                  value={disablePassword}
                  onChange={(e) => setDisablePassword(e.target.value)}
                  placeholder="Enter your password"
                  className={`mt-2 w-full max-w-xs ${input}`}
                />
              </label>

              {disableError ? (
                <div role="alert" aria-live="polite" className={notice.failed}>
                  {disableError}
                </div>
              ) : null}

              <div className="flex flex-wrap items-center gap-3">
                <button
                  type="submit"
                  disabled={disableSubmitting}
                  className={leverDanger}
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
                  className={leverSecondary}
                >
                  Cancel
                </button>
              </div>
            </form>
          </Section>
        )}
      </div>
    </Section>
  )
}

export default AccountSecurityPanel
