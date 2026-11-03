import type { useRecentMFA } from './useRecentMFA'

export default function RecentMFAConfirmation({ state }: { state: ReturnType<typeof useRecentMFA> }) {
  return (
    <form className="space-y-3 rounded-xl border border-amber-300/20 bg-amber-400/5 p-4" onSubmit={(event) => { event.preventDefault(); void state.confirm() }}>
      <p className="text-sm text-slate-200">Sensitive operations require recent MFA. Enable it under Administration → Account, then enter an unused authenticator code.</p>
      <label className="block text-sm text-slate-200">
        MFA code
        <input value={state.code} onChange={(event) => state.setCode(event.target.value)} autoComplete="one-time-code" inputMode="numeric" pattern="[0-9]{6}" maxLength={6} required placeholder="Authenticator code" className="ml-3 rounded-lg border border-white/20 bg-slate-950 p-2 font-mono" />
      </label>
      <button type="submit" disabled={state.busy || !state.code} className="rounded-xl border border-white/20 px-4 py-2 text-sm disabled:opacity-50">{state.busy ? 'Verifying…' : 'Verify MFA'}</button>
      {state.proof ? <p role="status" className="text-sm text-emerald-200">MFA confirmed for this session for up to five minutes.</p> : null}
      {state.error ? <p role="alert" className="text-sm text-rose-200">{state.error}</p> : null}
    </form>
  )
}
