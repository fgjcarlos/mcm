import { bodyText, fieldLabel, input, leverSecondary, notice, textLink } from '../ui/styles'
import type { useRecentMFA } from './useRecentMFA'

export default function RecentMFAConfirmation({ state }: { state: ReturnType<typeof useRecentMFA> }) {
  // A valid shared proof replaces the form, so the operator knows why no code is asked.
  // Confirmed MFA is a session fact, not a verified activation, so it stays neutral.
  if (state.proof) {
    const until = new Date(state.expires).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    return <p role="status" className={bodyText}>MFA confirmed for this session — valid until {until}, so no code is asked.</p>
  }
  return (
    <form className={`space-y-3 ${notice.neutral}`} onSubmit={(event) => { event.preventDefault(); void state.confirm() }}>
      {state.enrolled ? (
        <p>Sensitive operations require recent MFA. Enter an unused authenticator code.</p>
      ) : (
        // A new tab keeps this panel mounted, so its review survives the trip to Account.
        <p>
          Sensitive operations require recent MFA, and MFA is not enabled on your account.{' '}
          <a href="/account" target="_blank" rel="noopener noreferrer" className={textLink}>Open Panel administration → Account in a new tab</a>{' '}
          to enable it, then come back to this tab and enter a code. This review stays as it is; nothing is applied until you confirm it.
        </p>
      )}
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-2">
          <span className={fieldLabel}>MFA code</span>
          <input value={state.code} onChange={(event) => state.setCode(event.target.value)} autoComplete="one-time-code" inputMode="numeric" pattern="[0-9]{6}" maxLength={6} required placeholder="Authenticator code" className={`${input} h-10 w-48 font-mono tracking-[0.2em] placeholder:tracking-normal lg:h-auto`} />
        </label>
        <button type="submit" disabled={state.busy || !state.code} className={`${leverSecondary} lg:h-[2.375rem]`}>{state.busy ? 'Verifying…' : 'Verify MFA'}</button>
      </div>
      {state.error ? <p role="alert" className="text-sm text-danger-text">{state.error}</p> : null}
    </form>
  )
}
