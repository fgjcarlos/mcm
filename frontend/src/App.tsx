import { useCallback, useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import MQTTUsersPanel from './features/mqtt-users/MQTTUsersPanel'
import AccountSecurityPanel from './features/mfa/AccountSecurityPanel'
import ChangeRoute from './features/change-route/ChangeRoute'
import { isMFARejection, useRecentMFA } from './features/mfa/useRecentMFA'
import RecentMFAProvider from './features/mfa/RecentMFAProvider'
import AdminUsersPanel from './features/admin-users/AdminUsersPanel'
import ListenersPanel from './features/broker-config/ListenersPanel'
import CertificatesPanel from './features/broker-config/CertificatesPanel'
import BrokerConfigPanel from './features/broker-config/BrokerConfigPanel'
import SettingsPanel from './features/broker-config/SettingsPanel'
import BridgesPanel from './features/broker-config/BridgesPanel'
import { deploymentLamp, deploymentStatusLabel } from './features/status/deploymentLamp'
import { DiffBlock, Fact, Lamp, Plate, Section } from './features/ui/primitives'
import { bodyText, codeBlock, fieldHint, fieldLabel, input, leverDanger, leverPrimary, leverSecondary, notice, select, tableHead, tableRow, textLink, type LampTone } from './features/ui/styles'
import PluginsPanel from './features/broker-config/PluginsPanel'
import DynSecPanel from './features/broker-config/DynSecPanel'
import { useAuthSession, type AdminUser } from './features/auth/useAuthSession'
import { authenticatedFetch, isForbiddenResponseError, isUnauthorizedResponseError } from './features/api/client'
import { can, permissionTitle, ROLE_RANK, type Role } from './features/auth/permissions'
import { PendingChangesAction } from './features/deploy/PendingChangesAction'
import { DashboardFrame, Mark, type NavItem } from './features/dashboard/DashboardFrame'
import DashboardHome from './features/dashboard/DashboardHome'
import ACLSourcePanel from './features/acl/ACLSourcePanel'
import EffectivePermissionsPanel from './features/acl/EffectivePermissionsPanel'
import { LogsPanel } from './features/live-feed/LogsPanel'
import { ListControls } from './features/lists/ListControls'
import { usePagedList } from './features/lists/usePagedList'
import { useClientPage } from './features/lists/useClientPage'
import { ClientPager } from './features/lists/ClientPager'
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
  // Counters cover the broker-wide passwd/ACL files only; listener files are listed apart.
  summary?: {
    users_added: number
    users_removed: number
    users_rotated: number
    has_passwd_changes: boolean
    topics_added: number
    topics_removed: number
    has_acl_changes: boolean
  }
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

// Simple or advanced navigation, remembered per browser.
const navModeStorageKey = 'mcm_nav_mode'

function readAdvancedMode() {
  try {
    return window.localStorage.getItem(navModeStorageKey) === 'advanced'
  } catch {
    return false
  }
}

function persistAdvancedMode(advanced: boolean) {
  try {
    window.localStorage.setItem(navModeStorageKey, advanced ? 'advanced' : 'simple')
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

// Simple mode shows the entries without `advanced`; advanced mode shows all.
const navItems: NavItem[] = [
  // The home: standalone, above every group.
  {
    id: 'dashboard',
    label: 'Dashboard',
    path: '/dashboard',
    title: 'Live broker snapshot',
    description: 'Connection state and the latest topic traffic from the configured Mosquitto broker.',
  },
  // MQTT Access: the broker clients (devices and apps) and what they may do
  {
    id: 'users',
    group: 'MQTT Access',
    label: 'MQTT users',
    path: '/users',
    title: 'MQTT user directory',
    description: 'Provision MQTT users, toggle account status, reset credentials, and remove stale accounts.',
  },
  {
    id: 'acls',
    group: 'MQTT Access',
    label: 'ACLs',
    path: '/acls',
    title: 'ACL policy workspace',
    description: 'Topic permissions, policy reviews, and audit-safe change workflows.',
  },
  {
    id: 'broker-dynsec',
    group: 'MQTT Access',
    label: 'Dynamic security',
    path: '/broker-dynsec',
    title: 'Dynamic security database',
    description: 'Manage the clients, groups and roles the dynamic security plugin enforces, and watch the management channel of the plugin.',
    minRole: 'auditor',
    advanced: true,
  },
  // Broker: how the broker listens and runs
  {
    id: 'listeners',
    group: 'Broker',
    label: 'Listeners',
    path: '/listeners',
    title: 'MQTT listeners',
    description: 'Manage the broker listener set (TCP and WebSockets).',
    advanced: true,
  },
  {
    id: 'certificates',
    group: 'Broker',
    label: 'Certificates',
    path: '/certificates',
    title: 'Broker TLS certificates',
    description: 'Upload, rotate and activate the TLS material the broker serves.',
    minRole: 'auditor',
    advanced: true,
  },
  {
    id: 'broker-settings',
    group: 'Broker',
    label: 'Broker settings',
    path: '/broker-settings',
    title: 'Persistence, queues, sessions and logging',
    description: 'Edit the general broker settings with the catalog ranges and preview the change before any apply.',
    minRole: 'auditor',
    advanced: true,
  },
  {
    id: 'broker-bridges',
    group: 'Broker',
    label: 'Bridges',
    path: '/broker-bridges',
    title: 'Bridges to remote brokers',
    description: 'Connect this broker to remote brokers, forward topics in both directions and preview the change before any apply.',
    minRole: 'auditor',
    advanced: true,
  },
  {
    id: 'broker-plugins',
    group: 'Broker',
    label: 'Plugins',
    path: '/broker-plugins',
    title: 'Mosquitto plugins',
    description: 'Load the plugins this broker needs, keep the options MCM does not manage untouched and preview the change before any apply.',
    minRole: 'auditor',
    advanced: true,
  },
  {
    id: 'broker-config',
    group: 'Broker',
    label: 'Configuration',
    path: '/broker-config',
    title: 'Broker configuration manager',
    description: 'Import, edit, validate and version mosquitto.conf with explicit adoption before any apply.',
    minRole: 'auditor',
    advanced: true,
  },
  // Changes
  {
    id: 'deploy',
    group: 'Changes',
    label: 'Deploy',
    path: '/deploy',
    title: 'Mosquitto configuration deploy',
    description: 'Preview, apply, and track configuration changes to the Mosquitto broker.',
  },
  {
    id: 'changes',
    group: 'Changes',
    label: 'Configuration revisions',
    path: '/changes',
    title: 'Configuration changes',
    description: 'Review exact configuration revisions, activation results and recoverable copies.',
    minRole: 'auditor',
    advanced: true,
  },
  // Diagnostics
  {
    id: 'topics',
    group: 'Diagnostics',
    label: 'Topics',
    path: '/topics',
    title: 'Topic explorer',
    description: 'Inspect incoming topic names and safe payload previews as messages arrive; optional Sparkplug B and JSON Schema diagnostics inspect messages only and never enforce broker traffic.',
    advanced: true,
  },
  {
    id: 'logs',
    group: 'Diagnostics',
    label: 'Logs',
    path: '/logs',
    title: 'Realtime broker and MCM events',
    description: 'Monitor broker-log entries, connection transitions and MCM operational events as they are ingested.',
  },
  {
    id: 'broker-diagnostics',
    group: 'Diagnostics',
    label: 'Broker diagnostics',
    path: '/broker-diagnostics',
    title: 'Broker diagnostics workspace',
    description: 'Separate MCM observations, broker logs, internal metrics and broker-published system metrics.',
    minRole: 'auditor',
    advanced: true,
  },
  // Panel administration
  {
    id: 'account',
    group: 'Panel administration',
    label: 'Account',
    path: '/account',
    title: 'Account security',
    description: 'Manage two-factor authentication and account security settings.',
  },
  {
    id: 'admin-users',
    group: 'Panel administration',
    label: 'Panel operators',
    path: '/admin-users',
    title: 'Panel operator directory',
    description: 'Manage dashboard operators: provision accounts, assign roles, reset credentials, and deactivate stale users.',
    minRole: 'admin',
    advanced: true,
  },
  {
    id: 'security',
    group: 'Panel administration',
    label: 'Security',
    path: '/security',
    title: 'Recent security events',
    description: 'Review failed admin logins, disabled-user login attempts, protected API failures, and ACL API audit hooks.',
    advanced: true,
  },
  {
    id: 'audit',
    group: 'Panel administration',
    label: 'Audit',
    path: '/audit',
    title: 'Administrative audit log',
    description: 'Review recent administrative changes, actors, affected resources, and outcomes.',
    advanced: true,
  },
]

// The namespaced listing keys (users.q, acl.user...) of a URL query, as a
// query string; Logs and other unnamespaced keys are left out.
function listingQuery(search: string) {
  const params = new URLSearchParams(search)
  for (const key of [...params.keys()]) if (!key.includes('.')) params.delete(key)
  return params.size ? `?${params}` : ''
}

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
      <div className="flex min-h-screen items-center justify-center bg-frame text-enamel">
        <p className="text-sm text-enamel-muted">Restoring session…</p>
      </div>
    )
  }

  return <RecentMFAProvider mfaEnabled={currentUser.mfa_enabled}><Dashboard token={token} currentUser={currentUser} onLogout={handleLogout} onRefreshUser={refreshCurrentUser} /></RecentMFAProvider>
}

function Dashboard({ token, currentUser, onLogout, onRefreshUser }: { token: string; currentUser: AdminUser; onLogout: () => void; onRefreshUser: () => void }) {
  const [activeId, setActiveId] = useState<string>(() => navIdFromPath(window.location.pathname))
  const [visitedBroker, setVisitedBroker] = useState(() => ['broker-config', 'changes'].includes(navIdFromPath(window.location.pathname)))
  const [securityEvents, setSecurityEvents] = useState<SecurityEvent[]>([])
  const [securityError, setSecurityError] = useState<string>('')
  const [auditEvents, setAuditEvents] = useState<AuditEvent[]>([])
  const [auditError, setAuditError] = useState<string>('')
  const [deployPending, setDeployPending] = useState(readDeployPending)
  // Opening an advanced view, by URL or by a link, switches to advanced mode.
  const [advanced, setAdvanced] = useState(() => readAdvancedMode() || Boolean(navItems.find((item) => item.id === navIdFromPath(window.location.pathname))?.advanced))
  const nextMutationID = useRef(0)
  const appliedMutationID = useRef(0)
  const activeItem = navItems.find((item) => item.id === activeId) ?? navItems[0]
  const visibleNavItems = navItems.filter((item) => {
    if (item.advanced && !advanced) return false
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
  // The paged listings keep their search, filters and page in namespaced URL
  // keys (users.q, acl.user, deploy.page...; issue #356). Remember them per
  // panel when leaving it, so opening the panel again restores its context.
  const listingQueries = useRef(new Map<string, string>())

  // search: an optional URL query for the target panel (for example a
  // filtered Logs link from the dashboard). The sidebar passes none, so its
  // links always open the plain panel.
  const handleSelectNav = useCallback((item: NavItem, options?: { startOnboarding?: boolean; search?: string }) => {
    if (item.id === 'broker-config' || item.id === 'changes') setVisitedBroker(true)
    if (item.advanced) setAdvanced(true)
    // Compare path and query together: opening the same view with another
    // filter (a different query) is a new history entry, while opening exactly
    // the current URL again adds none.
    listingQueries.current.set(window.location.pathname, listingQuery(window.location.search))
    const target = `${item.path}${options?.search ?? listingQueries.current.get(item.path) ?? ''}`
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

  useEffect(() => persistAdvancedMode(advanced), [advanced])
  const toggleAdvanced = useCallback(() => {
    // Leaving advanced mode on an advanced view goes home, as that view leaves the navigation.
    if (advanced && activeItem.advanced) openPanel('dashboard')
    setAdvanced(!advanced)
  }, [advanced, activeItem, openPanel])

  useEffect(() => {
    const handlePopState = () => {
      const selected = navIdFromPath(window.location.pathname)
      if (selected === 'broker-config' || selected === 'changes') setVisitedBroker(true)
      if (navItems.find((item) => item.id === selected)?.advanced) setAdvanced(true)
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
      statusLoad={statusFacts.load}
      streamState={streamState}
      uniqueTopicCount={uniqueTopicCount}
      logCount={logs.length}
      currentUser={currentUser}
      advanced={advanced}
      onToggleAdvanced={toggleAdvanced}
      onSelectNav={handleSelectNav}
      onLogout={onLogout}
    >
      {/* The dashboard home shows the pending state in its own card (#359): one caution, not two. */}
      {deployPending && activeId !== 'dashboard' ? (
        <div role="status" aria-live="polite" className={`mt-8 ${notice.caution}`}>
          <p className="flex items-center gap-2 text-sm font-semibold text-lamp"><span aria-hidden="true" className="size-2.5 rounded-full bg-lamp" />Pending deploy</p>
          <p className="mt-2 text-sm text-enamel">User or ACL changes are saved but have not been applied to Mosquitto yet.</p>
          {/* The deploy panel carries its own preview and apply controls, so the
              banner offers the action only on the panels that lack it. */}
          {activeId === 'deploy' ? null : <PendingChangesAction role={currentUser.role} onReview={() => openPanel('deploy')} />}
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
        <MQTTUsersPanel token={token} onLogout={onLogout} role={currentUser.role} startOnboarding={startOnboarding} onMutationStart={beginDeployMutation} onMutation={markDeployPending} onApplyStart={() => nextMutationID.current} onApplySuccess={clearDeployPending} onReviewChanges={() => openPanel('deploy')} />
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

// Recent traffic on the dashboard home: hairline sections, columns divided by track hairlines.
const trafficColumn = 'lg:border-t-0 lg:px-8 lg:pt-0 lg:first:pl-0 lg:last:pr-0'

function DashboardPanel({ metrics, topics, latestTopic }: { metrics: BrokerTrafficMetrics; topics: TopicMessage[]; latestTopic?: TopicMessage }) {
  const hasRateSamples = metrics.rate_points.some((point) => point.count > 0)

  return (
    // The detail below the home summary (#359): one heading, its parts one level down.
    <Section title="Recent traffic" ruled={false} className="mt-10 border-t border-track-strong pt-8">
      <div className="mt-6 grid gap-y-6 lg:grid-cols-3 lg:divide-x lg:divide-track">
        <Section level={4} title="Message rate" className={trafficColumn}>
          <p className="mt-3 flex items-baseline gap-2">
            <span className="font-mono text-3xl font-semibold tabular-nums text-enamel-bright">{metrics.message_rate_per_minute.toFixed(1)}</span>
            <span className="font-mono text-sm text-enamel-soft">msg/min</span>
          </p>
          <p className={`mt-1 ${bodyText}`}>{metrics.message_count} messages in the last {Math.round(metrics.window_seconds / 60)} minutes.</p>
          <RateChart points={metrics.rate_points} />
          {!hasRateSamples ? <p className={`mt-3 ${fieldHint}`}>Waiting for the first rate sample</p> : null}
        </Section>

        <HotspotCard title="Top topics" empty="No topic activity in the recent window." items={metrics.top_topics} level={4} className={trafficColumn} />

        <Section level={4} title="Top clients" className={trafficColumn}>
          {metrics.top_clients_available && metrics.top_clients.length > 0 ? (
            <TrafficBars items={metrics.top_clients} />
          ) : (
            <div className={`mt-3 space-y-3 ${notice.neutral}`}>
              <p className="font-semibold text-enamel">Client IDs unavailable</p>
              <p>{metrics.top_clients_note || defaultClientNote}</p>
              <dl className="grid gap-3 sm:grid-cols-2">
                <WidgetFact label="Observed messages" value={String(metrics.message_count)} />
                <WidgetFact label="Topic hotspots" value={String(metrics.top_topics.length)} />
              </dl>
            </div>
          )}
        </Section>
      </div>

      <div className="mt-8 grid gap-y-6 border-t border-track pt-6 lg:grid-cols-[0.85fr_1.15fr] lg:divide-x lg:divide-track">
        <Section level={4} title="Metric source" className={trafficColumn}>
          <dl className="mt-3 grid gap-3 sm:grid-cols-3">
            <WidgetFact label="Source" value={metrics.persistence || 'Browser memory'} />
            <WidgetFact label="Window" value={`${Math.round(metrics.window_seconds / 60)} min`} />
            <WidgetFact label="Samples" value={String(metrics.rate_points.length)} />
          </dl>
        </Section>
        <Section level={4} title="Latest activity" className={trafficColumn}>
          {latestTopic ? (
            <div className="mt-3">
              <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                <p className="break-all font-mono text-sm text-enamel-bright">{latestTopic.topic}</p>
                <span className="shrink-0 font-mono text-xs tabular-nums text-enamel-muted">{new Date(latestTopic.observed_at).toLocaleTimeString()}</span>
              </div>
              <p className="mt-2 line-clamp-2 break-all font-mono text-xs text-enamel">{latestTopic.payload_preview || 'Payload preview unavailable.'}</p>
              <dl className="mt-3 grid gap-3 sm:grid-cols-3">
                <WidgetFact label="Format" value={latestTopic.payload_format ?? 'unknown'} />
                <WidgetFact label="Bytes" value={String(latestTopic.payload_bytes ?? 0)} />
                <WidgetFact label="Recent list" value={`${topics.length} messages`} />
              </dl>
              {latestTopic.sparkplug ? <SparkplugDetails metadata={latestTopic.sparkplug} sparkplugMetrics={latestTopic.sparkplug_metrics} compact /> : null}
            </div>
          ) : (
            <p className={`mt-3 ${bodyText}`}>Waiting for topic traffic from the broker stream.</p>
          )}
        </Section>
      </div>
    </Section>
  )
}

// A fact of a widget; place it inside a <dl>.
function WidgetFact({ label, value }: { label: string; value: string }) {
  return <Fact label={label} value={value} />
}

function HotspotCard({ title, empty, items, level = 3, ruled, className }: { title: string; empty: string; items: BrokerTrafficItem[]; level?: 3 | 4; ruled?: boolean; className?: string }) {
  return (
    <Section title={title} level={level} ruled={ruled} className={className}>
      {items.length === 0 ? <p className={`mt-3 ${bodyText}`}>{empty}</p> : <TrafficBars items={items} />}
    </Section>
  )
}

// Share bars: an enamel fill on a raised track, square ends (circles belong to lamps).
function TrafficBars({ items }: { items: BrokerTrafficItem[] }) {
  const maxCount = Math.max(...items.map((item) => item.count), 1)

  return (
    <div className="mt-3 space-y-4">
      {items.map((item) => (
        <div key={item.name}>
          <div className="flex items-center justify-between gap-3 text-sm">
            <span className="break-all font-mono text-enamel-bright">{item.name}</span>
            <span className="font-mono tabular-nums text-enamel">{item.count}</span>
          </div>
          <div className="mt-2 h-1.5 bg-frame-high">
            <div className="h-full bg-enamel" style={{ width: `${Math.max(6, (item.count / maxCount) * 100)}%` }} />
          </div>
          <p className={`mt-1 ${fieldHint}`}>{item.percentage.toFixed(0)}% of recent messages</p>
        </div>
      ))}
    </div>
  )
}

function RateChart({ points }: { points: BrokerRatePoint[] }) {
  const visiblePoints = points.slice(-8)
  const maxCount = Math.max(...visiblePoints.map((point) => point.count), 1)

  return (
    <div className="mt-4 flex h-24 items-end gap-1.5 border-b border-track-strong pb-1" aria-label="Recent message rate chart">
      {visiblePoints.length === 0 ? (
        <span className={`self-center ${bodyText}`}>No rate samples yet.</span>
      ) : (
        visiblePoints.map((point) => {
          const label = `${point.count} messages at ${new Date(point.timestamp).toLocaleTimeString([], { minute: '2-digit', second: '2-digit' })}`
          return (
            <div key={point.timestamp} className="flex h-full min-w-0 flex-1 flex-col items-center gap-1">
              {/* A flex-1 well gives the bar's percentage height a definite base. */}
              <div className="flex w-full flex-1 items-end">
                <div className="w-full bg-enamel-soft" style={{ height: `${Math.max(8, (point.count / maxCount) * 100)}%` }} title={label} aria-label={label} />
              </div>
              <span className="font-mono text-[0.625rem] tabular-nums text-enamel-muted">{new Date(point.timestamp).toLocaleTimeString([], { minute: '2-digit', second: '2-digit' })}</span>
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
  const pager = useClientPage(shown)
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
    <section className="mt-8">
      <LiveFeedChrome feed={feed} streamState={streamState} windowSize={liveTopicWindow} noun={messageNoun} label="Topic feed" />

      <div className="mt-6 flex flex-wrap items-end gap-4 border-t border-track pt-4">
        <label className="flex flex-col gap-2"><span className={fieldLabel}>Filter topics</span>
          <input type="search" aria-label="Filter topics" aria-describedby="topic-filter-help" value={filterText} onChange={(event) => setFilterText(event.target.value)} className={`${input} w-72 max-w-full font-mono`} />
        </label>
        {isTopicFilterActive(filterText) ? <button type="button" onClick={() => setFilterText('')} className={`h-9 text-sm ${textLink}`}>Clear filter</button> : null}
        <p id="topic-filter-help" className={`w-full max-w-[68ch] ${fieldHint}`}>Text matches part of a topic, ignoring case. With + (one level) or # (the remaining levels, last only) it matches as an MQTT topic filter, such as factory/+/temperature.</p>
        {invalidPattern ? <p className="w-full text-sm text-lamp">Not a valid MQTT topic filter: + and # must fill a whole level, and # must be the last level.</p> : null}
      </div>

      <div className="mt-6 grid gap-x-8 gap-y-6 lg:grid-cols-2">
        <section aria-label="Topic messages" className="min-w-0">
          {shown.length > 0 ? (
            <div className="space-y-4">
              <ClientPager pager={pager} noun={messageNoun} />
              <ol className="divide-y divide-track border-y border-track">
                {pager.rows.map(({ item: topic }) => <TopicRow key={topic.id} topic={topic} chosen={topic.id === selected?.id} inDetail={topic.id === detail?.id} onSelect={() => setSelectedId(topic.id)} />)}
              </ol>
            </div>
          ) : (
            <p className={bodyText}>{emptyFeedText({
              noun: messageNoun,
              filterLabel: 'filter',
              receivedAny: feed.visible.length > 0,
              pausedWhileArriving: feed.mode === 'paused' && feed.arrivedWhilePaused > 0,
              streamState,
            })}</p>
          )}
        </section>

        <section aria-label="Message detail" className="min-w-0 border-t border-track pt-4 lg:border-l lg:border-t-0 lg:pl-8 lg:pt-0">
          <h3 className="text-sm font-semibold text-enamel-soft">{selected ? 'Selected message' : 'Newest message'}</h3>
          {fallbackNotice ? <p className="mt-3 text-sm text-lamp">{fallbackNotice}</p> : null}
          {detail ? (
            <>
              <p className="mt-2 break-all font-mono text-xl font-semibold text-enamel-bright">{detail.topic}</p>
              <p className="mt-1 font-mono text-xs text-enamel-muted">Received {detail.observed_at} ({new Date(detail.observed_at).toLocaleString()})</p>
              {detail.schema_validation ? <SchemaValidationDetails validation={detail.schema_validation} /> : null}
              {detail.sparkplug ? <SparkplugDetails key={detail.id} metadata={detail.sparkplug} sparkplugMetrics={detail.sparkplug_metrics} /> : null}
              <PayloadMetadata event={detail} />
              <pre className={`mt-4 max-h-64 ${codeBlock}`}>{detail.payload_preview}</pre>
            </>
          ) : (
            <p className={`mt-3 ${bodyText}`}>Choose a message to see its payload here.</p>
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
      <button type="button" aria-pressed={chosen} onClick={onSelect} className={`block w-full px-2 py-2 text-left transition-colors duration-150 hover:bg-frame-raised ${inDetail ? 'bg-frame-high' : ''}`}>
        <span className="flex flex-wrap items-center gap-2">
          {/* The row in the detail is lit like the current nav item; the lamp sits inside the topic so a long topic never wraps away from it. */}
          <span className="min-w-0 break-all font-mono text-sm text-enamel-bright"><span aria-hidden="true" className={`mr-2 inline-block size-1.5 rounded-full align-middle ${inDetail ? 'bg-enamel-bright' : ''}`} />{topic.topic}</span>
          {topic.schema_validation ? <SchemaValidationBadge validation={topic.schema_validation} /> : null}
          {topic.sparkplug ? <SparkplugBadge metadata={topic.sparkplug} /> : null}
          <time dateTime={topic.observed_at} className="ml-auto shrink-0 font-mono text-xs tabular-nums text-enamel-muted">{new Date(topic.observed_at).toLocaleTimeString()}</time>
        </span>
        <span className="mt-1 block truncate font-mono text-xs text-enamel-soft">{topic.payload_preview || 'Payload preview unavailable.'}</span>
        <PayloadSummary event={topic} />
      </button>
    </li>
  )
}

// A schema result is a check, not an activation: valid stays enamel, invalid is a caution.
function SchemaValidationBadge({ validation }: { validation: SchemaValidation }) {
  return <Lamp tone={validation.valid ? 'neutral' : 'caution'}>Schema {validation.valid ? 'valid' : 'invalid'}</Lamp>
}

function SchemaValidationDetails({ validation }: { validation: SchemaValidation }) {
  return (
    <div className={`mt-4 ${validation.valid ? 'border-t border-track pt-3' : notice.caution}`}>
      <div className="flex flex-wrap items-center gap-2">
        <SchemaValidationBadge validation={validation} />
        <Plate>{validation.schema_name}</Plate>
        <Plate>{validation.topic_filter}</Plate>
      </div>
      {!validation.valid && validation.errors?.length ? (
        <ul className="mt-3 list-disc space-y-1 pl-5 font-mono text-xs text-enamel">
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
    <div className="mt-4 space-y-3 border-t border-track pt-3">
      <div className="flex flex-wrap gap-2">
        {chips.map((chip) => (
          <Plate key={chip}>{chip}</Plate>
        ))}
      </div>
      {inspection?.json_valid ? (
        <div className="grid gap-3 text-sm text-enamel sm:grid-cols-2">
          {inspection.detected_type === 'json_object' ? (
            <div className="sm:col-span-2">
              <p className={fieldHint}>Top-level keys</p>
              <div className="mt-2 flex flex-wrap gap-2">
                {(inspection.json_top_level_keys ?? []).length === 0 ? (
                  <span className="text-enamel-soft">No keys.</span>
                ) : (
                  inspection.json_top_level_keys?.map((key) => (
                    <Plate key={key}>{key}</Plate>
                  ))
                )}
              </div>
            </div>
          ) : null}
          {inspection.detected_type === 'json_array' ? <PayloadFact label="Elements" value={String(inspection.json_element_count ?? 0)} /> : null}
          {inspection.detected_type === 'json_scalar' && inspection.json_scalar_summary ? <PayloadFact label="Scalar" value={inspection.json_scalar_summary} /> : null}
        </div>
      ) : (
        <p className={bodyText}>Preview remains bounded and escaped by the UI; raw payloads are not persisted.</p>
      )}
    </div>
  )
}

function SparkplugBadge({ metadata }: { metadata: SparkplugMetadata }) {
  return <Plate>Sparkplug {metadata.message_type}</Plate>
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
  // The broker caps decoded metrics per payload (default 50, configurable), so they can outgrow a page.
  const pager = useClientPage(sparkplugMetrics?.metrics ?? [], { pageSize: 25 })

  return (
    <div className={`${compact ? 'mt-3' : 'mt-4'} border-t border-track pt-3`}>
      <div className="flex flex-wrap items-center gap-2">
        <SparkplugBadge metadata={metadata} />
        <Plate>{metadata.namespace}</Plate>
      </div>
      <dl className="mt-3 grid gap-3 sm:grid-cols-2">
        {items.map(([label, value]) => (
          <Fact key={label} label={label} value={<span className="break-all">{value}</span>} />
        ))}
      </dl>
      {sparkplugMetrics && sparkplugMetrics.metrics.length > 0 ? (
        <div className="mt-3">
          <p className="text-xs font-semibold text-enamel-soft">
            Decoded metrics
            {sparkplugMetrics.truncated ? ` (showing ${sparkplugMetrics.metrics.length} of more)` : ` (${sparkplugMetrics.metrics.length})`}
          </p>
          <div className="mt-2"><ClientPager pager={pager} noun={sparkplugMetricNoun} /></div>
          <div className="mt-2 table-scroll">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-track-strong">
                  <th className={`${tableHead} text-left`}>Name</th>
                  <th className={`${tableHead} text-left`}>Type</th>
                  <th className={`${tableHead} text-left`}>Value</th>
                </tr>
              </thead>
              <tbody>
                {pager.rows.map(({ item: metric, index }) => (
                  <tr key={`${metric.name}-${index}`} className={tableRow}>
                    <td className="break-all py-2 pr-4 font-mono text-enamel-bright">{metric.name || <span className="italic text-enamel-muted">unnamed</span>}</td>
                    <td className="py-2 pr-4 font-mono text-enamel-soft">{metric.datatype}</td>
                    <td className="break-all py-2 pr-4 font-mono text-enamel">
                      {metric.is_null ? (
                        <span className="italic text-enamel-muted">null</span>
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
            <p className={`mt-1.5 ${fieldHint}`}>
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
    <span className="mt-1 flex flex-wrap gap-x-3 gap-y-1">
      {payloadChips(event).map((chip) => (
        <span key={chip} className="font-mono text-[0.6875rem] text-enamel-muted">{chip}</span>
      ))}
    </span>
  )
}

function PayloadFact({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className={fieldHint}>{label}</p>
      <p className="mt-1 break-all font-mono text-enamel-bright">{value}</p>
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

const sparkplugMetricNoun = { one: 'Sparkplug metric', many: 'Sparkplug metrics' }
const securityEventNoun = { one: 'security event', many: 'security events' }
const auditEventNoun = { one: 'audit event', many: 'audit events' }

function SecurityPanel({ events, error }: { events: SecurityEvent[]; error: string }) {
  const pager = useClientPage(events)
  return (
    <section className="mt-8">
      <Section
        title="Audit feed"
        description="Sanitized security events only; passwords, JWTs, and request bodies are never displayed."
        aside={<Plate>Recent</Plate>}
      >
        {error ? (
          <div className={`mt-5 ${notice.failed}`}>{error}</div>
        ) : events.length === 0 ? (
          <p className={`mt-5 ${notice.neutral}`}>No security events recorded yet.</p>
        ) : (
          <div className="mt-5 space-y-4">
          <ClientPager pager={pager} noun={securityEventNoun} />
          <ol className="divide-y divide-track border-y border-track">
            {pager.rows.map(({ item: event }) => (
              <li key={event.id} className="py-4">
                <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-sm font-semibold text-enamel-bright">{event.category}</span>
                    <Plate>{event.reason}</Plate>
                  </div>
                  <time className="font-mono text-xs tabular-nums text-enamel-muted" dateTime={event.observed_at}>{new Date(event.observed_at).toLocaleString()}</time>
                </div>
                <dl className="mt-3 grid gap-3 sm:grid-cols-3">
                  <Fact label="User" value={event.username || 'n/a'} />
                  <Fact label="Source IP" value={event.source_ip || 'unknown'} />
                  <Fact label="Endpoint" value={<span className="break-all">{event.method} {event.path}</span>} />
                </dl>
              </li>
            ))}
          </ol>
          </div>
        )}
      </Section>
    </section>
  )
}

// An audit result: a failed request lights red; a success is a recorded fact, not a verification, so it stays enamel.
const auditTone = (result: string): LampTone => (result === 'success' ? 'neutral' : 'failed')

function AuditPanel({ events, error }: { events: AuditEvent[]; error: string }) {
  const pager = useClientPage(events)
  return (
    <section className="mt-8">
      <Section
        title="Audit history"
        description="Recent administrative user and ACL mutations."
        aside={<Plate>Read only</Plate>}
      >
        {error ? (
          <div className={`mt-5 ${notice.failed}`}>{error}</div>
        ) : events.length === 0 ? (
          <p className={`mt-5 ${notice.neutral}`}>No audit events recorded yet.</p>
        ) : (
          <div className="mt-5 space-y-4">
          <ClientPager pager={pager} noun={auditEventNoun} />
          <ol className="divide-y divide-track border-y border-track">
            {pager.rows.map(({ item: event }) => (
              <li key={event.id} className="py-4">
                <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                  <div className="flex flex-wrap items-center gap-3">
                    <Lamp tone={auditTone(event.result)}>{event.result}</Lamp>
                    <span className="font-mono text-sm text-enamel-bright">{event.actor}</span>
                    <Plate>{event.action}</Plate>
                  </div>
                  <time className="font-mono text-xs tabular-nums text-enamel-muted" dateTime={event.occurred_at}>{new Date(event.occurred_at).toLocaleString()}</time>
                </div>
                <p className="mt-2 break-all font-mono text-sm text-enamel">
                  {event.resource_type}{event.resource_id ? ` #${event.resource_id}` : ''}
                </p>
                {event.metadata && Object.keys(event.metadata).length > 0 ? (
                  <pre className={`mt-3 max-h-32 ${codeBlock}`}>{JSON.stringify(event.metadata, null, 2)}</pre>
                ) : null}
              </li>
            ))}
          </ol>
          </div>
        )}
      </Section>
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

const brokerLogEntryNoun = { one: 'broker log entry', many: 'broker log entries' }
const counterNoun = { one: 'counter', many: 'counters' }
const sysMetricNoun = { one: '$SYS metric', many: '$SYS metrics' }

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
  const logPager = useClientPage(logs?.entries ?? [], { pageSize: 25 })
  const counterPager = useClientPage(Object.entries(internal?.counters ?? {}), { pageSize: 25, match: ([name], query) => name.toLowerCase().includes(query) })
  const sysPager = useClientPage(Object.entries(sys?.metrics ?? {}), { pageSize: 25, match: ([topic], query) => topic.toLowerCase().includes(query) })
  const loading = <p className={`mt-3 ${bodyText}`}>Loading…</p>
  return <section className="mt-8 space-y-8">
    <Section title="Broker diagnostics">
      <div className="mt-3 flex flex-wrap items-end gap-4">
        <label className="flex flex-col gap-2"><span className={fieldLabel}>Minimum severity</span><select aria-label="Minimum severity" value={severity} onChange={(e) => setSeverity(e.target.value)} className={select}><option value="">Server default</option>{['debug','info','warning','error'].map((v) => <option key={v}>{v}</option>)}</select></label>
        <label className="flex flex-col gap-2"><span className={fieldLabel}>Entry limit</span><input aria-label="Entry limit" type="number" min="1" max="200" value={limit} onChange={(e) => setLimit(e.target.value === '' ? '' : String(Math.min(200, Math.max(1, Number(e.target.value)))))} className={`${input} w-28 font-mono`} placeholder="Default" /></label>
      </div>
      {error ? <p role="alert" className={`mt-4 ${notice.failed}`}>{error}</p> : null}
    </Section>
    <div className="grid gap-x-8 gap-y-8 border-t border-track pt-6 lg:grid-cols-2">
      <Section title="Observed traffic" ruled={false}>
        {traffic ? <>
          <p className={`mt-1 ${fieldHint}`}>Observed sample, not a complete broker inventory.</p>
          <dl className="mt-3 grid gap-3 sm:grid-cols-3"><WidgetFact label="Messages in window" value={String(traffic.message_count)} /><WidgetFact label="Window" value={`${traffic.window_seconds} seconds`} /><WidgetFact label="Rate" value={`${traffic.message_rate_per_minute.toFixed(1)} msg/min`} /></dl>
          <div className="mt-4 grid gap-6 xl:grid-cols-2"><HotspotCard title="Top topics" level={4} ruled={false} empty="No topic activity in the observed window." items={traffic.top_topics} />
            <div className={notice.neutral}><p className="font-semibold text-enamel">Client identity is not part of observed traffic</p><p className="mt-1">{traffic.top_clients_note || defaultClientNote}</p></div>
          </div>
        </> : loading}
      </Section>
      <Section title="MCM events" ruled={false}>
        {mcm ? <><dl className="mt-3 grid gap-3 sm:grid-cols-2"><WidgetFact label="Status events" value={String(mcm.status_events)} /><WidgetFact label="Topic messages" value={String(mcm.topic_messages)} /><WidgetFact label="Event subscribers" value={String(mcm.event_subscribers)} /><WidgetFact label="Last message at" value={mcm.last_message_at ? new Date(mcm.last_message_at).toLocaleString() : 'None yet'} /></dl><p className={`mt-3 ${fieldHint}`}>{mcm.note}</p></> : loading}
      </Section>
      <Section title="Broker logs" ruled={false}>
        {logs ? <>
          <dl className="mt-3 grid gap-3 sm:grid-cols-2"><WidgetFact label="Availability" value={logs.available ? 'Available' : 'Unavailable'} />{logs.available ? null : <WidgetFact label="Reason" value={logs.reason || 'No reason provided.'} />}{Object.entries(logs.severity_counts).map(([name, count]) => <WidgetFact key={name} label={`${name} entries`} value={String(count)} />)}</dl>
          <h5 className="mt-4 text-xs font-semibold text-enamel-soft">Effective limits</h5>
          <dl className="mt-2 grid gap-3 sm:grid-cols-2"><WidgetFact label="Max entries" value={String(logs.limits.max_entries)} /><WidgetFact label="Max entries per read" value={String(logs.limits.max_entries_per_read)} /><WidgetFact label="Max message bytes" value={String(logs.limits.max_message_bytes)} /><WidgetFact label="Max age" value={`${logs.limits.max_age_seconds} seconds`} /></dl>
          <p className={`mt-3 ${fieldHint}`}>{logs.note}</p>
          {logs.available ? <div className="mt-4 space-y-4">{logs.entries.length ? <><ClientPager pager={logPager} noun={brokerLogEntryNoun} /><div className="divide-y divide-track border-y border-track">{logPager.rows.map(({ item: entry, index }) => <article key={`${entry.observed_at}-${index}`} className="py-3">
            <div className="flex flex-wrap items-center gap-3"><time dateTime={entry.observed_at} className="font-mono text-xs tabular-nums text-enamel-muted">{new Date(entry.observed_at).toLocaleString()}</time><SeverityBadge severity={entry.severity} /><span className="font-mono text-xs text-enamel">{entry.kind}</span></div>
            {entry.client_id ? <p className="mt-2 text-xs text-enamel-soft">Client ID (broker-sourced): <span className="font-mono text-enamel-bright">{entry.client_id}</span></p> : null}
            {entry.topic ? <p className="mt-1 break-all text-xs text-enamel-soft">Topic: <span className="font-mono text-enamel-bright">{entry.topic}</span></p> : null}
            <p className="mt-2 whitespace-pre-wrap break-words font-mono text-xs leading-5 text-enamel-bright">{entry.message}</p>
          </article>)}</div></> : <p className={bodyText}>No broker log entries in this window.</p>}</div> : null}
        </> : loading}
      </Section>
      <Section title="Internal metrics" ruled={false}>
        {internal ? <><dl className="mt-3 grid gap-3 sm:grid-cols-2"><WidgetFact label="Endpoint" value={internal.endpoint} /><WidgetFact label="Format" value={internal.format} /></dl><h5 className="mt-4 text-xs font-semibold text-enamel-soft">Counters</h5><div className="mt-2"><ClientPager pager={counterPager} noun={counterNoun} searchLabel="Filter counters" /></div><ul className="mt-2 divide-y divide-track border-y border-track">{counterPager.rows.map(({ item: [name, value] }) => <li key={name} className="flex justify-between gap-3 py-2 text-sm"><span className="break-all font-mono text-enamel">{name}</span><span className="font-mono tabular-nums text-enamel-bright">{value}</span></li>)}</ul>{Object.keys(internal.counters).length === 0 ? <p className={`mt-2 ${bodyText}`}>No counters reported.</p> : null}<p className={`mt-3 ${fieldHint}`}>{internal.note}</p></> : loading}
      </Section>
    </div>
    <Section title="Broker-published $SYS metrics">
      {sys ? sys.available ? <><p className={`mt-1 ${fieldHint}`}>{sys.note}</p><div className="mt-3"><ClientPager pager={sysPager} noun={sysMetricNoun} searchLabel="Filter $SYS metrics" /></div><ul className="mt-3 divide-y divide-track border-y border-track">{sysPager.rows.map(({ item: [topic, metric] }) => <li key={topic} className="grid gap-2 py-2 sm:grid-cols-3"><span className="break-all font-mono text-sm text-enamel">{topic}</span><span className="font-mono text-sm tabular-nums text-enamel-bright">{metric.value}</span><time dateTime={metric.observed_at} className="font-mono text-xs tabular-nums text-enamel-muted">{new Date(metric.observed_at).toLocaleString()}</time></li>)}</ul>{Object.keys(sys.metrics).length === 0 ? <p className={`mt-3 ${bodyText}`}>No $SYS metrics observed yet.</p> : null}</> : <p className="mt-3 text-sm text-lamp">Unavailable: {sys.reason || 'No reason provided.'}</p> : loading}
    </Section>
    <Section title="Diagnostic checks"><div className="mt-3 grid gap-x-8 gap-y-4 sm:grid-cols-3 sm:divide-x sm:divide-track">{(['tcp','tls','mqtt'] as const).map((name) => { const check = data?.diagnostics?.[name]; return <div key={name} className="sm:px-6 sm:first:pl-0 sm:last:pr-0"><div className="flex items-center justify-between gap-2"><strong className="font-mono text-sm uppercase text-enamel-bright">{name}</strong><SeverityStatusBadge status={check?.status ?? 'not run'} /></div>
      {check ? <DiagnosticCheckDetail name={name} check={check} /> : <p className={`mt-2 ${fieldHint}`}>Check not run.</p>}
    </div> })}</div></Section>
  </section>
}

type DiagnosticCheck = NonNullable<NonNullable<DiagnosticsPayload['diagnostics']>[keyof NonNullable<DiagnosticsPayload['diagnostics']>]>
// Reachable is not verified: enamel when up, unlit when disabled or not run, amber otherwise.
function SeverityStatusBadge({ status }: { status: string }) {
  const tone: LampTone = status === 'connected' || status === 'reachable' ? 'neutral' : status === 'disabled' || status === 'not run' ? 'off' : 'caution'
  return <Lamp tone={tone}>{status}</Lamp>
}
function DiagnosticCheckDetail({ name, check }: { name: 'tcp' | 'tls' | 'mqtt'; check: DiagnosticCheck }) {
  const facts = name === 'tcp' && 'latency_ms' in check ? [['Latency', `${check.latency_ms} ms`], ['Address', check.address]] : name === 'tls' && 'version' in check ? [['TLS version', check.version || 'n/a'], ['Cipher', check.cipher || 'n/a']] : name === 'mqtt' && 'client_id' in check ? [['Client ID', check.client_id || 'n/a'], ['Subscriptions', check.subscriptions.join(', ') || 'None']] : []
  return <><dl className="mt-2 space-y-2">{facts.map(([label, value]) => <Fact key={label} label={label} value={<span className="break-all">{value}</span>} />)}</dl><p className={`mt-2 ${fieldHint}`}>{check.error || 'No error reported.'}</p></>
}

function ACLPanel({ token, onLogout, role = '', onMutationStart, onMutation }: { token: string; onLogout: () => void; role?: string; onMutationStart?: () => number; onMutation?: (mutationID: number) => void }) {
  const canWrite = can(role, 'acl.write')
  const list = usePagedList<ACLRule>({ path: '/api/v1/acls', rowsKey: 'rules', token, onUnauthorized: onLogout, namespace: 'acl', filterParams: { user: 'user', topic: 'topic' } })
  const rules = list.rows
  // Errors of the row actions; the load error is shown by ListControls.
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
  const fetchRules = list.reload

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
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h3 className="text-lg font-semibold text-enamel-bright">MQTT permissions</h3>
          <p className={`mt-1 max-w-[68ch] ${bodyText}`}>
            These are the permissions Mosquitto grants on a topic; the MCM administrative roles are managed in the
            admin panel.
          </p>
        </div>
        <button
          type="button"
          onClick={openCreate}
          disabled={!canWrite}
          title={!canWrite ? permissionTitle('acl.write') : undefined}
          className={leverSecondary}
        >
          Add Rule
        </button>
      </div>

      {error ? (
        <div className={notice.failed}>{error}</div>
      ) : null}

      {showForm ? (
        <div className="max-w-2xl border-y border-track-strong py-6">
          <h4 className="text-base font-semibold text-enamel-bright">{editRule ? 'Edit rule' : 'New rule'}</h4>
          <form onSubmit={handleSubmit} className="mt-4 space-y-5">
            <label className="block">
              <span className={fieldLabel}>Source</span>
              <select
                value={formSelector}
                onChange={(e) => setFormSelector(e.target.value as ACLSelector)}
                className={`mt-2 w-full ${input}`}
              >
                <option value="user">User &mdash; the rule applies to one MQTT user</option>
                <option value="anonymous">Anonymous &mdash; the rule applies to unauthenticated clients</option>
                <option value="pattern">Pattern &mdash; the rule applies to every client</option>
              </select>
              <span className={`mt-1 block ${fieldHint}`}>
                The native scope of the ACL entry in Mosquitto. A pattern rule may use the <code>%u</code> (username) and
                <code> %c</code> (client id) placeholders, each one on a whole topic level.
              </span>
            </label>
            {formSelector === 'user' ? (
              <label className="block">
                <span className={fieldLabel}>Principal</span>
                <input
                  type="text"
                  required
                  value={formPrincipal}
                  onChange={(e) => setFormPrincipal(e.target.value)}
                  placeholder="mqtt-username"
                  className={`mt-2 w-full ${input}`}
                />
                <span className={`mt-1 block ${fieldHint}`}>Use an MQTT username from the Users panel. MCM renders this as Mosquitto&apos;s <code>user &lt;username&gt;</code> directive.</span>
              </label>
            ) : null}
            <label className="block">
              <span className={fieldLabel}>Topic filter</span>
              <input
                type="text"
                required
                value={formTopicFilter}
                onChange={(e) => setFormTopicFilter(e.target.value)}
                placeholder="sensors/# or device/+/status"
                className={`mt-2 w-full ${input}`}
              />
              <span className={`mt-1 block ${fieldHint}`}>MQTT wildcards: + (single level), # (multi-level, must be last)</span>
            </label>
            <label className="block">
              <span className={fieldLabel}>Permission</span>
              <select
                value={formPermission}
                onChange={(e) => setFormPermission(e.target.value as ACLRule['permission'])}
                className={`mt-2 w-full ${input}`}
              >
                <option value="read">read</option>
                <option value="write">write</option>
                <option value="readwrite">readwrite</option>
                <option value="deny">deny</option>
              </select>
              <span className={`mt-1 block ${fieldHint}`}>
                Mosquitto handles a deny before any grant, so a deny that matches a topic removes the access a broader
                rule would grant.
              </span>
            </label>
            <label className="block">
              <span className={fieldLabel}>ACL file</span>
              <select
                value={formScope}
                onChange={(e) => setFormScope(e.target.value as ACLScope)}
                className={`mt-2 w-full ${input}`}
              >
                <option value="global">Broker-wide &mdash; the rule applies to every listener</option>
                <option value="listener">One listener &mdash; the rule applies to that listener only</option>
              </select>
              <span className={`mt-1 block ${fieldHint}`}>
                A listener with its own ACL file uses only that file, so the other listeners keep the broker-wide rules.
              </span>
            </label>
            {formScope === 'listener' ? (
              <label className="block">
                <span className={fieldLabel}>Listener</span>
                <input
                  type="text"
                  required
                  value={formListenerID}
                  onChange={(e) => setFormListenerID(e.target.value)}
                  placeholder="tls"
                  className={`mt-2 w-full ${input}`}
                />
                <span className={`mt-1 block ${fieldHint}`}>
                  The id of the listener that owns the rule. The listener must declare the ACL file MCM writes for it,
                  otherwise the deploy refuses the scope.
                </span>
              </label>
            ) : null}
            <label className="block">
              <span className={fieldLabel}>Description (optional)</span>
              <input
                type="text"
                value={formDescription}
                onChange={(e) => setFormDescription(e.target.value)}
                placeholder="Short description for audit purposes"
                className={`mt-2 w-full ${input}`}
              />
            </label>
            {formError ? (
              <div className={notice.failed}>{formError}</div>
            ) : null}
            <div className="flex flex-wrap items-center gap-3">
              <button
                type="submit"
                disabled={submitting || !canWrite}
                title={!canWrite ? permissionTitle('acl.write') : undefined}
                className={leverPrimary}
              >
                {submitting ? 'Saving…' : editRule ? 'Update rule' : 'Create rule'}
              </button>
              <button
                type="button"
                onClick={() => setShowForm(false)}
                className={leverSecondary}
              >
                Cancel
              </button>
            </div>
          </form>
        </div>
      ) : null}

      <ListControls list={list} noun={{ one: 'rule', many: 'rules' }} filterFields={[{ key: 'user', label: 'Filter by user' }, { key: 'topic', label: 'Filter by topic' }]} />

      <div className="table-scroll border-y border-track-strong">
        {rules.length === 0 && list.loading ? (
          <p className={`py-6 ${bodyText}`}>Loading rules…</p>
        ) : rules.length === 0 ? (
          list.error ? null : <p className={`py-6 ${bodyText}`}>{list.hasFilters ? 'No ACL rules match the filters.' : 'No ACL rules configured yet.'}</p>
        ) : (
          <table className="w-full min-w-[44rem] text-sm">
            <thead>
              <tr className="border-b border-track-strong">
                <th className={`${tableHead} text-left`}>Source</th>
                <th className={`${tableHead} text-left`}>ACL file</th>
                <th className={`${tableHead} text-left`}>Topic filter</th>
                <th className={`${tableHead} text-left`}>Permission</th>
                <th className={`${tableHead} pr-0 text-right`}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {rules.map((rule) => (
                <tr key={rule.id} className={tableRow}>
                  <td className="py-3 pr-4 text-sm text-enamel">
                    <span className="text-xs text-enamel-muted">{SELECTOR_LABELS[rule.selector ?? 'user']}</span>
                    {rule.principal ? <span className="ml-2 font-mono text-sm text-enamel-bright">{rule.principal}</span> : null}
                  </td>
                  <td className="py-3 pr-4 font-mono text-xs text-enamel-soft">
                    {rule.scope === 'listener' ? rule.listener_id : 'broker-wide'}
                  </td>
                  <td className="py-3 pr-4 font-mono text-sm text-enamel-bright">{rule.topic_filter}</td>
                  <td className="py-3 pr-4">
                    <Plate>{PERMISSION_LABELS[rule.permission]}</Plate>
                  </td>
                  <td className="py-3 text-right">
                    {deleteConfirmId === rule.id ? (
                      <span className="inline-flex items-center gap-2">
                        <span className="text-xs text-enamel-soft">Delete?</span>
                        <button
                          type="button"
                          onClick={() => { void handleDelete(rule.id) }}
                          className={leverDanger}
                        >
                          Confirm
                        </button>
                        <button
                          type="button"
                          onClick={() => setDeleteConfirmId(null)}
                          className={leverSecondary}
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
                          className={leverSecondary}
                        >
                          Edit
                        </button>
                        <button
                          type="button"
                          onClick={() => setDeleteConfirmId(rule.id)}
                          disabled={!canWrite}
                          title={!canWrite ? permissionTitle('acl.write') : undefined}
                          className={leverDanger}
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

      <ACLSourcePanel token={token} onLogout={onLogout} canWrite={canWrite} onMutationStart={onMutationStart} onMutation={onMutation} />

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
  const [applyError, setApplyError] = useState('')
  const history = usePagedList<Deployment>({ path: '/api/v1/deployments', rowsKey: 'deployments', token, onUnauthorized: onLogout, namespace: 'deploy', filterParams: { status: 'status' } })
  const deployments = history.rows
  const scopeBlocked = Boolean(preview?.scope_issues?.length)
  const listenerAtRisk = (preview?.listener_acl_files ?? []).flatMap((file) => (file.at_risk_entries ?? []).map((entry) => ({ listener: file.listener_id, entry })))
  const fetchHistory = history.reload

  // usePagedList reports a failed load without its status, and a 404/422
  // means deploy is not configured on this instance: ask once to tell them apart.
  useEffect(() => {
    if (!history.error) return
    let cancelled = false
    authenticatedFetch('/api/v1/deployments?limit=1', { token, onUnauthorized: onLogout })
      .then((response) => {
        if (!cancelled && (response.status === 404 || response.status === 422)) setUnavailable(true)
      })
      .catch(() => {})
    return () => { cancelled = true }
  }, [history.error, token, onLogout])

  const handlePreview = async () => {
    if (previewLoading) return
    setPreviewLoading(true)
    setPreviewError('')
    // Clearing the preview also drops ChangeRoute's started apply back to Review:
    // a new revision invalidates the confirmation of the previous one.
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

  const handleApply = async (proof: string): Promise<Deployment | null> => {
    if (!preview?.revision_id) {
      setApplyError('Preview revision is missing. Generate a new preview before applying.')
      return null
    }

    const mutationWatermark = onApplyStart?.() ?? 0
    setApplyError('')
    try {
      const response = await authenticatedFetch('/api/v1/deployments/apply', {
        token,
        onUnauthorized: onLogout,
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-MCM-MFA': proof },
        body: JSON.stringify({ revision_id: preview.revision_id }),
      })
      if (response.status === 404 || response.status === 422) {
        setUnavailable(true)
        return null
      }
      if (!response.ok) {
        const errBody = (await response.json().catch(() => null)) as { error?: string; details?: string[] } | null
        const details = errBody?.details?.join('; ')
        setApplyError(details ? `${errBody?.error ?? 'Apply failed.'} ${details}` : errBody?.error ?? 'Apply failed.')
        setPreview(null) // the server consumed this revision, even though the apply failed
        return null
      }
      const result = (await response.json()) as Deployment
      setPreview(null)
      // `applied` wrote the config without activation proof: the saved changes are no
      // longer pending a deploy (redeploying would not prove them), so clear the marker.
      if (result.status === 'active_verified' || result.status === 'applied') {
        onApplySuccess?.(mutationWatermark)
      }
      fetchHistory()
      return result
    } catch (err) {
      if (isUnauthorizedResponseError(err)) return null
      if (isMFARejection(err)) mfa.clear()
      // A 403 other than the recent-MFA rejection was answered, so the revision is consumed.
      else if (isForbiddenResponseError(err)) setPreview(null)
      setApplyError(isForbiddenResponseError(err) ? (err as Error).message : 'Could not reach the server.')
      return null
    }
  }

  if (unavailable) {
    return (
      <section className="mt-8">
        <div className={notice.caution}>
          <h3 className="font-semibold text-lamp">Not configured</h3>
          <p className="mt-1">Deploy functionality is not configured on this MCM instance.</p>
        </div>
      </section>
    )
  }

  return (
    <section className="mt-8 space-y-10">
      <Section
        title="Configuration preview"
        description="Generate a diff of pending ACL and password file changes before applying."
        aside={
          <button
            type="button"
            onClick={() => { void handlePreview() }}
            // aria-disabled, not disabled, while generating: a disabled button drops keyboard focus to the body (#359).
            disabled={!canPreview}
            aria-disabled={previewLoading || undefined}
            title={!canPreview ? permissionTitle('deploy.preview') : undefined}
            // The view's one primary lever until a preview with changes hands it to Apply (#359).
            className={preview?.has_changes ? leverSecondary : leverPrimary}
          >
            {previewLoading ? 'Generating…' : 'Preview Changes'}
          </button>
        }
      >
        {previewError ? (
          <div role="alert" className={`mt-5 ${notice.failed}`}>{previewError}</div>
        ) : null}

        {applyError ? (
          <div role="alert" className={`mt-5 ${notice.failed}`}>{applyError}</div>
        ) : null}
        {applyError && !preview ? <p className="mt-2 text-sm text-lamp">Generate a new preview before applying again.</p> : null}

        <div className="mt-5">
        <ChangeRoute hasChanges={Boolean(preview?.has_changes)} reviewKey={preview?.revision_id} mfa={mfa} onApply={handleApply} applyLabel="Apply" disabledReason={!canApply ? `${permissionTitle('deploy.apply')} to apply.` : scopeBlocked ? 'Resolve the listener scope problems before applying.' : undefined}>
        {preview ? (
          <div className="space-y-6">
            {!preview.has_changes && !(preview.orphan_rules?.length) ? (
              <p className={notice.neutral}>No pending changes — configuration is up to date.</p>
            ) : (
              <Section level={4} ruled={false} title="What this deploy covers">
                <p className={`mt-1 max-w-[68ch] ${bodyText}`}>Apply deploys every pending change saved in MCM, including changes saved outside this task.</p>
                <p className={`mt-1 max-w-[68ch] ${bodyText}`}>MCM cannot tell which task or person saved a pending change, so review the whole preview before you confirm.</p>
                {preview.summary ? (
                  <>
                    <dl className="mt-4 grid grid-cols-2 gap-x-6 gap-y-3 border-y border-track py-3 sm:grid-cols-5">
                      {([
                        ['MQTT users added', preview.summary.users_added],
                        ['MQTT users removed', preview.summary.users_removed],
                        ['MQTT users rotated', preview.summary.users_rotated],
                        ['ACL topics added', preview.summary.topics_added],
                        ['ACL topics removed', preview.summary.topics_removed],
                      ] as const).map(([label, count]) => (
                        <Fact key={label} label={label} value={<span className="text-lg tabular-nums">{count}</span>} />
                      ))}
                    </dl>
                    <p className={`mt-2 ${fieldHint}`}>These counts cover the broker-wide password and ACL files. The listener files are listed separately below.</p>
                  </>
                ) : null}
              </Section>
            )}
            {preview.scope_issues?.length ? (
              <PreviewBlock tone="failed" title="Listener scope problems" count={preview.scope_issues.length}>
                <p className={`mt-2 ${bodyText}`}>The listener-scoped rules would never reach the broker. The apply is refused until these are resolved.</p>
                <ul aria-label="Listener scope problems" tabIndex={0} className={`mt-3 space-y-2 font-mono text-xs text-enamel-bright ${reviewScroll}`}>
                  {preview.scope_issues.map((issue) => (
                    <li key={issue} className="break-words">{issue}</li>
                  ))}
                </ul>
                <p className="mt-3 text-sm font-semibold text-danger-text">Apply is disabled: the deploy is refused until the scope problems are resolved.</p>
              </PreviewBlock>
            ) : null}
            {preview.orphan_rules?.length ? (
              <PreviewBlock tone="caution" title="Orphan ACL rules" count={preview.orphan_rules.length}>
                <p className={`mt-2 ${bodyText}`}>These rules reference missing or disabled MQTT users and will be excluded from the rendered Mosquitto ACL file. Delete or reassign them before applying.</p>
                <div role="region" aria-label="Orphan ACL rules" tabIndex={0} className={`mt-3 table-scroll ${reviewScroll}`}>
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="border-b border-track-strong">
                        <th className={`${tableHead} text-left`}>Principal</th>
                        <th className={`${tableHead} text-left`}>Topic filter</th>
                        <th className={`${tableHead} text-left`}>Permission</th>
                      </tr>
                    </thead>
                    <tbody>
                      {preview.orphan_rules.map((rule) => (
                        <tr key={rule.id} className={tableRow}>
                          <td className="break-all py-2 pr-4 font-mono text-enamel-bright">{rule.principal}</td>
                          <td className="break-all py-2 pr-4 font-mono text-enamel">{rule.topic_filter}</td>
                          <td className="py-2 pr-4 font-mono text-enamel-soft">{rule.permission}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </PreviewBlock>
            ) : null}
            {preview.at_risk_entries?.length ? (
              <PreviewBlock tone="caution" title="Access at risk" count={preview.at_risk_entries.length}>
                <p className={`mt-2 ${bodyText}`}>The current file grants these entries and the rendered one does not. The deploy would drop that access.</p>
                <ul aria-label="Access at risk" tabIndex={0} className={`mt-3 space-y-1 font-mono text-xs text-enamel-bright ${reviewScroll}`}>
                  {preview.at_risk_entries.map((entry) => (
                    <li key={entry} className="break-all">{entry}</li>
                  ))}
                </ul>
              </PreviewBlock>
            ) : null}
            {listenerAtRisk.length ? (
              <PreviewBlock tone="caution" title="Listener access at risk">
                <ul aria-label="Listener access at risk" tabIndex={0} className={`mt-3 space-y-1 font-mono text-xs text-enamel-bright ${reviewScroll}`}>
                  {listenerAtRisk.map(({ listener, entry }) => (
                    <li key={`${listener}|${entry}`} className="break-all">at risk ({listener}): {entry}</li>
                  ))}
                </ul>
              </PreviewBlock>
            ) : null}
            {preview.composition_warnings?.length ? (
              <PreviewBlock tone="caution" title="Composition warnings">
                <ul aria-label="Composition warnings" tabIndex={0} className={`mt-3 space-y-2 text-sm text-enamel ${reviewScroll}`}>
                  {preview.composition_warnings.map((warning) => (
                    <li key={warning} className="break-words">{warning}</li>
                  ))}
                </ul>
              </PreviewBlock>
            ) : null}
            {preview.scope_pending_activation?.length ? (
              <PreviewBlock tone="neutral" title="Pending listener activation" count={preview.scope_pending_activation.length}>
                <p className={`mt-2 ${bodyText}`}>The files of these listeners are written by this deployment. Their rules become active once the listener configuration declares them.</p>
                <ul aria-label="Pending listener activation" tabIndex={0} className={`mt-3 space-y-2 text-sm text-enamel ${reviewScroll}`}>
                  {preview.scope_pending_activation.map((item) => (
                    <li key={item} className="break-words">{item}</li>
                  ))}
                </ul>
              </PreviewBlock>
            ) : null}
            {preview.removed_acl_files?.length ? (
              <PreviewBlock tone="caution" title="Files the deploy would delete">
                <p className={`mt-2 ${bodyText}`}>These listener files lose their last scoped rule, so the deploy removes them.</p>
                <ul aria-label="Files the deploy would delete" tabIndex={0} className={`mt-3 space-y-1 font-mono text-xs text-enamel-bright ${reviewScroll}`}>
                  {preview.removed_acl_files.map((path) => (
                    <li key={path} className="break-all">{path}</li>
                  ))}
                </ul>
              </PreviewBlock>
            ) : null}
            {preview.acl_diff || preview.passwd_diff || preview.preserved_entries?.length || preview.listener_acl_files?.length ? (
              <details className="border-t border-track pt-4">
                <summary className="cursor-pointer text-sm font-semibold text-enamel-bright transition-colors duration-150 hover:text-enamel">Show diffs and file contents</summary>
                <div className="mt-4 space-y-6">
                  {preview.preserved_entries?.length ? (
                    <PreviewBlock tone="neutral" title="Preserved entries" count={preview.preserved_entries.length}>
                      <p className={`mt-2 ${bodyText}`}>These lines of the adopted file survive into the rendered one — nothing of the import is lost.</p>
                      <ul aria-label="Preserved entries" tabIndex={0} className={`mt-3 space-y-1 font-mono text-xs text-enamel-bright ${reviewScroll}`}>
                        {preview.preserved_entries.map((entry) => (
                          <li key={entry} className="break-all">{entry}</li>
                        ))}
                      </ul>
                    </PreviewBlock>
                  ) : null}
                  {preview.listener_acl_files?.length ? <div role="region" aria-label="Listener files" tabIndex={0} className={`space-y-6 ${reviewScroll}`}>{preview.listener_acl_files.map((file) => (
                    <div key={file.path}>
                      <div className="flex flex-wrap items-baseline justify-between gap-3">
                        <h5 className="text-sm font-semibold text-enamel-bright">Listener file — {file.listener_id}</h5>
                        <span className="break-all font-mono text-xs text-enamel-muted">{file.path}</span>
                      </div>
                      <p className={`mt-1 ${bodyText}`}>
                        This listener reads only this file, so its rules apply to that listener alone.
                      </p>
                      {file.diff ? <div className="mt-3"><DiffBlock content={file.diff} /></div> : null}
                    </div>
                  ))}</div> : null}
                  {preview.acl_diff ? (
                    <div>
                      <h5 className="mb-2 text-sm font-semibold text-enamel-bright">ACL diff</h5>
                      <DiffBlock content={preview.acl_diff} />
                    </div>
                  ) : null}
                  {preview.passwd_diff ? (
                    <div>
                      <h5 className="mb-2 text-sm font-semibold text-enamel-bright">Password file diff</h5>
                      <DiffBlock content={preview.passwd_diff} />
                    </div>
                  ) : null}
                </div>
              </details>
            ) : null}
          </div>
        ) : null}
        </ChangeRoute>
        </div>
      </Section>

      <Section title="Deployment history">
        <div className="mt-4">
          <ListControls list={history} noun={{ one: 'deployment', many: 'deployments' }} filterFields={[{ key: 'status', label: 'Filter by status', options: deploymentStatusOptions }]} />
        </div>

        {history.error ? null : deployments.length === 0 && history.loading ? (
          <p className={`mt-4 ${bodyText}`}>Loading history…</p>
        ) : deployments.length === 0 ? (
          <p className={`mt-4 ${bodyText}`}>{history.hasFilters ? 'No deployments match the filter.' : 'No deployments recorded yet.'}</p>
        ) : (
          <div className="mt-4 table-scroll">
          <table className="w-full min-w-[40rem] text-sm">
            <thead>
              <tr className="border-b border-track-strong">
                <th className={`${tableHead} text-left`}>ID</th>
                <th className={`${tableHead} text-left`}>Status</th>
                <th className={`${tableHead} text-left`}>Message</th>
                <th className={`${tableHead} pr-0 text-right`}>Time</th>
              </tr>
            </thead>
            <tbody>
              {deployments.map((dep) => (
                <tr key={dep.id} className={tableRow}>
                  <td className="py-3 pr-4 font-mono text-xs text-enamel-muted">{String(dep.id).slice(0, 8)}</td>
                  <td className="py-3 pr-4">
                    <Lamp tone={deploymentLamp(dep.status)}>{deploymentStatusLabel[dep.status] ?? dep.status}</Lamp>
                  </td>
                  <td className="py-3 pr-4 text-sm text-enamel-soft">{dep.message ?? '—'}</td>
                  <td className="whitespace-nowrap py-3 text-right font-mono text-xs tabular-nums text-enamel-muted">{new Date(dep.created_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
          </div>
        )}
      </Section>
    </section>
  )
}

// One block of the deploy preview: a lamp-lit heading with its count on a hairline, no box.
// Deploy review lists stay whole (S3): bounded height with a scroll, never pages.
const reviewScroll = 'max-h-96 overflow-y-auto'

// The block's severity in words next to its lamp: colour never carries it alone (#359).
const previewSeverity = { failed: 'Blocks apply', caution: 'Review before applying', neutral: 'For information' } as const

function PreviewBlock({ tone, title, count, children }: { tone: keyof typeof previewSeverity; title: string; count?: number; children: ReactNode }) {
  return (
    <section className="border-t border-track pt-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h4 className="text-sm font-semibold text-enamel-bright">{title}</h4>
          <Lamp tone={tone}>{previewSeverity[tone]}</Lamp>
        </div>
        {count !== undefined ? <Plate tone={tone === 'caution' ? 'caution' : 'neutral'}>{count}</Plate> : null}
      </div>
      {children}
    </section>
  )
}

// Deployment statuses the history can be filtered by (S10b: exact match).
const deploymentStatusOptions = [
  { value: '', label: 'All statuses' },
  ...['active_verified', 'applied', 'pending_activation', 'failed', 'rolled_back', 'rollback_failed'].map((status) => ({ value: status, label: deploymentStatusLabel[status] })),
]

function LoginScreen({ onLogin }: { onLogin: (token: string, user: AdminUser) => void }) {
  const [username, setUsername] = useState<string>('')
  const [password, setPassword] = useState<string>('')
  const [confirmation, setConfirmation] = useState<string>('')
  const [setupToken, setSetupToken] = useState<string>('')
  const [setupStatus, setSetupStatus] = useState<'required' | 'token_not_configured' | 'complete'>('complete')
  const [confirmationError, setConfirmationError] = useState<string>('')
  const [submitting, setSubmitting] = useState<boolean>(false)
  const [error, setError] = useState<string>('')
  const [mfaChallenge, setMfaChallenge] = useState<string>('')
  const [mfaCode, setMfaCode] = useState<string>('')

  useEffect(() => {
    let cancelled = false
    fetch('/api/v1/auth/setup', { cache: 'no-store' })
      .then(async (response) => {
        if (!response.ok) throw new Error('Unable to read setup status.')
        return response.json() as Promise<{ status?: string }>
      })
      .then((body) => {
        if (cancelled) return
        if (body.status === 'required' || body.status === 'token_not_configured') setSetupStatus(body.status)
      })
      .catch(() => {
        // Setup status is advisory; a failure must never prevent ordinary login.
      })
    return () => { cancelled = true }
  }, [])

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

  const handleSetupSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting) return
    if (password !== confirmation) {
      setConfirmationError('Passwords do not match.')
      return
    }
    setConfirmationError('')
    setSubmitting(true)
    setError('')
    try {
      const response = await fetch('/api/v1/auth/setup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: username.trim(), password, password_confirmation: confirmation, setup_token: setupToken }),
      })
      const body = (await response.json().catch(() => null)) as (LoginResponse & { error?: string }) | null
      if (response.status === 409) {
        setSetupStatus('complete')
        setError('This instance is already configured. Sign in with an existing account.')
        return
      }
      if (!response.ok) {
        setError(body?.error ?? 'Administrator setup failed. Please try again.')
        return
      }
      if (body?.token && body.user) {
        onLogin(body.token, body.user)
        return
      }
      setError('Setup response was incomplete. Please retry.')
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
    <div className="flex min-h-screen items-center justify-center bg-frame px-4 py-12 text-enamel">
      <div className="w-full max-w-sm">
        <p className="flex items-center gap-2.5 font-display text-base font-semibold uppercase leading-none tracking-[0.1em] text-enamel-bright">
          <Mark />
          Mosquitto Control Manager
        </p>
        <div className="mt-10">
          <h1 className="font-display text-4xl font-bold uppercase leading-none text-enamel-bright">
            {mfaChallenge ? 'Verify MFA' : setupStatus === 'required' ? 'Create administrator account' : setupStatus === 'token_not_configured' ? 'Setup unavailable' : 'Sign in'}
          </h1>
          <div aria-hidden="true" className="relative mt-4 h-px bg-track-strong">
            <span className="absolute -top-px left-0 h-[3px] w-12 bg-enamel" />
          </div>
          <p className="mt-4 text-sm leading-relaxed text-enamel-soft">
            {mfaChallenge
              ? 'Enter the 6-digit code from your authenticator app, or one of your recovery codes.'
              : setupStatus === 'required'
                ? 'Choose a username and password for the first administrator. Configure MCM_SETUP_TOKEN on the server and enter it here; the setup code is not your password.'
                : setupStatus === 'token_not_configured'
                  ? 'First-admin setup is locked until the operator configures MCM_SETUP_TOKEN on the server. No account can be created from this screen.'
                  : 'Authenticate with an admin user to access the Mosquitto Control Manager dashboard.'}
          </p>
        </div>

        {mfaChallenge ? (
          <form onSubmit={handleMFASubmit} className="mt-6 space-y-4">
            <label className="block">
              <span className="text-sm font-semibold text-enamel">Code</span>
              <input
                type="text"
                inputMode="text"
                autoComplete="one-time-code"
                required
                autoFocus
                value={mfaCode}
                onChange={(event) => setMfaCode(event.target.value)}
                className="mt-2 w-full rounded-plate border border-track-bright bg-frame-raised px-3 py-2.5 font-mono text-sm tracking-[0.3em] text-enamel-bright transition-colors duration-150 hover:border-enamel-muted focus-visible:border-enamel-bright"
              />
            </label>

            {error ? (
              <div role="alert" className="border border-danger/60 px-4 py-3 text-sm text-danger-text">
                {error}
              </div>
            ) : null}

            <button
              type="submit"
              disabled={submitting}
              className={`w-full ${leverPrimary}`}
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
              className="w-full py-2 text-sm text-enamel-soft underline decoration-track-bright underline-offset-4 transition-colors duration-150 hover:text-enamel-bright"
            >
              ← Back to sign in
            </button>
          </form>
        ) : setupStatus === 'token_not_configured' ? null : setupStatus === 'required' ? (
          <form onSubmit={handleSetupSubmit} className="mt-6 space-y-4">
            <label className="block">
              <span className="text-sm font-semibold text-enamel">Username</span>
              <input type="text" autoComplete="username" required value={username} onChange={(event) => setUsername(event.target.value)} className="mt-2 w-full rounded-plate border border-track-bright bg-frame-raised px-3 py-2.5 font-mono text-sm text-enamel-bright transition-colors duration-150 hover:border-enamel-muted focus-visible:border-enamel-bright" />
            </label>
            <label className="block">
              <span className="text-sm font-semibold text-enamel">Password</span>
              <input type="password" autoComplete="new-password" required value={password} onChange={(event) => { setPassword(event.target.value); setConfirmationError('') }} className="mt-2 w-full rounded-plate border border-track-bright bg-frame-raised px-3 py-2.5 font-mono text-sm text-enamel-bright transition-colors duration-150 hover:border-enamel-muted focus-visible:border-enamel-bright" />
            </label>
            <label className="block">
              <span className="text-sm font-semibold text-enamel">Confirm password</span>
              <input type="password" autoComplete="new-password" required value={confirmation} aria-invalid={confirmationError ? true : undefined} aria-describedby={confirmationError ? 'confirm-password-error' : undefined} onChange={(event) => { setConfirmation(event.target.value); setConfirmationError('') }} className="mt-2 w-full rounded-plate border border-track-bright bg-frame-raised px-3 py-2.5 font-mono text-sm text-enamel-bright transition-colors duration-150 hover:border-enamel-muted focus-visible:border-enamel-bright" />
              {confirmationError ? <span id="confirm-password-error" role="alert" className="mt-1 block text-sm text-danger-text">{confirmationError}</span> : null}
            </label>
            <label className="block">
              <span className="text-sm font-semibold text-enamel">Setup code</span>
              <input type="password" autoComplete="off" required value={setupToken} onChange={(event) => setSetupToken(event.target.value)} className="mt-2 w-full rounded-plate border border-track-bright bg-frame-raised px-3 py-2.5 font-mono text-sm text-enamel-bright transition-colors duration-150 hover:border-enamel-muted focus-visible:border-enamel-bright" />
            </label>
            {error ? <div role="alert" className="border border-danger/60 px-4 py-3 text-sm text-danger-text">{error}</div> : null}
            <button type="submit" disabled={submitting} className={`w-full ${leverPrimary}`}>
              {submitting ? 'Creating…' : 'Create administrator'}
            </button>
          </form>
        ) : (
          <form onSubmit={handlePasswordSubmit} className="mt-6 space-y-4">
            <label className="block">
              <span className="text-sm font-semibold text-enamel">Username</span>
              <input
                type="text"
                autoComplete="username"
                required
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                className="mt-2 w-full rounded-plate border border-track-bright bg-frame-raised px-3 py-2.5 font-mono text-sm text-enamel-bright transition-colors duration-150 hover:border-enamel-muted focus-visible:border-enamel-bright"
              />
            </label>

            <label className="block">
              <span className="text-sm font-semibold text-enamel">Password</span>
              <input
                type="password"
                autoComplete="current-password"
                required
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                className="mt-2 w-full rounded-plate border border-track-bright bg-frame-raised px-3 py-2.5 font-mono text-sm text-enamel-bright transition-colors duration-150 hover:border-enamel-muted focus-visible:border-enamel-bright"
              />
            </label>

            {error ? (
              <div role="alert" className="border border-danger/60 px-4 py-3 text-sm text-danger-text">
                {error}
              </div>
            ) : null}

            <button
              type="submit"
              disabled={submitting}
              className={`w-full ${leverPrimary}`}
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
