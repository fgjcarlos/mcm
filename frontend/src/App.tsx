import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import MQTTUsersPanel from './features/mqtt-users/MQTTUsersPanel'
import AccountSecurityPanel from './features/mfa/AccountSecurityPanel'
import RecentMFAConfirmation from './features/mfa/RecentMFAConfirmation'
import { useRecentMFA } from './features/mfa/useRecentMFA'
import AdminUsersPanel from './features/admin-users/AdminUsersPanel'
import ListenersPanel from './features/broker-config/ListenersPanel'
import CertificatesPanel from './features/broker-config/CertificatesPanel'
import BrokerConfigPanel from './features/broker-config/BrokerConfigPanel'
import SettingsPanel from './features/broker-config/SettingsPanel'
import BridgesPanel from './features/broker-config/BridgesPanel'
import PluginsPanel from './features/broker-config/PluginsPanel'
import DynSecPanel from './features/broker-config/DynSecPanel'
import { useAuthSession, type AdminUser } from './features/auth/useAuthSession'
import { authenticatedFetch, isForbiddenResponseError, isUnauthorizedResponseError } from './features/api/client'
import { can, permissionTitle, ROLE_RANK, type Role } from './features/auth/permissions'
import { DashboardFrame, type NavItem } from './features/dashboard/DashboardFrame'
import DashboardHome from './features/dashboard/DashboardHome'
import ACLSourcePanel from './features/acl/ACLSourcePanel'
import EffectivePermissionsPanel from './features/acl/EffectivePermissionsPanel'
import { LogsPanel } from './features/live-feed/LogsPanel'
import { serializeLogFilterQuery } from './features/live-feed/logFilter'
import { LiveFeedChrome } from './features/live-feed/LiveFeedChrome'
import { emptyFeedText } from './features/live-feed/feedText'
import { useLiveFeed } from './features/live-feed/useLiveFeed'
import { isTopicFilterActive, isTopicPatternValid, matchesTopicFilter } from './features/live-feed/topicFilter'
import { SeverityBadge } from './features/live-feed/SeverityBadge'
import { PERMISSION_LABELS, SELECTOR_LABELS, type ACLPermission, type ACLRule, type ACLScope, type ACLSelector } from './features/acl/types'
import {
  defaultClientNote,
  liveTopicWindow,
  useBrokerStream,
  type BrokerLog,
  type BrokerStreamState,
  type LiveTopicMessage,
  type BrokerRatePoint,
  type BrokerTrafficItem,
  type BrokerTrafficMetrics,
  type SchemaValidation,
  type SparkplugDecodedPayload,
  type SparkplugMetadata,
  type TopicMessage,
} from './features/broker/useBrokerStream'

type AuditEvent = {
  id: number
  occurred_at: string
  actor: string
  action: string
  resource_type: string
  resource_id?: string
  result: 'success' | 'failure' | string
  metadata?: Record<string, unknown>
}

type SecurityEvent = {
  id: number
  category: string
  reason: string
  username?: string
  source_ip?: string
  method?: string
  path?: string
  observed_at: string
}

type Deployment = {
  id: number | string
  status: 'applied' | 'rolled_back' | 'rollback_failed' | string
  message?: string
  created_at: string
}

type ListenerACLFilePreview = {
  listener_id: string
  path: string
  body?: string
  diff?: string
  at_risk_entries?: string[]
  preserved_entries?: string[]
}

type DeployPreview = {
  revision_id: string
  acl_diff: string
  passwd_diff: string
  orphan_rules?: ACLRule[]
  at_risk_entries?: string[]
  preserved_entries?: string[]
  composition_warnings?: string[]
  listener_acl_files?: ListenerACLFilePreview[]
  removed_acl_files?: string[]
  scope_issues?: string[]
  scope_pending_activation?: string[]
  has_changes: boolean
}

const deployPendingStorageKey = 'mcm_deploy_pending'

function readDeployPending() {
  try {
    return window.localStorage.getItem(deployPendingStorageKey) === '1'
  } catch {
    return false
  }
}

function persistDeployPending(pending: boolean) {
  try {
    if (pending) {
      window.localStorage.setItem(deployPendingStorageKey, '1')
    } else {
      window.localStorage.removeItem(deployPendingStorageKey)
    }
  } catch {
    // The in-memory state remains authoritative when storage is unavailable.
  }
}

type LoginResponse = {
  token?: string
  expires_at?: string
  user?: AdminUser
  mfa_required?: boolean
  mfa_challenge?: string
}

const navItems: NavItem[] = [
  // Configuration
  {
    id: 'broker-config',
    group: 'Configuration',
    label: 'Configuration',
    path: '/broker-config',
    eyebrow: 'Operations',
    title: 'Broker configuration manager',
    description: 'Import, edit, validate and version mosquitto.conf with explicit adoption before any apply.',
    minRole: 'auditor',
  },
  {
    id: 'broker-settings',
    group: 'Configuration',
    label: 'Broker settings',
    path: '/broker-settings',
    eyebrow: 'Operations',
    title: 'Persistence, queues, sessions and logging',
    description: 'Edit the general broker settings with the catalog ranges and preview the change before any apply.',
    minRole: 'auditor',
  },
  {
    id: 'broker-bridges',
    group: 'Configuration',
    label: 'Bridges',
    path: '/broker-bridges',
    eyebrow: 'Operations',
    title: 'Bridges to remote brokers',
    description: 'Connect this broker to remote brokers, forward topics in both directions and preview the change before any apply.',
    minRole: 'auditor',
  },
  {
    id: 'broker-plugins',
    group: 'Configuration',
    label: 'Plugins',
    path: '/broker-plugins',
    eyebrow: 'Operations',
    title: 'Mosquitto plugins',
    description: 'Load the plugins this broker needs, keep the options MCM does not manage untouched and preview the change before any apply.',
    minRole: 'auditor',
  },
  // MQTT Access
  {
    id: 'users',
    group: 'MQTT Access',
    label: 'Users',
    path: '/users',
    eyebrow: 'Identity',
    title: 'MQTT user directory',
    description: 'Provision MQTT users, toggle account status, reset credentials, and remove stale accounts.',
  },
  {
    id: 'acls',
    group: 'MQTT Access',
    label: 'ACLs',
    path: '/acls',
    eyebrow: 'Authorization',
    title: 'ACL policy workspace',
    description: 'Topic permissions, policy reviews, and audit-safe change workflows.',
  },
  {
    id: 'listeners',
    group: 'MQTT Access',
    label: 'Listeners',
    path: '/listeners',
    eyebrow: 'MQTT access',
    title: 'MQTT listeners',
    description: 'Manage the broker listener set (TCP and WebSockets).',
  },
  {
    id: 'certificates',
    group: 'MQTT Access',
    label: 'Certificates',
    path: '/certificates',
    eyebrow: 'MQTT access',
    title: 'Broker TLS certificates',
    description: 'Upload, rotate and activate the TLS material the broker serves.',
    minRole: 'auditor',
  },
  {
    id: 'broker-dynsec',
    group: 'MQTT Access',
    label: 'Dynamic security',
    path: '/broker-dynsec',
    eyebrow: 'Authorization',
    title: 'Dynamic security database',
    description: 'Manage the clients, groups and roles the dynamic security plugin enforces, and watch the management channel of the plugin.',
    minRole: 'auditor',
  },
  // Changes
  {
    id: 'deploy',
    group: 'Changes',
    label: 'Deploy',
    path: '/deploy',
    eyebrow: 'Operations',
    title: 'Mosquitto configuration deploy',
    description: 'Preview, apply, and track configuration changes to the Mosquitto broker.',
  },
  {
    id: 'changes',
    group: 'Changes',
    label: 'Configuration revisions',
    path: '/changes',
    eyebrow: 'Review and recovery',
    title: 'Configuration changes',
    description: 'Review exact configuration revisions, activation results and recoverable copies.',
    minRole: 'auditor',
  },
  // Diagnostics
  {
    id: 'dashboard',
    group: 'Diagnostics',
    label: 'Dashboard',
    path: '/dashboard',
    eyebrow: 'Broker overview',
    title: 'Live broker snapshot',
    description: 'Connection state and the latest topic traffic from the configured Mosquitto broker.',
  },
  {
    id: 'topics',
    group: 'Diagnostics',
    label: 'Topics',
    path: '/topics',
    eyebrow: 'Observation',
    title: 'Topic explorer',
    description: 'Inspect incoming topic names and safe payload previews as messages arrive; optional Sparkplug B and JSON Schema diagnostics inspect messages only and never enforce broker traffic.',
  },
  {
    id: 'broker-diagnostics',
    group: 'Diagnostics',
    label: 'Broker diagnostics',
    path: '/broker-diagnostics',
    eyebrow: 'Diagnostics',
    title: 'Broker diagnostics workspace',
    description: 'Separate MCM observations, broker logs, internal metrics and broker-published system metrics.',
    minRole: 'auditor',
  },
  {
    id: 'logs',
    group: 'Diagnostics',
    label: 'Logs',
    path: '/logs',
    eyebrow: 'Operations',
    title: 'Realtime broker and MCM events',
    description: 'Monitor broker-log entries, connection transitions and MCM operational events as they are ingested.',
  },
  // Administration
  {
    id: 'security',
    group: 'Administration',
    label: 'Security',
    path: '/security',
    eyebrow: 'Audit',
    title: 'Recent security events',
    description: 'Review failed admin logins, disabled-user login attempts, protected API failures, and ACL API audit hooks.',
  },
  {
    id: 'audit',
    group: 'Administration',
    label: 'Audit',
    path: '/audit',
    eyebrow: 'Security',
    title: 'Administrative audit log',
    description: 'Review recent administrative changes, actors, affected resources, and outcomes.',
  },
  {
    id: 'account',
    group: 'Administration',
    label: 'Account',
    path: '/account',
    eyebrow: 'Security',
    title: 'Account security',
    description: 'Manage two-factor authentication and account security settings.',
  },
  {
    id: 'admin-users',
    group: 'Administration',
    label: 'Operators',
    path: '/admin-users',
    eyebrow: 'Administration',
    title: 'Admin user directory',
    description: 'Manage dashboard operators: provision accounts, assign roles, reset credentials, and deactivate stale users.',
    minRole: 'admin',
  },
]

function navIdFromPath(pathname: string) {
  const normalizedPath = pathname.replace(/\/+$/, '') || '/dashboard'
  return navItems.find((item) => item.path === normalizedPath)?.id ?? 'dashboard'
}

function App() {
  const { token, currentUser, handleLogin, handleLogout, refreshCurrentUser } = useAuthSession()

  if (!token) {
    return <LoginScreen onLogin={handleLogin} />
  }

  if (!currentUser) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[radial-gradient(circle_at_top,#1d4e89_0%,#0f172a_38%,#020617_100%)] text-slate-100">
        <p className="text-sm uppercase tracking-[0.3em] text-slate-400">Restoring session…</p>
      </div>
    )
  }

  return <Dashboard token={token} currentUser={currentUser} onLogout={handleLogout} onRefreshUser={refreshCurrentUser} />
}

function Dashboard({ token, currentUser, onLogout, onRefreshUser }: { token: string; currentUser: AdminUser; onLogout: () => void; onRefreshUser: () => void }) {
  const [activeId, setActiveId] = useState<string>(() => navIdFromPath(window.location.pathname))
  const [visitedBroker, setVisitedBroker] = useState(() => ['broker-config', 'changes'].includes(navIdFromPath(window.location.pathname)))
  const [securityEvents, setSecurityEvents] = useState<SecurityEvent[]>([])
  const [securityError, setSecurityError] = useState<string>('')
  const [auditEvents, setAuditEvents] = useState<AuditEvent[]>([])
  const [auditError, setAuditError] = useState<string>('')
  const [deployPending, setDeployPending] = useState(readDeployPending)
  const nextMutationID = useRef(0)
  const appliedMutationID = useRef(0)
  const activeItem = navItems.find((item) => item.id === activeId) ?? navItems[0]
  const visibleNavItems = navItems.filter((item) => {
    if (!item.minRole) return true
    const userRank = ROLE_RANK[currentUser.role as Role] ?? 0
    const minRank  = ROLE_RANK[item.minRole as Role] ?? 0
    return userRank >= minRank
  })
  const {
    brokerStatus,
    streamState,
    topics,
    latestTopic,
    logs,
    uniqueTopicCount,
    liveTrafficMetrics,
    statusFacts,
  } = useBrokerStream(token, onLogout)
  // Set only by the dashboard's "Connect an application" action so the Users
  // panel opens its onboarding journey once; any other navigation clears it.
  const [startOnboarding, setStartOnboarding] = useState(false)
  // Bumped on every navigation, including to the view already open, and used
  // as the key of views that read their state from the URL query (Logs), so
  // they remount and start again from the URL they were opened with.
  const [navigationKey, setNavigationKey] = useState(0)

  // search: an optional URL query for the target panel (for example a
  // filtered Logs link from the dashboard). The sidebar passes none, so its
  // links always open the plain panel.
  const handleSelectNav = useCallback((item: NavItem, options?: { startOnboarding?: boolean; search?: string }) => {
    if (item.id === 'broker-config' || item.id === 'changes') setVisitedBroker(true)
    // Compare path and query together: opening the same view with another
    // filter (a different query) is a new history entry, while opening exactly
    // the current URL again adds none.
    const target = `${item.path}${options?.search ?? ''}`
    const current = `${window.location.pathname}${window.location.search}`
    if (current !== target) {
      window.history.pushState(null, '', target)
    }
    setStartOnboarding(options?.startOnboarding === true)
    setActiveId(item.id)
    setNavigationKey((current) => current + 1)
  }, [])
  const openPanel = useCallback((id: string, options?: { startOnboarding?: boolean; search?: string }) => {
    const item = navItems.find((candidate) => candidate.id === id)
    if (item) handleSelectNav(item, options)
  }, [handleSelectNav])

  const beginDeployMutation = useCallback(() => {
    nextMutationID.current += 1
    return nextMutationID.current
  }, [])
  const markDeployPending = useCallback((mutationID: number) => {
    if (mutationID <= appliedMutationID.current) return
    setDeployPending(true)
    persistDeployPending(true)
  }, [])
  const clearDeployPending = useCallback((mutationWatermark: number) => {
    if (nextMutationID.current !== mutationWatermark) return
    appliedMutationID.current = mutationWatermark
    setDeployPending(false)
    persistDeployPending(false)
  }, [])

  useEffect(() => {
    const handlePopState = () => {
      const selected = navIdFromPath(window.location.pathname)
      if (selected === 'broker-config' || selected === 'changes') setVisitedBroker(true)
      setStartOnboarding(false)
      setActiveId(selected)
      setNavigationKey((current) => current + 1)
    }
    window.addEventListener('popstate', handlePopState)
    return () => window.removeEventListener('popstate', handlePopState)
  }, [])

  useEffect(() => {
    if (activeId !== 'security') return

    authenticatedFetch('/api/v1/security/events?limit=50', { token, onUnauthorized: onLogout })
      .then(async (response) => {
        if (!response.ok) {
          throw new Error('Unable to load security events.')
        }
        return response.json() as Promise<{ events?: SecurityEvent[] }>
      })
      .then((body) => {
        if (!body) return
        setSecurityEvents(body.events ?? [])
        setSecurityError('')
      })
      .catch((error: Error) => {
        if (isUnauthorizedResponseError(error)) return
        setSecurityError(error.message)
      })
  }, [activeId, token, onLogout])

  useEffect(() => {
    if (activeId !== 'audit') return

    authenticatedFetch('/api/v1/audit-events?limit=25', { token, onUnauthorized: onLogout })
      .then(async (response) => {
        if (!response.ok) {
          throw new Error('Failed to load audit events.')
        }
        return response.json() as Promise<{ events?: AuditEvent[] }>
      })
      .then((body) => {
        if (!body) return
        setAuditEvents(body.events ?? [])
        setAuditError('')
      })
      .catch((error: Error) => {
        if (isUnauthorizedResponseError(error)) return
        setAuditError(error.message)
      })
  }, [activeId, token, onLogout])

  return (
    <DashboardFrame
      navItems={visibleNavItems}
      activeItem={activeItem}
      activeId={activeId}
      brokerStatus={brokerStatus}
      streamState={streamState}
      uniqueTopicCount={uniqueTopicCount}
      logCount={logs.length}
      currentUser={currentUser}
      onSelectNav={handleSelectNav}
      onLogout={onLogout}
    >
      {deployPending ? (
        <div role="status" aria-live="polite" className="mt-8 rounded-2xl border border-amber-300/30 bg-amber-400/10 p-5">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">Pending deploy</p>
          <p className="mt-2 text-sm text-amber-50">User or ACL changes are saved but have not been applied to Mosquitto yet.</p>
        </div>
      ) : null}

      {visitedBroker && visibleNavItems.some((item) => item.id === 'broker-config') ? (
        <div hidden={activeId !== 'broker-config' && activeId !== 'changes'}>
          <BrokerConfigPanel token={token} onLogout={onLogout} role={currentUser.role} view={activeId === 'changes' ? 'changes' : 'configuration'} onReview={() => handleSelectNav(navItems.find((item) => item.id === 'changes')!)} />
        </div>
      ) : null}
      {activeId === 'broker-config' || activeId === 'changes' ? null : activeId === 'logs' ? (
        <LogsPanel key={navigationKey} logs={logs} streamState={streamState} />
      ) : activeId === 'broker-diagnostics' ? (
        <BrokerDiagnosticsPanel token={token} onLogout={onLogout} />
      ) : activeId === 'security' ? (
        <SecurityPanel events={securityEvents} error={securityError} />
      ) : activeId === 'audit' ? (
        <AuditPanel events={auditEvents} error={auditError} />
      ) : activeId === 'dashboard' ? (
        <>
          <DashboardHome
            role={currentUser.role}
            brokerStatus={brokerStatus}
            streamState={streamState}
            statusFacts={statusFacts}
            messageCount={liveTrafficMetrics.message_count}
            windowSeconds={liveTrafficMetrics.window_seconds}
            deployPending={deployPending}
            logs={logs}
            onOpenPanel={openPanel}
            onOpenLogs={(filter) => openPanel('logs', { search: serializeLogFilterQuery(filter) })}
            onConnectApplication={() => openPanel('users', { startOnboarding: true })}
          />
          <DashboardPanel metrics={liveTrafficMetrics} topics={topics} latestTopic={latestTopic} />
        </>
      ) : activeId === 'acls' ? (
        <ACLPanel token={token} onLogout={onLogout} role={currentUser.role} onMutationStart={beginDeployMutation} onMutation={markDeployPending} />
      ) : activeId === 'users' ? (
        <MQTTUsersPanel token={token} onLogout={onLogout} role={currentUser.role} startOnboarding={startOnboarding} onMutationStart={beginDeployMutation} onMutation={markDeployPending} onApplyStart={() => nextMutationID.current} onApplySuccess={clearDeployPending} />
      ) : activeId === 'listeners' ? (
        <ListenersPanel token={token} onLogout={onLogout} role={currentUser.role} onApplyStart={beginDeployMutation} onApplySuccess={clearDeployPending} />
      ) : activeId === 'certificates' ? (
        <CertificatesPanel token={token} onLogout={onLogout} role={currentUser.role} />
      ) : activeId === 'deploy' ? (
        <DeployPanel token={token} onLogout={onLogout} role={currentUser.role} onApplyStart={() => nextMutationID.current} onApplySuccess={clearDeployPending} />
      ) : activeId === 'broker-settings' ? (
        <SettingsPanel token={token} onLogout={onLogout} role={currentUser.role} />
      ) : activeId === 'broker-bridges' ? (
        <BridgesPanel token={token} onLogout={onLogout} role={currentUser.role} />
      ) : activeId === 'broker-plugins' ? (
        <PluginsPanel token={token} onLogout={onLogout} role={currentUser.role} />
      ) : activeId === 'broker-dynsec' ? (
        <DynSecPanel token={token} onLogout={onLogout} role={currentUser.role} />
      ) : activeId === 'account' ? (
        <AccountSecurityPanel token={token} currentUser={currentUser} onLogout={onLogout} onMFAChange={onRefreshUser} />
      ) : activeId === 'admin-users' ? (
        <AdminUsersPanel token={token} onLogout={onLogout} role={currentUser.role} />
      ) : (
        <TopicsPanel topics={topics} streamState={streamState} />
      )}
    </DashboardFrame>
  )
}

function DashboardPanel({ metrics, topics, latestTopic }: { metrics: BrokerTrafficMetrics; topics: TopicMessage[]; latestTopic?: TopicMessage }) {
  const hasRateSamples = metrics.rate_points.some((point) => point.count > 0)

  return (
    <section className="mt-8 space-y-4">
      <div className="grid gap-4 lg:grid-cols-3">
        <article className="rounded-[1.75rem] border border-cyan-300/20 bg-cyan-400/10 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-100/80">Message rate</p>
          <div className="mt-3 flex items-end gap-2">
            <span className="font-mono text-4xl font-semibold text-white">{metrics.message_rate_per_minute.toFixed(1)}</span>
            <span className="pb-1 text-sm text-cyan-100">msg/min</span>
          </div>
          <p className="mt-2 text-sm text-slate-300">{metrics.message_count} messages in the last {Math.round(metrics.window_seconds / 60)} minutes.</p>
          <RateChart points={metrics.rate_points} />
          {!hasRateSamples ? <p className="mt-3 text-xs uppercase tracking-[0.18em] text-cyan-100/70">Waiting for the first rate sample</p> : null}
        </article>

        <HotspotCard title="Top topics" empty="No topic activity in the recent window." items={metrics.top_topics} />

        <article className="rounded-[1.75rem] border border-white/10 bg-slate-900/70 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Top clients</p>
          {metrics.top_clients_available && metrics.top_clients.length > 0 ? (
            <TrafficBars items={metrics.top_clients} />
          ) : (
            <div className="mt-4 space-y-3 rounded-2xl border border-dashed border-amber-300/25 bg-amber-400/10 p-4 text-sm leading-6 text-amber-50">
              <p className="font-semibold">Client IDs unavailable</p>
              <p className="text-amber-50/80">{metrics.top_clients_note || defaultClientNote}</p>
              <div className="grid gap-2 sm:grid-cols-2">
                <WidgetFact label="Observed messages" value={String(metrics.message_count)} />
                <WidgetFact label="Topic hotspots" value={String(metrics.top_topics.length)} />
              </div>
            </div>
          )}
        </article>
      </div>

      <div className="grid gap-4 lg:grid-cols-[0.85fr_1.15fr]">
        <article className="rounded-[1.75rem] border border-white/10 bg-white/[0.04] p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Metric source</p>
          <div className="mt-4 grid gap-3 sm:grid-cols-3">
            <WidgetFact label="Source" value={metrics.persistence || 'Browser memory'} />
            <WidgetFact label="Window" value={`${Math.round(metrics.window_seconds / 60)} min`} />
            <WidgetFact label="Samples" value={String(metrics.rate_points.length)} />
          </div>
        </article>
        <article className="rounded-[1.75rem] border border-white/10 bg-white/[0.04] p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Latest activity</p>
          {latestTopic ? (
            <div className="mt-4 rounded-2xl border border-cyan-300/20 bg-cyan-400/10 p-4">
              <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                <p className="break-all font-mono text-sm text-cyan-50">{latestTopic.topic}</p>
                <span className="shrink-0 text-xs text-cyan-100/70">{new Date(latestTopic.observed_at).toLocaleTimeString()}</span>
              </div>
              <p className="mt-3 line-clamp-2 break-all text-sm text-slate-200">{latestTopic.payload_preview || 'Payload preview unavailable.'}</p>
              <div className="mt-3 grid gap-2 sm:grid-cols-3">
                <WidgetFact label="Format" value={latestTopic.payload_format ?? 'unknown'} />
                <WidgetFact label="Bytes" value={String(latestTopic.payload_bytes ?? 0)} />
                <WidgetFact label="Recent list" value={`${topics.length} messages`} />
              </div>
              {latestTopic.sparkplug ? <SparkplugDetails metadata={latestTopic.sparkplug} sparkplugMetrics={latestTopic.sparkplug_metrics} compact /> : null}
            </div>
          ) : (
            <div className="mt-4 rounded-2xl border border-dashed border-white/10 bg-slate-950/30 p-4 text-sm text-slate-300">
              Waiting for topic traffic from the broker stream.
            </div>
          )}
        </article>
      </div>
    </section>
  )
}

function WidgetFact({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl border border-white/10 bg-slate-950/35 px-3 py-2">
      <p className="text-[0.65rem] font-semibold uppercase tracking-[0.18em] text-slate-400">{label}</p>
      <p className="mt-1 break-words font-mono text-sm text-white">{value}</p>
    </div>
  )
}

function HotspotCard({ title, empty, items }: { title: string; empty: string; items: BrokerTrafficItem[] }) {
  return (
    <article className="rounded-[1.75rem] border border-white/10 bg-slate-900/70 p-6">
      <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">{title}</p>
      {items.length === 0 ? <p className="mt-4 text-sm text-slate-300">{empty}</p> : <TrafficBars items={items} />}
    </article>
  )
}

function TrafficBars({ items }: { items: BrokerTrafficItem[] }) {
  const maxCount = Math.max(...items.map((item) => item.count), 1)

  return (
    <div className="mt-4 space-y-4">
      {items.map((item) => (
        <div key={item.name}>
          <div className="flex items-center justify-between gap-3 text-sm">
            <span className="break-all font-mono text-cyan-100">{item.name}</span>
            <span className="text-slate-300">{item.count}</span>
          </div>
          <div className="mt-2 h-2 overflow-hidden rounded-full bg-slate-800">
            <div className="h-full rounded-full bg-cyan-300" style={{ width: `${Math.max(6, (item.count / maxCount) * 100)}%` }} />
          </div>
          <p className="mt-1 text-xs text-slate-400">{item.percentage.toFixed(0)}% of recent messages</p>
        </div>
      ))}
    </div>
  )
}

function RateChart({ points }: { points: BrokerRatePoint[] }) {
  const visiblePoints = points.slice(-8)
  const maxCount = Math.max(...visiblePoints.map((point) => point.count), 1)

  return (
    <div className="mt-5 flex h-24 items-end gap-1.5 rounded-2xl border border-white/10 bg-slate-950/40 p-3" aria-label="Recent message rate chart">
      {visiblePoints.length === 0 ? (
        <span className="self-center text-sm text-slate-300">No rate samples yet.</span>
      ) : (
        visiblePoints.map((point) => {
          const label = `${point.count} messages at ${new Date(point.timestamp).toLocaleTimeString([], { minute: '2-digit', second: '2-digit' })}`
          return (
            <div key={point.timestamp} className="flex flex-1 flex-col items-center justify-end gap-1">
              <div className="w-full rounded-t bg-cyan-300/80" style={{ height: `${Math.max(8, (point.count / maxCount) * 100)}%` }} title={label} aria-label={label} />
              <span className="text-[0.6rem] text-slate-400">{new Date(point.timestamp).toLocaleTimeString([], { minute: '2-digit', second: '2-digit' })}</span>
            </div>
          )
        })
      )}
    </div>
  )
}

const messageNoun = { one: 'message', many: 'messages' }

// Live Topics view (issue #357): compact rows on the left, the detail of the
// selected message on the right (the newest visible one until a row is
// chosen), a topic filter, a visual pause and the limit of the live window.
function TopicsPanel({ topics, streamState }: { topics: LiveTopicMessage[]; streamState: BrokerStreamState }) {
  const feed = useLiveFeed(topics, liveTopicWindow)
  const [filterText, setFilterText] = useState('')
  const [selectedId, setSelectedId] = useState<number | null>(null)

  const shown = feed.visible.filter((topic) => matchesTopicFilter(topic.topic, filterText))
  const selected = selectedId === null ? undefined : shown.find((topic) => topic.id === selectedId)
  const detail = selected ?? shown[0]
  let fallbackNotice = ''
  if (selectedId !== null && !selected && detail) {
    fallbackNotice = feed.visible.some((topic) => topic.id === selectedId)
      ? 'The selected message is hidden by the filter; showing the newest match.'
      : 'The selected message left the live window; showing the newest message.'
  }
  const invalidPattern = isTopicFilterActive(filterText) && !isTopicPatternValid(filterText)

  return (
    <section className="mt-8 rounded-[1.75rem] border border-white/10 bg-slate-900/70 p-6">
      <LiveFeedChrome feed={feed} streamState={streamState} windowSize={liveTopicWindow} noun={messageNoun} label="Topic feed" />

      <div className="mt-4 flex flex-wrap items-end gap-4">
        <label className="text-sm text-slate-300">Filter topics{' '}
          <input type="search" aria-label="Filter topics" aria-describedby="topic-filter-help" value={filterText} onChange={(event) => setFilterText(event.target.value)} className="ml-2 rounded-lg bg-slate-900 p-2 font-mono" />
        </label>
        {isTopicFilterActive(filterText) ? <button type="button" onClick={() => setFilterText('')} className="text-sm font-semibold text-cyan-200 underline">Clear filter</button> : null}
        <p id="topic-filter-help" className="w-full text-xs text-slate-400">Text matches part of a topic, ignoring case. With + (one level) or # (the remaining levels, last only) it matches as an MQTT topic filter, such as factory/+/temperature.</p>
        {invalidPattern ? <p className="w-full text-sm text-amber-200">Not a valid MQTT topic filter: + and # must fill a whole level, and # must be the last level.</p> : null}
      </div>

      <div className="mt-5 grid gap-5 lg:grid-cols-[1fr_1fr]">
        <section aria-label="Topic messages">
          {shown.length > 0 ? (
            <ol className="divide-y divide-white/5 rounded-2xl border border-white/10 bg-white/[0.03]">
              {shown.map((topic) => <TopicRow key={topic.id} topic={topic} chosen={topic.id === selected?.id} inDetail={topic.id === detail?.id} onSelect={() => setSelectedId(topic.id)} />)}
            </ol>
          ) : (
            <p className="text-sm text-slate-300">{emptyFeedText({
              noun: messageNoun,
              filterLabel: 'filter',
              receivedAny: feed.visible.length > 0,
              pausedWhileArriving: feed.mode === 'paused' && feed.arrivedWhilePaused > 0,
              streamState,
            })}</p>
          )}
        </section>

        <section aria-label="Message detail" className="rounded-[1.75rem] border border-white/10 bg-[linear-gradient(135deg,rgba(34,211,238,0.16),rgba(15,23,42,0.08)_55%,rgba(249,115,22,0.18))] p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-100/80">{selected ? 'Selected message' : 'Newest message'}</p>
          {fallbackNotice ? <p className="mt-3 text-sm text-amber-200">{fallbackNotice}</p> : null}
          {detail ? (
            <>
              <h3 className="mt-3 break-all font-mono text-2xl font-semibold text-white">{detail.topic}</h3>
              <p className="mt-1 font-mono text-xs text-slate-300">Received {detail.observed_at} ({new Date(detail.observed_at).toLocaleString()})</p>
              {detail.schema_validation ? <SchemaValidationDetails validation={detail.schema_validation} /> : null}
              {detail.sparkplug ? <SparkplugDetails metadata={detail.sparkplug} sparkplugMetrics={detail.sparkplug_metrics} /> : null}
              <PayloadMetadata event={detail} />
              <pre className="mt-4 max-h-64 overflow-auto rounded-2xl border border-white/10 bg-slate-950/70 p-4 text-sm text-slate-100">{detail.payload_preview}</pre>
            </>
          ) : (
            <p className="mt-3 text-sm leading-7 text-slate-200/90">Choose a message to see its payload here.</p>
          )}
        </section>
      </div>
    </section>
  )
}

// chosen: the row the user picked, exposed as pressed. inDetail: the row whose
// message the detail shows, which is the newest one until a row is chosen; it
// is only highlighted, since the user did not press it.
function TopicRow({ topic, chosen, inDetail, onSelect }: { topic: LiveTopicMessage; chosen: boolean; inDetail: boolean; onSelect: () => void }) {
  return (
    <li>
      <button type="button" aria-pressed={chosen} onClick={onSelect} className={`block w-full px-3 py-2 text-left hover:bg-white/[0.04] focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300 ${inDetail ? 'bg-cyan-400/10' : ''}`}>
        <span className="flex flex-wrap items-center gap-2">
          <span className="min-w-0 break-all font-mono text-sm text-cyan-100">{topic.topic}</span>
          {topic.schema_validation ? <SchemaValidationBadge validation={topic.schema_validation} /> : null}
          {topic.sparkplug ? <SparkplugBadge metadata={topic.sparkplug} /> : null}
          <time dateTime={topic.observed_at} className="ml-auto shrink-0 font-mono text-xs text-slate-400">{new Date(topic.observed_at).toLocaleTimeString()}</time>
        </span>
        <span className="mt-1 block truncate font-mono text-xs text-slate-300">{topic.payload_preview || 'Payload preview unavailable.'}</span>
        <PayloadSummary event={topic} />
      </button>
    </li>
  )
}

function SchemaValidationBadge({ validation }: { validation: SchemaValidation }) {
  return (
    <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${validation.valid ? 'bg-emerald-400/10 text-emerald-200' : 'bg-red-400/10 text-red-200'}`}>
      Schema {validation.valid ? 'valid' : 'invalid'}
    </span>
  )
}

function SchemaValidationDetails({ validation }: { validation: SchemaValidation }) {
  return (
    <div className={`mt-4 rounded-2xl border p-3 ${validation.valid ? 'border-emerald-300/20 bg-emerald-400/10' : 'border-red-300/20 bg-red-400/10'}`}>
      <div className="flex flex-wrap items-center gap-2">
        <SchemaValidationBadge validation={validation} />
        <span className="rounded-full bg-slate-950/60 px-2.5 py-1 font-mono text-xs text-slate-100">{validation.schema_name}</span>
        <span className="rounded-full bg-slate-950/60 px-2.5 py-1 font-mono text-xs text-slate-100">{validation.topic_filter}</span>
      </div>
      {!validation.valid && validation.errors?.length ? (
        <ul className="mt-3 list-disc space-y-1 pl-5 text-xs text-red-100">
          {validation.errors.map((error) => (
            <li key={error} className="break-all">{error}</li>
          ))}
        </ul>
      ) : null}
    </div>
  )
}

function PayloadMetadata({ event }: { event: TopicMessage }) {
  const inspection = event.payload_inspection
  const chips = payloadChips(event)

  return (
    <div className="mt-4 space-y-3 rounded-2xl border border-white/10 bg-slate-950/40 p-4">
      <div className="flex flex-wrap gap-2">
        {chips.map((chip) => (
          <span key={chip} className="rounded-full bg-cyan-400/10 px-3 py-1 font-mono text-xs text-cyan-100">{chip}</span>
        ))}
      </div>
      {inspection?.json_valid ? (
        <div className="grid gap-3 text-sm text-slate-200 sm:grid-cols-2">
          {inspection.detected_type === 'json_object' ? (
            <div className="sm:col-span-2">
              <p className="text-xs uppercase tracking-[0.18em] text-slate-400">Top-level keys</p>
              <div className="mt-2 flex flex-wrap gap-2">
                {(inspection.json_top_level_keys ?? []).length === 0 ? (
                  <span className="text-slate-300">No keys.</span>
                ) : (
                  inspection.json_top_level_keys?.map((key) => (
                    <span key={key} className="rounded-lg bg-white/[0.06] px-2 py-1 font-mono text-xs text-slate-100">{key}</span>
                  ))
                )}
              </div>
            </div>
          ) : null}
          {inspection.detected_type === 'json_array' ? <PayloadFact label="Elements" value={String(inspection.json_element_count ?? 0)} /> : null}
          {inspection.detected_type === 'json_scalar' && inspection.json_scalar_summary ? <PayloadFact label="Scalar" value={inspection.json_scalar_summary} /> : null}
        </div>
      ) : (
        <p className="text-sm text-slate-300">Preview remains bounded and escaped by the UI; raw payloads are not persisted.</p>
      )}
    </div>
  )
}

function SparkplugBadge({ metadata }: { metadata: SparkplugMetadata }) {
  return (
    <span className="rounded-full bg-orange-400/10 px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] text-orange-200">
      Sparkplug {metadata.message_type}
    </span>
  )
}

function SparkplugDetails({
  metadata,
  sparkplugMetrics,
  compact = false,
}: {
  metadata: SparkplugMetadata
  sparkplugMetrics?: SparkplugDecodedPayload
  compact?: boolean
}) {
  const items = [
    ['Group', metadata.group_id],
    ['Message', metadata.message_type],
    ['Edge node', metadata.edge_node_id],
    ['Device', metadata.device_id],
  ].filter(([, value]) => Boolean(value))

  return (
    <div className={`${compact ? 'mt-2' : 'mt-4'} rounded-2xl border border-orange-300/20 bg-orange-400/10 p-3`}>
      <div className="flex flex-wrap items-center gap-2">
        <SparkplugBadge metadata={metadata} />
        <span className="rounded-full bg-slate-950/60 px-2.5 py-1 font-mono text-xs text-orange-100">{metadata.namespace}</span>
      </div>
      <dl className="mt-3 grid gap-2 text-xs text-slate-200 sm:grid-cols-2">
        {items.map(([label, value]) => (
          <div key={label}>
            <dt className="uppercase tracking-[0.18em] text-orange-100/70">{label}</dt>
            <dd className="mt-1 break-all font-mono text-orange-50">{value}</dd>
          </div>
        ))}
      </dl>
      {sparkplugMetrics && sparkplugMetrics.metrics.length > 0 ? (
        <div className="mt-3">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-orange-100/70">
            Decoded metrics
            {sparkplugMetrics.truncated ? ` (showing ${sparkplugMetrics.metrics.length} of more)` : ` (${sparkplugMetrics.metrics.length})`}
          </p>
          <div className="mt-2 overflow-x-auto rounded-xl border border-orange-300/20 bg-slate-950/40">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-orange-300/20">
                  <th className="px-3 py-2 text-left font-semibold uppercase tracking-[0.15em] text-orange-100/60">Name</th>
                  <th className="px-3 py-2 text-left font-semibold uppercase tracking-[0.15em] text-orange-100/60">Type</th>
                  <th className="px-3 py-2 text-left font-semibold uppercase tracking-[0.15em] text-orange-100/60">Value</th>
                </tr>
              </thead>
              <tbody>
                {sparkplugMetrics.metrics.map((metric, index) => (
                  <tr key={`${metric.name}-${index}`} className="border-b border-orange-300/10 last:border-0">
                    <td className="break-all px-3 py-2 font-mono text-orange-50">{metric.name || <span className="text-slate-400 italic">unnamed</span>}</td>
                    <td className="px-3 py-2 text-orange-100/80">{metric.datatype}</td>
                    <td className="break-all px-3 py-2 font-mono text-slate-200">
                      {metric.is_null ? (
                        <span className="text-slate-400 italic">null</span>
                      ) : (
                        String(metric.value ?? '')
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {sparkplugMetrics.truncated ? (
            <p className="mt-1.5 text-xs text-orange-100/50">
              Showing {sparkplugMetrics.metrics.length} metric{sparkplugMetrics.metrics.length !== 1 ? 's' : ''}. More were received but omitted to stay within the configured limit.
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

function PayloadSummary({ event }: { event: TopicMessage }) {
  // A span so it can sit inside a row button.
  return (
    <span className="mt-2 flex flex-wrap gap-2">
      {payloadChips(event).map((chip) => (
        <span key={chip} className="rounded-full bg-slate-950/70 px-2.5 py-1 font-mono text-[0.7rem] text-slate-300">{chip}</span>
      ))}
    </span>
  )
}

function PayloadFact({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-xs uppercase tracking-[0.18em] text-slate-400">{label}</p>
      <p className="mt-1 break-all font-mono text-slate-100">{value}</p>
    </div>
  )
}

function payloadChips(event: TopicMessage) {
  const inspection = event.payload_inspection
  const type = inspection?.detected_type ?? event.payload_format ?? 'unknown'
  const bytes = inspection?.byte_length ?? event.payload_bytes
  return [
    type,
    bytes === undefined ? undefined : `${bytes} bytes`,
    inspection?.json_valid ? 'valid JSON' : event.payload_format === 'json' ? 'JSON' : undefined,
    event.schema_validation ? `schema ${event.schema_validation.valid ? 'valid' : 'invalid'}` : undefined,
    (inspection?.truncated ?? event.truncated) ? 'truncated' : undefined,
  ].filter((chip): chip is string => Boolean(chip))
}

function SecurityPanel({ events, error }: { events: SecurityEvent[]; error: string }) {
  return (
    <section className="mt-8 rounded-[1.75rem] border border-white/10 bg-slate-900/70 p-6">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Audit feed</p>
          <p className="mt-2 text-sm text-slate-300">Sanitized security events only; passwords, JWTs, and request bodies are never displayed.</p>
        </div>
        <span className="rounded-full bg-amber-400/10 px-3 py-1 text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">Recent</span>
      </div>

      {error ? (
        <div className="mt-5 rounded-2xl border border-dashed border-amber-300/30 bg-amber-400/10 p-5 text-sm text-amber-100">{error}</div>
      ) : events.length === 0 ? (
        <div className="mt-5 rounded-2xl border border-dashed border-white/10 bg-white/[0.03] p-5 text-sm text-slate-300">No security events recorded yet.</div>
      ) : (
        <div className="mt-5 space-y-3">
          {events.map((event) => (
            <article key={event.id} className="rounded-2xl border border-white/10 bg-white/[0.04] p-4">
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="rounded-full bg-rose-400/10 px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] text-rose-200">{event.category}</span>
                  <span className="rounded-full bg-slate-950/70 px-2.5 py-1 font-mono text-xs text-cyan-100">{event.reason}</span>
                </div>
                <time className="text-xs text-slate-400" dateTime={event.observed_at}>{new Date(event.observed_at).toLocaleString()}</time>
              </div>
              <dl className="mt-3 grid gap-2 text-sm text-slate-200 sm:grid-cols-2">
                <div><dt className="text-xs uppercase tracking-[0.18em] text-slate-400">User</dt><dd className="mt-1 font-mono">{event.username || 'n/a'}</dd></div>
                <div><dt className="text-xs uppercase tracking-[0.18em] text-slate-400">Source IP</dt><dd className="mt-1 font-mono">{event.source_ip || 'unknown'}</dd></div>
                <div><dt className="text-xs uppercase tracking-[0.18em] text-slate-400">Endpoint</dt><dd className="mt-1 break-all font-mono">{event.method} {event.path}</dd></div>
              </dl>
            </article>
          ))}
        </div>
      )}
    </section>
  )
}


function AuditPanel({ events, error }: { events: AuditEvent[]; error: string }) {
  return (
    <section className="mt-8 rounded-[1.75rem] border border-white/10 bg-slate-900/70 p-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Audit history</p>
          <p className="mt-2 text-sm text-slate-300">Recent administrative user and ACL mutations.</p>
        </div>
        <span className="rounded-full bg-cyan-400/10 px-3 py-1 text-xs font-semibold uppercase tracking-[0.18em] text-cyan-200">Read only</span>
      </div>

      <div className="mt-5 space-y-3">
        {error ? (
          <div className="rounded-2xl border border-dashed border-amber-300/20 bg-amber-400/10 p-5 text-sm text-amber-100">{error}</div>
        ) : events.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-white/10 bg-white/[0.03] p-5 text-sm text-slate-300">No audit events recorded yet.</div>
        ) : (
          events.map((event) => (
            <article key={event.id} className="rounded-2xl border border-white/10 bg-white/[0.04] p-4">
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                <div className="flex flex-wrap items-center gap-2">
                  <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${event.result === 'success' ? 'bg-emerald-400/10 text-emerald-200' : 'bg-rose-400/10 text-rose-200'}`}>{event.result}</span>
                  <span className="rounded-full bg-slate-950/70 px-2.5 py-1 font-mono text-xs text-cyan-100">{event.actor}</span>
                  <span className="rounded-full bg-white/[0.06] px-2.5 py-1 font-mono text-xs text-slate-200">{event.action}</span>
                </div>
                <time className="text-xs text-slate-400" dateTime={event.occurred_at}>{new Date(event.occurred_at).toLocaleString()}</time>
              </div>
              <p className="mt-3 break-all text-sm text-slate-100">
                {event.resource_type}{event.resource_id ? ` #${event.resource_id}` : ''}
              </p>
              {event.metadata && Object.keys(event.metadata).length > 0 ? (
                <pre className="mt-3 max-h-32 overflow-auto rounded-2xl border border-white/10 bg-slate-950/70 p-3 text-xs text-slate-200">{JSON.stringify(event.metadata, null, 2)}</pre>
              ) : null}
            </article>
          ))
        )}
      </div>
    </section>
  )
}

type DiagnosticsTraffic = BrokerTrafficMetrics
type DiagnosticsLogEntry = { observed_at: string; severity: BrokerLog['severity']; kind: string; client_id?: string; topic?: string; message: string }
type DiagnosticsPayload = {
  sources?: {
    observed_traffic?: DiagnosticsTraffic
    mcm_events?: { status_events: number; topic_messages: number; event_subscribers: number; last_message_at: string | null; note: string }
    broker_logs?: { available: boolean; reason: string; entries: DiagnosticsLogEntry[]; severity_counts: Record<string, number>; limits: { max_entries: number; max_entries_per_read: number; max_message_bytes: number; max_age_seconds: number }; note: string }
    internal_metrics?: { endpoint: string; format: string; counters: Record<string, number>; note: string }
  }
  broker_sys?: { available: boolean; reason: string; metrics: Record<string, { value: string; observed_at: string }>; note: string }
  diagnostics?: {
    tcp?: { status: string; address: string; latency_ms: number; error: string }
    tls?: { status: string; version: string; cipher: string; error: string }
    mqtt?: { status: string; client_id: string; subscriptions: string[]; error: string }
  }
}

function BrokerDiagnosticsPanel({ token, onLogout }: { token: string; onLogout: () => void }) {
  const [severity, setSeverity] = useState('')
  const [limit, setLimit] = useState('')
  const [data, setData] = useState<DiagnosticsPayload | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    const query = new URLSearchParams()
    if (severity) query.set('severity', severity)
    if (limit) query.set('limit', limit)
    let cancelled = false
    authenticatedFetch(`/api/v1/broker/diagnostics${query.size ? `?${query}` : ''}`, { token, onUnauthorized: onLogout })
      .then(async (response) => { if (!response.ok) throw new Error('Failed to load broker diagnostics.'); return response.json() as Promise<DiagnosticsPayload> })
      .then((body) => { if (!cancelled) { setData(body); setError('') } })
      .catch((cause: Error) => { if (!cancelled && !isUnauthorizedResponseError(cause)) setError(cause.message) })
    return () => { cancelled = true }
  }, [token, onLogout, severity, limit])

  const sources = data?.sources
  const traffic = sources?.observed_traffic
  const mcm = sources?.mcm_events
  const logs = sources?.broker_logs
  const internal = sources?.internal_metrics
  const sys = data?.broker_sys
  return <section className="mt-8 space-y-5">
    <article className="rounded-2xl border border-white/10 bg-slate-950/55 p-5">
      <h2 className="text-lg font-semibold text-white">Broker diagnostics</h2>
      <div className="mt-4 flex flex-wrap gap-4"><label className="text-sm text-slate-300">Minimum severity <select aria-label="Minimum severity" value={severity} onChange={(e) => setSeverity(e.target.value)} className="ml-2 rounded-lg bg-slate-900 p-2"><option value="">Server default</option>{['debug','info','warning','error'].map((v) => <option key={v}>{v}</option>)}</select></label>
      <label className="text-sm text-slate-300">Entry limit <input aria-label="Entry limit" type="number" min="1" max="200" value={limit} onChange={(e) => setLimit(e.target.value === '' ? '' : String(Math.min(200, Math.max(1, Number(e.target.value)))))} className="ml-2 w-24 rounded-lg bg-slate-900 p-2" placeholder="Default" /></label></div>
    </article>
    {error ? <p role="alert" className="rounded-2xl border border-amber-300/30 bg-amber-400/10 p-4 text-amber-100">{error}</p> : null}
    <div className="grid gap-4 lg:grid-cols-2">
      <article className="rounded-2xl border border-white/10 bg-slate-950/55 p-5">
        <h3 className="text-sm font-semibold uppercase tracking-wider text-cyan-300">Observed traffic</h3>
        {traffic ? <>
          <p className="mt-2 text-xs text-slate-400">Observed sample, not a complete broker inventory.</p>
          <div className="mt-4 grid gap-3 sm:grid-cols-3"><WidgetFact label="Messages in window" value={String(traffic.message_count)} /><WidgetFact label="Window" value={`${traffic.window_seconds} seconds`} /><WidgetFact label="Rate" value={`${traffic.message_rate_per_minute.toFixed(1)} msg/min`} /></div>
          <div className="mt-4 grid gap-4 xl:grid-cols-2"><HotspotCard title="Top topics" empty="No topic activity in the observed window." items={traffic.top_topics} />
            <div className="rounded-2xl border border-dashed border-amber-300/25 bg-amber-400/10 p-4 text-sm leading-6 text-amber-50"><p className="font-semibold">Client identity is not part of observed traffic</p><p className="mt-1 text-amber-50/80">{traffic.top_clients_note || defaultClientNote}</p></div>
          </div>
        </> : <p className="mt-3 text-sm text-slate-300">Loading…</p>}
      </article>
      <article className="rounded-2xl border border-white/10 bg-slate-950/55 p-5">
        <h3 className="text-sm font-semibold uppercase tracking-wider text-cyan-300">MCM events</h3>
        {mcm ? <><dl className="mt-3 grid gap-3 sm:grid-cols-2"><WidgetFact label="Status events" value={String(mcm.status_events)} /><WidgetFact label="Topic messages" value={String(mcm.topic_messages)} /><WidgetFact label="Event subscribers" value={String(mcm.event_subscribers)} /><WidgetFact label="Last message at" value={mcm.last_message_at ? new Date(mcm.last_message_at).toLocaleString() : 'None yet'} /></dl><p className="mt-3 text-xs text-slate-400">{mcm.note}</p></> : <p className="mt-3 text-sm text-slate-300">Loading…</p>}
      </article>
      <article className="rounded-2xl border border-white/10 bg-slate-950/55 p-5">
        <h3 className="text-sm font-semibold uppercase tracking-wider text-cyan-300">Broker logs</h3>
        {logs ? <>
          <dl className="mt-3 grid gap-3 sm:grid-cols-2"><WidgetFact label="Availability" value={logs.available ? 'Available' : 'Unavailable'} />{logs.available ? null : <WidgetFact label="Reason" value={logs.reason || 'No reason provided.'} />}{Object.entries(logs.severity_counts).map(([name, count]) => <WidgetFact key={name} label={`${name} entries`} value={String(count)} />)}</dl>
          <p className="mt-4 text-xs font-semibold uppercase tracking-wider text-slate-400">Effective limits</p>
          <dl className="mt-2 grid gap-3 sm:grid-cols-2"><WidgetFact label="Max entries" value={String(logs.limits.max_entries)} /><WidgetFact label="Max entries per read" value={String(logs.limits.max_entries_per_read)} /><WidgetFact label="Max message bytes" value={String(logs.limits.max_message_bytes)} /><WidgetFact label="Max age" value={`${logs.limits.max_age_seconds} seconds`} /></dl>
          <p className="mt-3 text-xs text-slate-400">{logs.note}</p>
          {logs.available ? <div className="mt-4 space-y-3">{logs.entries.length ? logs.entries.map((entry, index) => <article key={`${entry.observed_at}-${index}`} className="rounded-2xl border border-white/10 bg-white/[0.04] p-4">
            <div className="flex flex-wrap items-center gap-2"><time dateTime={entry.observed_at} className="text-xs text-slate-400">{new Date(entry.observed_at).toLocaleString()}</time><SeverityBadge severity={entry.severity} /><span className="font-mono text-xs text-cyan-100">{entry.kind}</span></div>
            {entry.client_id ? <p className="mt-2 text-xs text-slate-300">Client ID (broker-sourced): <span className="font-mono text-cyan-100">{entry.client_id}</span></p> : null}
            {entry.topic ? <p className="mt-1 break-all text-xs text-slate-300">Topic: <span className="font-mono text-cyan-100">{entry.topic}</span></p> : null}
            <p className="mt-3 whitespace-pre-wrap break-words text-sm text-slate-100">{entry.message}</p>
          </article>) : <p className="text-sm text-slate-300">No broker log entries in this window.</p>}</div> : null}
        </> : <p className="mt-3 text-sm text-slate-300">Loading…</p>}
      </article>
      <article className="rounded-2xl border border-white/10 bg-slate-950/55 p-5">
        <h3 className="text-sm font-semibold uppercase tracking-wider text-cyan-300">Internal metrics</h3>
        {internal ? <><dl className="mt-3 grid gap-3 sm:grid-cols-2"><WidgetFact label="Endpoint" value={internal.endpoint} /><WidgetFact label="Format" value={internal.format} /></dl><p className="mt-4 text-xs font-semibold uppercase tracking-wider text-slate-400">Counters</p><ul className="mt-2 space-y-2">{Object.entries(internal.counters).map(([name, value]) => <li key={name} className="flex justify-between gap-3 rounded-xl bg-white/[0.04] px-3 py-2 text-sm"><span className="break-all font-mono text-cyan-100">{name}</span><span className="font-mono text-white">{value}</span></li>)}</ul>{Object.keys(internal.counters).length === 0 ? <p className="mt-2 text-sm text-slate-300">No counters reported.</p> : null}<p className="mt-3 text-xs text-slate-400">{internal.note}</p></> : <p className="mt-3 text-sm text-slate-300">Loading…</p>}
      </article>
    </div>
    <article className="rounded-2xl border border-white/10 bg-slate-950/55 p-5"><h3 className="text-sm font-semibold uppercase tracking-wider text-cyan-300">Broker-published $SYS metrics</h3>
      {sys ? sys.available ? <><p className="mt-2 text-xs text-slate-400">{sys.note}</p><div className="mt-3 space-y-2">{Object.entries(sys.metrics).map(([topic, metric]) => <div key={topic} className="grid gap-2 rounded-xl bg-white/[0.04] p-3 sm:grid-cols-3"><span className="break-all font-mono text-cyan-100">{topic}</span><span className="font-mono text-white">{metric.value}</span><time dateTime={metric.observed_at} className="text-xs text-slate-400">{new Date(metric.observed_at).toLocaleString()}</time></div>)}</div>{Object.keys(sys.metrics).length === 0 ? <p className="mt-3 text-sm text-slate-300">No $SYS metrics observed yet.</p> : null}</> : <p className="mt-3 text-sm text-amber-100">Unavailable: {sys.reason || 'No reason provided.'}</p> : <p className="mt-3 text-sm text-slate-300">Loading…</p>}
    </article>
    <article className="rounded-2xl border border-white/10 bg-slate-950/55 p-5"><h3 className="text-sm font-semibold uppercase tracking-wider text-cyan-300">Diagnostic checks</h3><div className="mt-3 grid gap-3 sm:grid-cols-3">{(['tcp','tls','mqtt'] as const).map((name) => { const check = data?.diagnostics?.[name]; return <div key={name} className="rounded-xl border border-white/10 bg-white/[0.04] p-3"><div className="flex items-center justify-between gap-2"><strong className="uppercase text-cyan-100">{name}</strong><SeverityStatusBadge status={check?.status ?? 'not run'} /></div>
      {check ? <DiagnosticCheckDetail name={name} check={check} /> : <p className="mt-2 text-xs text-slate-300">Check not run.</p>}
    </div> })}</div></article>
  </section>
}

type DiagnosticCheck = NonNullable<NonNullable<DiagnosticsPayload['diagnostics']>[keyof NonNullable<DiagnosticsPayload['diagnostics']>]>
function SeverityStatusBadge({ status }: { status: string }) {
  const style = status === 'connected' || status === 'reachable' ? 'bg-emerald-400/10 text-emerald-200' : status === 'disabled' ? 'bg-slate-400/10 text-slate-200' : 'bg-amber-400/10 text-amber-200'
  return <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${style}`}>{status}</span>
}
function DiagnosticCheckDetail({ name, check }: { name: 'tcp' | 'tls' | 'mqtt'; check: DiagnosticCheck }) {
  const facts = name === 'tcp' && 'latency_ms' in check ? [['Latency', `${check.latency_ms} ms`], ['Address', check.address]] : name === 'tls' && 'version' in check ? [['TLS version', check.version || 'n/a'], ['Cipher', check.cipher || 'n/a']] : name === 'mqtt' && 'client_id' in check ? [['Client ID', check.client_id || 'n/a'], ['Subscriptions', check.subscriptions.join(', ') || 'None']] : []
  return <><dl className="mt-2 space-y-2 text-xs">{facts.map(([label, value]) => <div key={label}><dt className="text-slate-400">{label}</dt><dd className="break-all font-mono text-slate-200">{value}</dd></div>)}</dl><p className="mt-2 text-xs text-slate-300">{check.error || 'No error reported.'}</p></>
}

function ACLPanel({ token, onLogout, role = '', onMutationStart, onMutation }: { token: string; onLogout: () => void; role?: string; onMutationStart?: () => number; onMutation?: (mutationID: number) => void }) {
  const canWrite = can(role, 'acl.write')
  const [rules, setRules] = useState<ACLRule[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [showForm, setShowForm] = useState(false)
  const [editRule, setEditRule] = useState<ACLRule | null>(null)
  const [formPrincipal, setFormPrincipal] = useState('')
  const [formTopicFilter, setFormTopicFilter] = useState('')
  const [formPermission, setFormPermission] = useState<ACLPermission>('read')
  const [formSelector, setFormSelector] = useState<ACLSelector>('user')
  const [formScope, setFormScope] = useState<ACLScope>('global')
  const [formListenerID, setFormListenerID] = useState('')
  const [formDescription, setFormDescription] = useState('')
  const [formError, setFormError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
  const [refreshTick, setRefreshTick] = useState(0)

  const fetchRules = useCallback(() => { setRefreshTick((n) => n + 1) }, [])

  useEffect(() => {
    let cancelled = false
    authenticatedFetch('/api/v1/acls', { token, onUnauthorized: onLogout })
      .then(async (response) => {
        if (!response.ok) throw new Error('Failed to load ACL rules.')
        return response.json() as Promise<{ rules: ACLRule[] }>
      })
      .then((body) => {
        if (cancelled) return
        setRules(body.rules ?? [])
        setError('')
        setLoading(false)
      })
      .catch((err: Error) => {
        if (cancelled) return
        if (isUnauthorizedResponseError(err)) return
        setError(err.message)
        setLoading(false)
      })
    return () => { cancelled = true }
  }, [token, refreshTick, onLogout])

  const openCreate = () => {
    setEditRule(null)
    setFormPrincipal('')
    setFormTopicFilter('')
    setFormPermission('read')
    setFormSelector('user')
    setFormScope('global')
    setFormListenerID('')
    setFormDescription('')
    setFormError('')
    setShowForm(true)
  }

  const openEdit = (rule: ACLRule) => {
    setEditRule(rule)
    setFormPrincipal(rule.principal)
    setFormTopicFilter(rule.topic_filter)
    setFormPermission(rule.permission)
    setFormSelector(rule.selector ?? 'user')
    setFormScope(rule.scope ?? 'global')
    setFormListenerID(rule.listener_id ?? '')
    setFormDescription(rule.description ?? '')
    setFormError('')
    setShowForm(true)
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting) return
    setSubmitting(true)
    setFormError('')
    const mutationID = onMutationStart?.() ?? 0
    try {
      const body = JSON.stringify({
        principal: formPrincipal.trim(),
        topic_filter: formTopicFilter.trim(),
        permission: formPermission,
        selector: formSelector,
        scope: formScope,
        listener_id: formScope === 'listener' ? formListenerID.trim() : '',
        description: formDescription.trim(),
      })
      const url = editRule ? `/api/v1/acls/${editRule.id}` : '/api/v1/acls'
      const method = editRule ? 'PUT' : 'POST'
      const response = await authenticatedFetch(url, {
        token,
        onUnauthorized: onLogout,
        method,
        headers: { 'Content-Type': 'application/json' },
        body,
      })
      if (!response.ok) {
        const errBody = (await response.json().catch(() => null)) as { error?: string; details?: string[] } | null
        const details = errBody?.details?.join('; ')
        setFormError(details ?? errBody?.error ?? 'Request failed.')
        return
      }
      setShowForm(false)
      onMutation?.(mutationID)
      fetchRules()
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) { setFormError((err as Error).message); return }
      setFormError('Could not reach the server.')
    } finally {
      setSubmitting(false)
    }
  }

  const handleDelete = async (id: string) => {
    const mutationID = onMutationStart?.() ?? 0
    try {
      const response = await authenticatedFetch(`/api/v1/acls/${id}`, {
        token,
        onUnauthorized: onLogout,
        method: 'DELETE',
      })
      if (!response.ok && response.status !== 204) {
        const errBody = (await response.json().catch(() => null)) as { error?: string } | null
        setError(errBody?.error ?? 'Delete failed.')
        return
      }
      setDeleteConfirmId(null)
      onMutation?.(mutationID)
      fetchRules()
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) { setError((err as Error).message); return }
      setError('Could not reach the server.')
    }
  }

  return (
    <section className="mt-8 space-y-6">
      <div className="flex items-center justify-between gap-4">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">MQTT permissions</p>
          <p className="mt-1 text-sm text-slate-300">
            {rules.length} rule{rules.length !== 1 ? 's' : ''} configured. These are the permissions Mosquitto grants on
            a topic; the MCM administrative roles are managed in the admin panel.
          </p>
        </div>
        <button
          type="button"
          onClick={openCreate}
          disabled={!canWrite}
          title={!canWrite ? permissionTitle('acl.write') : undefined}
          className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
        >
          Add Rule
        </button>
      </div>

      {error ? (
        <div className="rounded-2xl border border-dashed border-amber-300/30 bg-amber-400/10 p-5 text-sm text-amber-100">{error}</div>
      ) : null}

      {showForm ? (
        <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">{editRule ? 'Edit rule' : 'New rule'}</p>
          <form onSubmit={handleSubmit} className="mt-4 space-y-4">
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Source</span>
              <select
                value={formSelector}
                onChange={(e) => setFormSelector(e.target.value as ACLSelector)}
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              >
                <option value="user">User &mdash; the rule applies to one MQTT user</option>
                <option value="anonymous">Anonymous &mdash; the rule applies to unauthenticated clients</option>
                <option value="pattern">Pattern &mdash; the rule applies to every client</option>
              </select>
              <span className="mt-1 block text-xs text-slate-400">
                The native scope of the ACL entry in Mosquitto. A pattern rule may use the <code>%u</code> (username) and
                <code> %c</code> (client id) placeholders, each one on a whole topic level.
              </span>
            </label>
            {formSelector === 'user' ? (
              <label className="block">
                <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Principal</span>
                <input
                  type="text"
                  required
                  value={formPrincipal}
                  onChange={(e) => setFormPrincipal(e.target.value)}
                  placeholder="mqtt-username"
                  className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
                />
                <span className="mt-1 block text-xs text-slate-400">Use an MQTT username from the Users panel. MCM renders this as Mosquitto&apos;s <code>user &lt;username&gt;</code> directive.</span>
              </label>
            ) : null}
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Topic filter</span>
              <input
                type="text"
                required
                value={formTopicFilter}
                onChange={(e) => setFormTopicFilter(e.target.value)}
                placeholder="sensors/# or device/+/status"
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              />
              <span className="mt-1 block text-xs text-slate-400">MQTT wildcards: + (single level), # (multi-level, must be last)</span>
            </label>
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Permission</span>
              <select
                value={formPermission}
                onChange={(e) => setFormPermission(e.target.value as ACLRule['permission'])}
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              >
                <option value="read">read</option>
                <option value="write">write</option>
                <option value="readwrite">readwrite</option>
                <option value="deny">deny</option>
              </select>
              <span className="mt-1 block text-xs text-slate-400">
                Mosquitto handles a deny before any grant, so a deny that matches a topic removes the access a broader
                rule would grant.
              </span>
            </label>
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">ACL file</span>
              <select
                value={formScope}
                onChange={(e) => setFormScope(e.target.value as ACLScope)}
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              >
                <option value="global">Broker-wide &mdash; the rule applies to every listener</option>
                <option value="listener">One listener &mdash; the rule applies to that listener only</option>
              </select>
              <span className="mt-1 block text-xs text-slate-400">
                A listener with its own ACL file uses only that file, so the other listeners keep the broker-wide rules.
              </span>
            </label>
            {formScope === 'listener' ? (
              <label className="block">
                <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Listener</span>
                <input
                  type="text"
                  required
                  value={formListenerID}
                  onChange={(e) => setFormListenerID(e.target.value)}
                  placeholder="tls"
                  className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
                />
                <span className="mt-1 block text-xs text-slate-400">
                  The id of the listener that owns the rule. The listener must declare the ACL file MCM writes for it,
                  otherwise the deploy refuses the scope.
                </span>
              </label>
            ) : null}
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Description (optional)</span>
              <input
                type="text"
                value={formDescription}
                onChange={(e) => setFormDescription(e.target.value)}
                placeholder="Short description for audit purposes"
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              />
            </label>
            {formError ? (
              <div className="rounded-2xl border border-rose-300/30 bg-rose-400/10 px-4 py-3 text-sm text-rose-100">{formError}</div>
            ) : null}
            <div className="flex gap-3">
              <button
                type="submit"
                disabled={submitting || !canWrite}
                title={!canWrite ? permissionTitle('acl.write') : undefined}
                className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
              >
                {submitting ? 'Saving…' : editRule ? 'Update rule' : 'Create rule'}
              </button>
              <button
                type="button"
                onClick={() => setShowForm(false)}
                className="rounded-xl border border-white/10 px-4 py-2 text-sm text-slate-300 transition hover:border-white/20 hover:text-white"
              >
                Cancel
              </button>
            </div>
          </form>
        </div>
      ) : null}

      <div className="rounded-2xl border border-white/10 bg-slate-900/60">
        {loading ? (
          <p className="p-6 text-sm text-slate-400">Loading rules…</p>
        ) : rules.length === 0 ? (
          <p className="p-6 text-sm text-slate-400">No ACL rules configured yet.</p>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10">
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Source</th>
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">ACL file</th>
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Topic filter</th>
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Permission</th>
                <th className="px-4 py-3 text-right text-xs uppercase tracking-[0.18em] text-slate-400">Actions</th>
              </tr>
            </thead>
            <tbody>
              {rules.map((rule) => (
                <tr key={rule.id} className="border-b border-white/5 last:border-0">
                  <td className="px-4 py-3 text-sm text-slate-200">
                    <span className="text-xs uppercase tracking-[0.18em] text-slate-400">{SELECTOR_LABELS[rule.selector ?? 'user']}</span>
                    {rule.principal ? <span className="ml-2 font-mono text-sm text-slate-200">{rule.principal}</span> : null}
                  </td>
                  <td className="px-4 py-3 text-xs text-slate-400">
                    {rule.scope === 'listener' ? rule.listener_id : 'broker-wide'}
                  </td>
                  <td className="px-4 py-3 font-mono text-sm text-cyan-100">{rule.topic_filter}</td>
                  <td className="px-4 py-3">
                    <span
                      className={`rounded-full px-2.5 py-1 font-mono text-xs ${
                        rule.permission === 'deny' ? 'bg-rose-400/10 text-rose-200' : 'bg-cyan-400/10 text-cyan-200'
                      }`}
                    >
                      {PERMISSION_LABELS[rule.permission]}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-right">
                    {deleteConfirmId === rule.id ? (
                      <span className="inline-flex items-center gap-2">
                        <span className="text-xs text-slate-300">Delete?</span>
                        <button
                          type="button"
                          onClick={() => { void handleDelete(rule.id) }}
                          className="rounded-xl bg-red-500/20 px-3 py-1.5 text-xs text-red-300 transition hover:bg-red-500/30"
                        >
                          Confirm
                        </button>
                        <button
                          type="button"
                          onClick={() => setDeleteConfirmId(null)}
                          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20"
                        >
                          Cancel
                        </button>
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-2">
                        <button
                          type="button"
                          onClick={() => openEdit(rule)}
                          disabled={!canWrite}
                          title={!canWrite ? permissionTitle('acl.write') : undefined}
                          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white disabled:opacity-50"
                        >
                          Edit
                        </button>
                        <button
                          type="button"
                          onClick={() => setDeleteConfirmId(rule.id)}
                          disabled={!canWrite}
                          title={!canWrite ? permissionTitle('acl.write') : undefined}
                          className="rounded-xl bg-red-500/20 px-3 py-1.5 text-xs text-red-300 transition hover:bg-red-500/30 disabled:opacity-50"
                        >
                          Delete
                        </button>
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <ACLSourcePanel token={token} onLogout={onLogout} canWrite={canWrite} />

      <EffectivePermissionsPanel token={token} onLogout={onLogout} />
    </section>
  )
}

function DeployPanel({ token, onLogout, role = '', onApplyStart, onApplySuccess }: { token: string; onLogout: () => void; role?: string; onApplyStart?: () => number; onApplySuccess?: (mutationWatermark: number) => void }) {
  const mfa = useRecentMFA(token, onLogout)
  const canPreview = can(role, 'deploy.preview')
  const canApply = can(role, 'deploy.apply')
  const [preview, setPreview] = useState<DeployPreview | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewError, setPreviewError] = useState('')
  const [unavailable, setUnavailable] = useState(false)
  const [applying, setApplying] = useState(false)
  const [applyResult, setApplyResult] = useState<Deployment | null>(null)
  const [applyError, setApplyError] = useState('')
  const [confirmApply, setConfirmApply] = useState(false)
  const [deployments, setDeployments] = useState<Deployment[]>([])
  const [historyLoading, setHistoryLoading] = useState(true)
  const [historyError, setHistoryError] = useState('')
  const [historyTick, setHistoryTick] = useState(0)

  const fetchHistory = useCallback(() => { setHistoryTick((n) => n + 1) }, [])

  useEffect(() => {
    let cancelled = false
    authenticatedFetch('/api/v1/deployments', { token, onUnauthorized: onLogout })
      .then(async (response) => {
        if (response.status === 404 || response.status === 422) {
          if (!cancelled) setUnavailable(true)
          return null
        }
        if (!response.ok) throw new Error('Failed to load deployment history.')
        return response.json() as Promise<{ deployments: Deployment[] }>
      })
      .then((body) => {
        if (cancelled) return
        if (body) {
          setDeployments(body.deployments ?? [])
          setHistoryError('')
        }
        setHistoryLoading(false)
      })
      .catch((err: Error) => {
        if (cancelled) return
        if (isUnauthorizedResponseError(err)) return
        setHistoryError(err.message)
        setHistoryLoading(false)
      })
    return () => { cancelled = true }
  }, [token, historyTick, onLogout])

  const handlePreview = async () => {
    setPreviewLoading(true)
    setPreviewError('')
    setPreview(null)
    try {
      const response = await authenticatedFetch('/api/v1/deployments/preview', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
      })
      if (response.status === 404 || response.status === 422) {
        setUnavailable(true)
        return
      }
      if (!response.ok) {
        const errBody = (await response.json().catch(() => null)) as { error?: string } | null
        setPreviewError(errBody?.error ?? 'Preview failed.')
        return
      }
      const data = (await response.json()) as DeployPreview
      setPreview(data)
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) { setPreviewError((err as Error).message); return }
      setPreviewError('Could not reach the server.')
    } finally {
      setPreviewLoading(false)
    }
  }

  const handleApply = async () => {
    if (!mfa.proof) { setApplyError('Confirm recent MFA before applying.'); return }
    if (!preview?.revision_id) {
      setApplyError('Preview revision is missing. Generate a new preview before applying.')
      return
    }

    const mutationWatermark = onApplyStart?.() ?? 0
    setApplying(true)
    setApplyError('')
    setApplyResult(null)
    setConfirmApply(false)
    try {
      const response = await authenticatedFetch('/api/v1/deployments/apply', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-MCM-MFA': mfa.proof },
        body: JSON.stringify({ revision_id: preview.revision_id }),
      })
      if (response.status === 404 || response.status === 422) {
        setUnavailable(true)
        return
      }
      if (!response.ok) {
        const errBody = (await response.json().catch(() => null)) as { error?: string } | null
        setApplyError(errBody?.error ?? 'Apply failed.')
        return
      }
      const result = (await response.json()) as Deployment
      setApplyResult(result)
      setPreview(null)
      if (result.status === 'active_verified' || result.status === 'applied') {
        onApplySuccess?.(mutationWatermark)
      }
      fetchHistory()
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return
      if (isForbiddenResponseError(err)) { setApplyError((err as Error).message); return }
      setApplyError('Could not reach the server.')
    } finally {
      setApplying(false)
    }
  }

  if (unavailable) {
    return (
      <section className="mt-8">
        <div className="rounded-2xl border border-dashed border-amber-300/25 bg-amber-400/10 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-amber-200">Not configured</p>
          <p className="mt-2 text-sm text-slate-300">Deploy functionality is not configured on this MCM instance.</p>
        </div>
      </section>
    )
  }

  return (
    <section className="mt-8 space-y-6">
      {canApply ? <RecentMFAConfirmation state={mfa} /> : null}
      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Configuration preview</p>
            <p className="mt-1 text-sm text-slate-300">Generate a diff of pending ACL and password file changes before applying.</p>
          </div>
          <div className="flex items-center gap-3">
            {preview?.has_changes ? (
              confirmApply ? (
                <span className="inline-flex items-center gap-2">
                  <span className="text-xs text-slate-300">Apply changes?</span>
                  <button
                    type="button"
                    onClick={() => { void handleApply() }}
                    disabled={applying || !canApply || !mfa.proof}
                    title={!canApply ? permissionTitle('deploy.apply') : undefined}
                    className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
                  >
                    {applying ? 'Applying…' : 'Confirm Apply'}
                  </button>
                  <button
                    type="button"
                    onClick={() => setConfirmApply(false)}
                    className="rounded-xl border border-white/10 px-4 py-2 text-sm text-slate-300 transition hover:border-white/20 hover:text-white"
                  >
                    Cancel
                  </button>
                </span>
              ) : (
                <button
                  type="button"
                  onClick={() => setConfirmApply(true)}
                  disabled={!canApply}
                  title={!canApply ? permissionTitle('deploy.apply') : undefined}
                  className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 disabled:opacity-50"
                >
                  Apply
                </button>
              )
            ) : null}
            <button
              type="button"
              onClick={() => { void handlePreview() }}
              disabled={previewLoading || !canPreview}
              title={!canPreview ? permissionTitle('deploy.preview') : undefined}
              className="rounded-xl border border-white/10 px-4 py-2 text-sm font-semibold text-slate-200 transition hover:border-white/20 hover:text-white disabled:opacity-50"
            >
              {previewLoading ? 'Generating…' : 'Preview Changes'}
            </button>
          </div>
        </div>

        {previewError ? (
          <div className="mt-5 rounded-2xl border border-dashed border-amber-300/30 bg-amber-400/10 p-5 text-sm text-amber-100">{previewError}</div>
        ) : null}

        {applyError ? (
          <div className="mt-5 rounded-2xl border border-dashed border-rose-300/30 bg-rose-400/10 p-5 text-sm text-rose-100">{applyError}</div>
        ) : null}

        {applyResult ? (
          <div className={`mt-5 rounded-2xl border p-5 ${isSuccessfulDeployment(applyResult.status) ? 'border-emerald-300/30 bg-emerald-400/10' : 'border-amber-300/30 bg-amber-400/10'}`}>
            <p className={`text-xs font-semibold uppercase tracking-[0.18em] ${isSuccessfulDeployment(applyResult.status) ? 'text-emerald-200' : 'text-amber-200'}`}>
              {applyResult.status}
            </p>
            {applyResult.message ? <p className="mt-2 text-sm text-slate-300">{applyResult.message}</p> : null}
          </div>
        ) : null}

        {preview ? (
          <div className="mt-5 space-y-4">
            {!preview.has_changes && !(preview.orphan_rules?.length) ? (
              <div className="rounded-2xl border border-dashed border-white/10 bg-white/[0.03] p-5 text-sm text-slate-300">No pending changes — configuration is up to date.</div>
            ) : null}
            {preview.orphan_rules?.length ? (
              <div className="rounded-2xl border border-amber-300/30 bg-amber-400/10 p-5">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <p className="text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">Orphan ACL rules</p>
                  <span className="rounded-full bg-amber-300/15 px-2.5 py-1 text-xs font-semibold text-amber-100">{preview.orphan_rules.length}</span>
                </div>
                <p className="mt-2 text-sm leading-6 text-amber-50">These rules reference missing or disabled MQTT users and will be excluded from the rendered Mosquitto ACL file. Delete or reassign them before applying.</p>
                <div className="mt-4 overflow-x-auto rounded-xl border border-amber-300/20 bg-slate-950/40">
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="border-b border-amber-300/20">
                        <th className="px-3 py-2 text-left text-xs uppercase tracking-[0.15em] text-amber-100/70">Principal</th>
                        <th className="px-3 py-2 text-left text-xs uppercase tracking-[0.15em] text-amber-100/70">Topic filter</th>
                        <th className="px-3 py-2 text-left text-xs uppercase tracking-[0.15em] text-amber-100/70">Permission</th>
                      </tr>
                    </thead>
                    <tbody>
                      {preview.orphan_rules.map((rule) => (
                        <tr key={rule.id} className="border-b border-amber-300/10 last:border-0">
                          <td className="break-all px-3 py-2 font-mono text-amber-50">{rule.principal}</td>
                          <td className="break-all px-3 py-2 font-mono text-amber-100">{rule.topic_filter}</td>
                          <td className="px-3 py-2 text-amber-100/80">{rule.permission}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            ) : null}
            {preview.at_risk_entries?.length ? (
              <div className="rounded-2xl border border-amber-300/30 bg-amber-400/10 p-5">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <p className="text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">Access at risk</p>
                  <span className="rounded-full bg-amber-300/15 px-2.5 py-1 text-xs font-semibold text-amber-100">{preview.at_risk_entries.length}</span>
                </div>
                <p className="mt-2 text-sm leading-6 text-amber-50">The current file grants these entries and the rendered one does not. The deploy would drop that access.</p>
                <ul className="mt-4 space-y-2 font-mono text-xs text-amber-50">
                  {preview.at_risk_entries.map((entry) => (
                    <li key={entry} className="break-all">{entry}</li>
                  ))}
                </ul>
              </div>
            ) : null}
            {preview.preserved_entries?.length ? (
              <div className="rounded-2xl border border-emerald-300/25 bg-emerald-400/10 p-5">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <p className="text-xs font-semibold uppercase tracking-[0.18em] text-emerald-200">Preserved entries</p>
                  <span className="rounded-full bg-emerald-300/15 px-2.5 py-1 text-xs font-semibold text-emerald-100">{preview.preserved_entries.length}</span>
                </div>
                <p className="mt-2 text-sm leading-6 text-emerald-50">These lines of the adopted file survive into the rendered one — nothing of the import is lost.</p>
                <ul className="mt-4 space-y-2 font-mono text-xs text-emerald-50">
                  {preview.preserved_entries.map((entry) => (
                    <li key={entry} className="break-all">{entry}</li>
                  ))}
                </ul>
              </div>
            ) : null}
            {preview.composition_warnings?.length ? (
              <div className="rounded-2xl border border-amber-300/30 bg-amber-400/10 p-5">
                <p className="text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">Composition warnings</p>
                <ul className="mt-3 space-y-2 text-sm text-amber-50">
                  {preview.composition_warnings.map((warning) => (
                    <li key={warning} className="break-words">{warning}</li>
                  ))}
                </ul>
              </div>
            ) : null}
            {preview.listener_acl_files?.length ? (
              <div className="space-y-4">
                {preview.listener_acl_files.map((file) => (
                  <div key={file.path} className="rounded-2xl border border-white/10 bg-slate-950/40 p-5">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <p className="text-xs font-semibold uppercase tracking-[0.18em] text-cyan-300">
                        Listener file — {file.listener_id}
                      </p>
                      <span className="break-all font-mono text-xs text-slate-400">{file.path}</span>
                    </div>
                    <p className="mt-2 text-sm leading-6 text-slate-300">
                      This listener reads only this file, so its rules apply to that listener alone.
                    </p>
                    {file.at_risk_entries?.length ? (
                      <ul className="mt-3 space-y-1 font-mono text-xs text-amber-100">
                        {file.at_risk_entries.map((entry) => (
                          <li key={entry} className="break-all">at risk: {entry}</li>
                        ))}
                      </ul>
                    ) : null}
                    {file.diff ? <div className="mt-4"><DiffBlock content={file.diff} /></div> : null}
                  </div>
                ))}
              </div>
            ) : null}
            {preview.removed_acl_files?.length ? (
              <div className="rounded-2xl border border-amber-300/30 bg-amber-400/10 p-5">
                <p className="text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">Files the deploy would delete</p>
                <p className="mt-2 text-sm leading-6 text-amber-50">These listener files lose their last scoped rule, so the deploy removes them.</p>
                <ul className="mt-4 space-y-2 font-mono text-xs text-amber-50">
                  {preview.removed_acl_files.map((path) => (
                    <li key={path} className="break-all">{path}</li>
                  ))}
                </ul>
              </div>
            ) : null}
            {preview.scope_issues?.length ? (
              <div className="rounded-2xl border border-rose-300/30 bg-rose-400/10 p-5">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <p className="text-xs font-semibold uppercase tracking-[0.18em] text-rose-200">Listener scope problems</p>
                  <span className="rounded-full bg-rose-300/15 px-2.5 py-1 text-xs font-semibold text-rose-100">{preview.scope_issues.length}</span>
                </div>
                <p className="mt-2 text-sm leading-6 text-rose-50">The listener-scoped rules would never reach the broker. The apply is refused until these are resolved.</p>
                <ul className="mt-4 space-y-2 text-sm text-rose-50">
                  {preview.scope_issues.map((issue) => (
                    <li key={issue} className="break-words">{issue}</li>
                  ))}
                </ul>
              </div>
            ) : null}
            {preview.scope_pending_activation?.length ? (
              <div className="rounded-2xl border border-cyan-300/25 bg-cyan-400/10 p-5">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <p className="text-xs font-semibold uppercase tracking-[0.18em] text-cyan-100">Pending listener activation</p>
                  <span className="rounded-full bg-cyan-300/15 px-2.5 py-1 text-xs font-semibold text-cyan-50">{preview.scope_pending_activation.length}</span>
                </div>
                <p className="mt-2 text-sm leading-6 text-cyan-50">The files of these listeners are written by this deployment. Their rules become active once the listener configuration declares them.</p>
                <ul className="mt-4 space-y-2 text-sm text-cyan-50">
                  {preview.scope_pending_activation.map((notice) => (
                    <li key={notice} className="break-words">{notice}</li>
                  ))}
                </ul>
              </div>
            ) : null}
            {preview.acl_diff ? (
              <div>
                <p className="mb-2 text-xs font-semibold uppercase tracking-[0.18em] text-cyan-300">ACL diff</p>
                <DiffBlock content={preview.acl_diff} />
              </div>
            ) : null}
            {preview.passwd_diff ? (
              <div>
                <p className="mb-2 text-xs font-semibold uppercase tracking-[0.18em] text-cyan-300">Password file diff</p>
                <DiffBlock content={preview.passwd_diff} />
              </div>
            ) : null}
          </div>
        ) : null}
      </div>

      <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-6">
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Deployment history</p>

        {historyError ? (
          <div className="mt-4 rounded-2xl border border-dashed border-amber-300/30 bg-amber-400/10 p-5 text-sm text-amber-100">{historyError}</div>
        ) : historyLoading ? (
          <p className="mt-4 text-sm text-slate-400">Loading history…</p>
        ) : deployments.length === 0 ? (
          <p className="mt-4 text-sm text-slate-400">No deployments recorded yet.</p>
        ) : (
          <table className="mt-4 w-full text-sm">
            <thead>
              <tr className="border-b border-white/10">
                <th className="px-0 py-2 text-left text-xs uppercase tracking-[0.18em] text-slate-400">ID</th>
                <th className="px-4 py-2 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Status</th>
                <th className="px-4 py-2 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Message</th>
                <th className="px-0 py-2 text-right text-xs uppercase tracking-[0.18em] text-slate-400">Time</th>
              </tr>
            </thead>
            <tbody>
              {deployments.map((dep) => (
                <tr key={dep.id} className="border-b border-white/5 last:border-0">
                  <td className="py-3 pr-4 font-mono text-xs text-slate-400">{String(dep.id).slice(0, 8)}</td>
                  <td className="px-4 py-3">
                    <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${isSuccessfulDeployment(dep.status) ? 'bg-emerald-400/10 text-emerald-200' : 'bg-amber-400/10 text-amber-200'}`}>
                      {dep.status}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-sm text-slate-300">{dep.message ?? '—'}</td>
                  <td className="py-3 pl-4 text-right text-xs text-slate-400">{new Date(dep.created_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </section>
  )
}

function isSuccessfulDeployment(status: string) {
  return status === 'active_verified' || status === 'applied'
}

function DiffBlock({ content }: { content: string }) {
  return (
    <pre className="max-h-64 overflow-auto rounded-2xl border border-white/10 bg-slate-950/70 p-4 font-mono text-xs leading-5">
      {content.split('\n').map((line, idx) => (
        <span
          key={idx}
          className={`block ${line.startsWith('+') ? 'bg-emerald-500/10 text-emerald-200' : line.startsWith('-') ? 'bg-red-500/10 text-red-200' : 'text-slate-300'}`}
        >
          {line || ' '}
        </span>
      ))}
    </pre>
  )
}

function LoginScreen({ onLogin }: { onLogin: (token: string, user: AdminUser) => void }) {
  const [username, setUsername] = useState<string>('')
  const [password, setPassword] = useState<string>('')
  const [submitting, setSubmitting] = useState<boolean>(false)
  const [error, setError] = useState<string>('')
  const [mfaChallenge, setMfaChallenge] = useState<string>('')
  const [mfaCode, setMfaCode] = useState<string>('')

  const completeMFA = async (challenge: string, code: string) => {
    const response = await fetch('/api/v1/auth/login/mfa', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ mfa_challenge: challenge, code: code.trim() }),
    })
    if (response.status === 401) {
      setError('Invalid MFA code. Try again or use a recovery code.')
      return
    }
    if (!response.ok) {
      const errorBody = (await response.json().catch(() => null)) as { error?: string } | null
      setError(errorBody?.error ?? 'MFA verification failed.')
      return
    }
    const completed = (await response.json()) as LoginResponse
    if (!completed.token || !completed.user) {
      setError('MFA verification did not return a session token.')
      return
    }
    onLogin(completed.token, completed.user)
  }

  const handlePasswordSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting) return
    setSubmitting(true)
    setError('')
    try {
      const response = await fetch('/api/v1/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: username.trim(), password }),
      })
      if (response.status === 401) {
        setError('Invalid username or password.')
        return
      }
      if (!response.ok) {
        const body = (await response.json().catch(() => null)) as { error?: string } | null
        setError(body?.error ?? 'Login failed. Please try again.')
        return
      }
      const body = (await response.json()) as LoginResponse
      if (body.mfa_required && body.mfa_challenge) {
        setMfaChallenge(body.mfa_challenge)
        return
      }
      if (body.token && body.user) {
        onLogin(body.token, body.user)
        return
      }
      setError('Login response was incomplete. Please retry.')
    } catch {
      setError('Could not reach the server. Check the MCM service and try again.')
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
          <form onSubmit={handleMFASubmit} className="mt-6 space-y-4">
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
          <form onSubmit={handlePasswordSubmit} className="mt-6 space-y-4">
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

export default App
