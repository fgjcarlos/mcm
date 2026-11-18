import type { ReactNode } from 'react'
import { can, requiredRoleFor } from '../auth/permissions'
import { PendingChangesAction } from '../deploy/PendingChangesAction'
import type { BrokerConnectionStatus, BrokerLog, BrokerStatusFacts, BrokerStreamState } from '../broker/useBrokerStream'
import { logsPath, matchesLogFilter, type LogFilter } from '../live-feed/logFilter'
import { plural } from '../live-feed/feedText'
import { Fact, Lamp, Section } from '../ui/primitives'
import { bodyText, leverSecondary, notice, textLink, type LampTone } from '../ui/styles'

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

// Connected is reachable, not verified: an enamel lamp, never green; amber when it is not.
const healthCopy: Record<BrokerHealth, { title: string; tone: LampTone }> = {
  checking: { title: 'Checking broker status…', tone: 'off' },
  unknown: { title: 'Status unavailable', tone: 'caution' },
  disconnected: { title: 'Disconnected', tone: 'caution' },
  idle: { title: 'Connected · no traffic', tone: 'neutral' },
  active: { title: 'Connected · receiving traffic', tone: 'neutral' },
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
    <div className="mt-8 grid gap-y-6 lg:grid-cols-[1.2fr_1fr_1fr] lg:divide-x lg:divide-track">
      <HomeSection label="Broker health">
        <p className="mt-3"><Lamp tone={healthCopy[health].tone}>{healthCopy[health].title}</Lamp></p>
        <p className={`mt-2 ${bodyText}`}>
          {health === 'checking' ? 'Reading the broker status from MCM.' : null}
          {health === 'unknown' ? 'MCM could not read the broker status. This does not mean the broker is down.' : null}
          {health === 'disconnected' ? <>MCM cannot reach the broker at {address}. Clients cannot publish or subscribe through it until it is back.</> : null}
          {health === 'idle' ? <>The broker is reachable, but no messages were observed in the last {windowMinutes} minutes. No traffic is not a disconnection.</> : null}
          {health === 'active' ? <>The broker is reachable and clients are publishing: {messageCount} messages observed in the last {windowMinutes} minutes.</> : null}
        </p>
        <dl className="mt-4 grid gap-x-6 gap-y-3 border-t border-track pt-4 sm:grid-cols-2">
          <Fact label="Address" value={<span className="break-all">{statusFacts.address || 'Unknown'}</span>} />
          <Fact label="Status reported" value={formatAge(statusFacts.observedAt) ?? 'Unknown'} />
          <Fact label="Last message" value={formatAge(statusFacts.lastMessageAt) ?? 'None observed yet'} />
          <Fact label="Live updates" value={streamLabel[streamState]} />
        </dl>
        {mcmProblemCount > 0 || mosquittoProblemCount > 0 ? (
          <section aria-label="Diagnostic warnings" className={`mt-4 space-y-3 ${notice.caution}`}>
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
      </HomeSection>

      <HomeSection label="Pending changes">
        {deployPending ? (
          <>
            <p className="mt-3"><Lamp tone="caution">Saved, not applied</Lamp></p>
            <p className={`mt-2 ${bodyText}`}>User or ACL changes are saved in MCM, but Mosquitto does not use them yet.</p>
            <PendingChangesAction role={role} onReview={() => onOpenPanel('deploy')} />
          </>
        ) : (
          <>
            <p className="mt-3"><Lamp tone="off">Nothing pending in this browser</Lamp></p>
            <p className={`mt-2 ${bodyText}`}>Changes saved from another browser are not tracked here.</p>
          </>
        )}
      </HomeSection>

      <HomeSection label="Connect an application">
        <p className={`mt-3 ${bodyText}`}>Create its MQTT user and access rule, apply them, and test the connection in one guided flow.</p>
        {can(role, 'mqttUser.write') ? (
          <HomeAction onClick={onConnectApplication}>Connect an application</HomeAction>
        ) : (
          <p className={`mt-2 ${bodyText}`}>Connecting an application needs the {requiredRoleFor('mqttUser.write')} role or higher.</p>
        )}
      </HomeSection>
    </div>
  )
}

// A home section: hairline-ruled when stacked, divided by vertical hairlines on wide screens.
function HomeSection({ label, children }: { label: string; children: ReactNode }) {
  return <Section title={label} className="lg:border-t-0 lg:px-8 lg:pt-0 lg:first:pl-0 lg:last:pr-0">{children}</Section>
}

function HomeWarning({ text, filter, onOpenLogs, children }: { text: string; filter: LogFilter; onOpenLogs: (filter: LogFilter) => void; children: ReactNode }) {
  return (
    <div>
      <p>{text}</p>
      <a
        href={logsPath(filter)}
        onClick={(event) => {
          // Leave modified or non-primary clicks (new tab, new window) to the browser.
          if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
          event.preventDefault()
          onOpenLogs(filter)
        }}
        className={`font-semibold ${textLink}`}
      >
        {children}
      </a>
    </div>
  )
}

function HomeAction({ onClick, children }: { onClick: () => void; children: ReactNode }) {
  return (
    <button type="button" onClick={onClick} className={`mt-4 ${leverSecondary}`}>
      {children}
    </button>
  )
}

export default DashboardHome
