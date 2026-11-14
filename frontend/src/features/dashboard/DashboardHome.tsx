import type { ReactNode } from 'react'
import { can, requiredRoleFor } from '../auth/permissions'
import { PendingChangesAction } from '../deploy/PendingChangesAction'
import type { BrokerConnectionStatus, BrokerLog, BrokerStatusFacts, BrokerStreamState } from '../broker/useBrokerStream'
import { logsPath, matchesLogFilter, type LogFilter } from '../live-feed/logFilter'
import { plural } from '../live-feed/feedText'

// The operational part of the dashboard home (issue #354): broker health and
// how fresh it is, pending changes with the next allowed action, and the way
// to connect an application. Recent traffic is rendered after it.
// Warnings backed by diagnostic events in the live window link to the Logs
// view with the matching filter already applied.

type BrokerHealth = 'checking' | 'unknown' | 'disconnected' | 'idle' | 'active'

function brokerHealth(facts: BrokerStatusFacts, status: BrokerConnectionStatus, messageCount: number): BrokerHealth {
  if (facts.load === 'loading') return 'checking'
  if (facts.load === 'failed') return 'unknown'
  if (status !== 'connected') return 'disconnected'
  return messageCount > 0 ? 'active' : 'idle'
}

function formatAge(value: string | undefined, now = Date.now()) {
  if (!value) return undefined
  const seconds = Math.max(0, Math.floor((now - Date.parse(value)) / 1000))
  if (seconds < 60) return 'Less than a minute ago'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} h ago`
  return new Date(value).toLocaleString()
}

const mcmProblems: LogFilter = { source: 'mcm', severities: ['warning', 'error'], text: '' }
const mosquittoProblems: LogFilter = { source: 'mosquitto_log', severities: ['warning', 'error'], text: '' }

const streamLabel: Record<BrokerStreamState, string> = {
  connecting: 'Connecting',
  connected: 'Live',
  reconnecting: 'Reconnecting',
  disconnected: 'Stopped',
}

const healthCopy: Record<BrokerHealth, { title: string; tone: string }> = {
  checking: { title: 'Checking broker status…', tone: 'border-white/10 bg-white/[0.04] text-slate-200' },
  unknown: { title: 'Status unavailable', tone: 'border-amber-300/30 bg-amber-400/10 text-amber-100' },
  disconnected: { title: 'Disconnected', tone: 'border-rose-300/30 bg-rose-400/10 text-rose-100' },
  idle: { title: 'Connected · no traffic', tone: 'border-emerald-300/25 bg-emerald-400/10 text-emerald-100' },
  active: { title: 'Connected · receiving traffic', tone: 'border-emerald-300/25 bg-emerald-400/10 text-emerald-100' },
}

type DashboardHomeProps = {
  role: string
  brokerStatus: BrokerConnectionStatus
  streamState: BrokerStreamState
  statusFacts: BrokerStatusFacts
  messageCount: number
  windowSeconds: number
  deployPending: boolean
  logs: BrokerLog[]
  onOpenPanel: (id: 'deploy') => void
  onOpenLogs: (filter: LogFilter) => void
  onConnectApplication: () => void
}

function DashboardHome({ role, brokerStatus, streamState, statusFacts, messageCount, windowSeconds, deployPending, logs, onOpenPanel, onOpenLogs, onConnectApplication }: DashboardHomeProps) {
  const health = brokerHealth(statusFacts, brokerStatus, messageCount)
  // MCM events explain a broker MCM cannot reach or read; while it is
  // connected they are not tied to its health.
  const mcmProblemCount = health === 'disconnected' || health === 'unknown' ? logs.filter((log) => matchesLogFilter(log, mcmProblems)).length : 0
  const mosquittoProblemCount = logs.filter((log) => matchesLogFilter(log, mosquittoProblems)).length
  const windowMinutes = Math.round(windowSeconds / 60)
  const address = statusFacts.address || 'the configured address'

  return (
    <div className="mt-8 grid gap-4 lg:grid-cols-[1.2fr_1fr_1fr]">
      <HomeCard label="Broker health">
        <p className={`mt-4 inline-flex rounded-full border px-3 py-1 text-sm font-semibold ${healthCopy[health].tone}`}>{healthCopy[health].title}</p>
        <p className="mt-3 text-sm leading-6 text-slate-300">
          {health === 'checking' ? 'Reading the broker status from MCM.' : null}
          {health === 'unknown' ? 'MCM could not read the broker status. This does not mean the broker is down.' : null}
          {health === 'disconnected' ? <>MCM cannot reach the broker at {address}. Clients cannot publish or subscribe through it until it is back.</> : null}
          {health === 'idle' ? <>The broker is reachable, but no messages were observed in the last {windowMinutes} minutes. No traffic is not a disconnection.</> : null}
          {health === 'active' ? <>The broker is reachable and clients are publishing: {messageCount} messages observed in the last {windowMinutes} minutes.</> : null}
        </p>
        <dl className="mt-4 grid gap-2 sm:grid-cols-2">
          <HomeFact label="Address" value={statusFacts.address || 'Unknown'} />
          <HomeFact label="Status reported" value={formatAge(statusFacts.observedAt) ?? 'Unknown'} />
          <HomeFact label="Last message" value={formatAge(statusFacts.lastMessageAt) ?? 'None observed yet'} />
          <HomeFact label="Live updates" value={streamLabel[streamState]} />
        </dl>
        {mcmProblemCount > 0 || mosquittoProblemCount > 0 ? (
          <section aria-label="Diagnostic warnings" className="mt-4 space-y-3">
            {mcmProblemCount > 0 ? (
              <HomeWarning text={`MCM recorded ${plural(mcmProblemCount, 'warning or error', 'warnings or errors')} in the live window.`} filter={mcmProblems} onOpenLogs={onOpenLogs}>
                View {plural(mcmProblemCount, 'related MCM event', 'related MCM events')}
              </HomeWarning>
            ) : null}
            {mosquittoProblemCount > 0 ? (
              <HomeWarning text={`Mosquitto logged ${plural(mosquittoProblemCount, 'warning or error', 'warnings or errors')} in the live window.`} filter={mosquittoProblems} onOpenLogs={onOpenLogs}>
                View {plural(mosquittoProblemCount, 'Mosquitto log entry', 'Mosquitto log entries')}
              </HomeWarning>
            ) : null}
          </section>
        ) : null}
      </HomeCard>

      <HomeCard label="Pending changes">
        {deployPending ? (
          <>
            <p className="mt-4 inline-flex rounded-full border border-amber-300/30 bg-amber-400/10 px-3 py-1 text-sm font-semibold text-amber-100">Saved, not applied</p>
            <p className="mt-3 text-sm leading-6 text-slate-300">User or ACL changes are saved in MCM, but Mosquitto does not use them yet.</p>
            <PendingChangesAction role={role} onReview={() => onOpenPanel('deploy')} />
          </>
        ) : (
          <>
            <p className="mt-4 inline-flex rounded-full border border-white/10 bg-white/[0.04] px-3 py-1 text-sm font-semibold text-slate-200">Nothing pending in this browser</p>
            <p className="mt-3 text-sm leading-6 text-slate-300">Changes saved from another browser are not tracked here.</p>
          </>
        )}
      </HomeCard>

      <HomeCard label="Connect an application">
        <p className="mt-4 text-sm leading-6 text-slate-300">Create its MQTT user and access rule, apply them, and test the connection in one guided flow.</p>
        {can(role, 'mqttUser.write') ? (
          <HomeAction onClick={onConnectApplication}>Connect an application</HomeAction>
        ) : (
          <p className="mt-2 text-sm leading-6 text-slate-300">Connecting an application needs the {requiredRoleFor('mqttUser.write')} role or higher.</p>
        )}
      </HomeCard>
    </div>
  )
}

function HomeCard({ label, children }: { label: string; children: ReactNode }) {
  return (
    <section aria-label={label} className="rounded-[1.75rem] border border-white/10 bg-slate-900/70 p-6">
      <h2 className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">{label}</h2>
      {children}
    </section>
  )
}

function HomeFact({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl border border-white/10 bg-slate-950/35 px-3 py-2">
      <dt className="text-[11px] uppercase tracking-[0.18em] text-slate-400">{label}</dt>
      <dd className="mt-1 break-all font-mono text-sm text-slate-100">{value}</dd>
    </div>
  )
}

function HomeWarning({ text, filter, onOpenLogs, children }: { text: string; filter: LogFilter; onOpenLogs: (filter: LogFilter) => void; children: ReactNode }) {
  return (
    <div className="rounded-xl border border-amber-300/30 bg-amber-400/10 px-3 py-2 text-sm leading-6 text-amber-50">
      <p>{text}</p>
      <a
        href={logsPath(filter)}
        onClick={(event) => {
          // Leave modified or non-primary clicks (new tab, new window) to the browser.
          if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
          event.preventDefault()
          onOpenLogs(filter)
        }}
        className="font-semibold text-amber-100 underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-amber-200"
      >
        {children}
      </a>
    </div>
  )
}

function HomeAction({ onClick, children }: { onClick: () => void; children: ReactNode }) {
  return (
    <button type="button" onClick={onClick} className="mt-4 rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 focus-visible:outline focus-visible:outline-2 focus-visible:outline-cyan-300">
      {children}
    </button>
  )
}

export default DashboardHome
