import { useCallback, useEffect, useState, type FormEvent } from 'react'
import MQTTUsersPanel from './features/mqtt-users/MQTTUsersPanel'
import { useAuthSession, type AdminUser } from './features/auth/useAuthSession'
import { DashboardFrame, type NavItem } from './features/dashboard/DashboardFrame'
import {
  defaultClientNote,
  useBrokerStream,
  type BrokerLog,
  type BrokerRatePoint,
  type BrokerTrafficItem,
  type BrokerTrafficMetrics,
  type BrokerStreamState,
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

type ACLRule = {
  id: string
  principal: string
  topic_filter: string
  permission: 'read' | 'write' | 'readwrite'
  description?: string
}

type Deployment = {
  id: string
  status: 'applied' | 'rolled_back' | 'rollback_failed' | string
  message?: string
  created_at: string
}

type DeployPreview = {
  acl_diff: string
  passwd_diff: string
  has_changes: boolean
}

type LoginResponse = {
  token?: string
  expires_at?: string
  user?: AdminUser
  mfa_required?: boolean
  mfa_challenge?: string
}

const navItems: NavItem[] = [
  {
    id: 'dashboard',
    label: 'Dashboard',
    path: '/dashboard',
    eyebrow: 'Broker overview',
    title: 'Live broker snapshot',
    description: 'Connection state and the latest topic traffic from the configured Mosquitto broker.',
  },
  {
    id: 'topics',
    label: 'Topics',
    path: '/topics',
    eyebrow: 'Traffic',
    title: 'Topic explorer',
    description: 'Inspect incoming topic names and safe payload previews as messages arrive.',
  },
  {
    id: 'logs',
    label: 'Logs',
    path: '/logs',
    eyebrow: 'Operations',
    title: 'Realtime broker logs',
    description: 'Monitor connection transitions and MCM broker operational events as they are ingested.',
  },
  {
    id: 'users',
    label: 'Users',
    path: '/users',
    eyebrow: 'Identity',
    title: 'MQTT user directory',
    description: 'Provision MQTT users, toggle account status, reset credentials, and remove stale accounts.',
  },
  {
    id: 'acls',
    label: 'ACLs',
    path: '/acls',
    eyebrow: 'Authorization',
    title: 'ACL policy workspace',
    description: 'Topic permissions, policy reviews, and audit-safe change workflows.',
  },
  {
    id: 'deploy',
    label: 'Deploy',
    path: '/deploy',
    eyebrow: 'Operations',
    title: 'Mosquitto configuration deploy',
    description: 'Preview, apply, and track configuration changes to the Mosquitto broker.',
  },
  {
    id: 'security',
    label: 'Security',
    path: '/security',
    eyebrow: 'Audit',
    title: 'Recent security events',
    description: 'Review failed admin logins, disabled-user login attempts, protected API failures, and ACL API audit hooks.',
  },
  {
    id: 'audit',
    label: 'Audit',
    path: '/audit',
    eyebrow: 'Security',
    title: 'Administrative audit log',
    description: 'Review recent administrative changes, actors, affected resources, and outcomes.',
  },
  {
    id: 'settings',
    label: 'Settings',
    path: '/settings',
    eyebrow: 'System',
    title: 'Platform settings placeholder',
    description: 'Global defaults, integration configuration, and broker-level safety controls.',
  },
]

function navIdFromPath(pathname: string) {
  const normalizedPath = pathname.replace(/\/+$/, '') || '/dashboard'
  return navItems.find((item) => item.path === normalizedPath)?.id ?? navItems[0].id
}

function App() {
  const { token, currentUser, handleLogin, handleLogout } = useAuthSession()

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

  return <Dashboard token={token} currentUser={currentUser} onLogout={handleLogout} />
}

function Dashboard({ token, currentUser, onLogout }: { token: string; currentUser: AdminUser; onLogout: () => void }) {
  const [activeId, setActiveId] = useState<string>(() => navIdFromPath(window.location.pathname))
  const [securityEvents, setSecurityEvents] = useState<SecurityEvent[]>([])
  const [securityError, setSecurityError] = useState<string>('')
  const [auditEvents, setAuditEvents] = useState<AuditEvent[]>([])
  const [auditError, setAuditError] = useState<string>('')
  const activeItem = navItems.find((item) => item.id === activeId) ?? navItems[0]
  const {
    brokerStatus,
    streamState,
    topics,
    latestTopic,
    logs,
    uniqueTopicCount,
    liveTrafficMetrics,
  } = useBrokerStream(token)

  const handleSelectNav = useCallback((item: NavItem) => {
    if (window.location.pathname !== item.path) {
      window.history.pushState(null, '', item.path)
    }
    setActiveId(item.id)
  }, [])

  useEffect(() => {
    const handlePopState = () => setActiveId(navIdFromPath(window.location.pathname))
    window.addEventListener('popstate', handlePopState)
    return () => window.removeEventListener('popstate', handlePopState)
  }, [])

  useEffect(() => {
    if (activeId !== 'security') return

    fetch('/api/v1/security/events?limit=50', { headers: { Authorization: `Bearer ${token}` } })
      .then(async (response) => {
        if (response.status === 401) {
          onLogout()
          return null
        }
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
      .catch((error: Error) => setSecurityError(error.message))
  }, [activeId, token, onLogout])

  useEffect(() => {
    if (activeId !== 'audit') return

    fetch('/api/v1/audit-events?limit=25', { headers: { Authorization: `Bearer ${token}` } })
      .then(async (response) => {
        if (response.status === 401) {
          onLogout()
          return null
        }
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
      .catch((error: Error) => setAuditError(error.message))
  }, [activeId, token, onLogout])

  return (
    <DashboardFrame
      navItems={navItems}
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
      {activeId === 'logs' ? (
        <LogsPanel logs={logs} streamState={streamState} />
      ) : activeId === 'security' ? (
        <SecurityPanel events={securityEvents} error={securityError} />
      ) : activeId === 'audit' ? (
        <AuditPanel events={auditEvents} error={auditError} />
      ) : activeId === 'dashboard' ? (
        <DashboardPanel metrics={liveTrafficMetrics} topics={topics} latestTopic={latestTopic} />
      ) : activeId === 'acls' ? (
        <ACLPanel token={token} />
      ) : activeId === 'users' ? (
        <MQTTUsersPanel token={token} />
      ) : activeId === 'deploy' ? (
        <DeployPanel token={token} />
      ) : (
        <TopicsPanel topics={topics} latestTopic={latestTopic} />
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

function TopicsPanel({ topics, latestTopic }: { topics: TopicMessage[]; latestTopic?: TopicMessage }) {
  return (
    <section className="mt-8 grid gap-4 lg:grid-cols-[0.85fr_1.15fr]">
      <article className="rounded-[1.75rem] border border-white/10 bg-[linear-gradient(135deg,rgba(34,211,238,0.16),rgba(15,23,42,0.08)_55%,rgba(249,115,22,0.18))] p-6">
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-100/80">Latest message</p>
        {latestTopic ? (
          <>
            <h3 className="mt-3 break-all text-2xl font-semibold text-white">{latestTopic.topic}</h3>
            {latestTopic.schema_validation ? <SchemaValidationDetails validation={latestTopic.schema_validation} /> : null}
            {latestTopic.sparkplug ? <SparkplugDetails metadata={latestTopic.sparkplug} sparkplugMetrics={latestTopic.sparkplug_metrics} /> : null}
            <PayloadMetadata event={latestTopic} />
            <pre className="mt-4 max-h-64 overflow-auto rounded-2xl border border-white/10 bg-slate-950/70 p-4 text-sm text-slate-100">{latestTopic.payload_preview}</pre>
          </>
        ) : (
          <p className="mt-3 text-sm leading-7 text-slate-200/90">Waiting for messages from the development broker.</p>
        )}
      </article>

      <article className="rounded-[1.75rem] border border-dashed border-cyan-300/25 bg-slate-900/70 p-6">
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Topic explorer</p>
        <div className="mt-4 space-y-3">
          {topics.length === 0 ? (
            <p className="text-sm text-slate-300">No topic activity received yet.</p>
          ) : (
            topics.map((topic, index) => (
              <div key={`${topic.topic}-${topic.observed_at}-${index}`} className="rounded-2xl border border-white/10 bg-white/[0.04] p-4">
                <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                  <div className="flex flex-wrap items-center gap-2">
                    <p className="break-all font-mono text-sm text-cyan-100">{topic.topic}</p>
                    {topic.schema_validation ? <SchemaValidationBadge validation={topic.schema_validation} /> : null}
                    {topic.sparkplug ? <SparkplugBadge metadata={topic.sparkplug} /> : null}
                  </div>
                  <span className="text-xs text-slate-400">{new Date(topic.observed_at).toLocaleTimeString()}</span>
                </div>
                {topic.sparkplug ? <SparkplugDetails metadata={topic.sparkplug} sparkplugMetrics={topic.sparkplug_metrics} compact /> : null}
                <p className="mt-2 line-clamp-2 break-all text-sm text-slate-300">{topic.payload_preview}</p>
                <PayloadSummary event={topic} />
              </div>
            ))
          )}
        </div>
      </article>
    </section>
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
  return (
    <div className="mt-3 flex flex-wrap gap-2">
      {payloadChips(event).map((chip) => (
        <span key={chip} className="rounded-full bg-slate-950/70 px-2.5 py-1 font-mono text-[0.7rem] text-slate-300">{chip}</span>
      ))}
    </div>
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

function LogsPanel({ logs, streamState }: { logs: BrokerLog[]; streamState: BrokerStreamState }) {
  return (
    <section className="mt-8 rounded-[1.75rem] border border-white/10 bg-slate-900/70 p-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Log feed</p>
          <p className="mt-2 text-sm text-slate-300">WebSocket state: <span className="capitalize text-white">{streamState}</span></p>
        </div>
        <span className={`rounded-full px-3 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${streamState === 'connected' ? 'bg-emerald-400/10 text-emerald-200' : streamState === 'reconnecting' ? 'bg-amber-400/10 text-amber-200' : 'bg-rose-400/10 text-rose-200'}`}>
          {streamState === 'connected' ? 'Live' : streamState}
        </span>
      </div>

      <div className="mt-5 space-y-3">
        {logs.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-white/10 bg-white/[0.03] p-5 text-sm text-slate-300">
            {streamState === 'connected' ? 'No broker log events received yet.' : 'Connect to the event stream to receive broker logs.'}
          </div>
        ) : (
          logs.map((log, index) => (
            <article key={`${log.observed_at}-${log.source}-${index}`} className="rounded-2xl border border-white/10 bg-white/[0.04] p-4">
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                <div className="flex flex-wrap items-center gap-2">
                  <SeverityBadge severity={log.severity} />
                  <span className="rounded-full bg-slate-950/70 px-2.5 py-1 font-mono text-xs text-cyan-100">{log.source}</span>
                </div>
                <time className="text-xs text-slate-400" dateTime={log.observed_at}>{new Date(log.observed_at).toLocaleString()}</time>
              </div>
              <p className="mt-3 whitespace-pre-wrap break-words text-sm leading-6 text-slate-100">{log.message}</p>
            </article>
          ))
        )}
      </div>
    </section>
  )
}

function ACLPanel({ token }: { token: string }) {
  const [rules, setRules] = useState<ACLRule[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [showForm, setShowForm] = useState(false)
  const [editRule, setEditRule] = useState<ACLRule | null>(null)
  const [formPrincipal, setFormPrincipal] = useState('')
  const [formTopicFilter, setFormTopicFilter] = useState('')
  const [formPermission, setFormPermission] = useState<ACLRule['permission']>('read')
  const [formDescription, setFormDescription] = useState('')
  const [formError, setFormError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
  const [refreshTick, setRefreshTick] = useState(0)

  const fetchRules = useCallback(() => { setRefreshTick((n) => n + 1) }, [])

  useEffect(() => {
    let cancelled = false
    fetch('/api/v1/acls', { headers: { Authorization: `Bearer ${token}` } })
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
        setError(err.message)
        setLoading(false)
      })
    return () => { cancelled = true }
  }, [token, refreshTick])

  const openCreate = () => {
    setEditRule(null)
    setFormPrincipal('')
    setFormTopicFilter('')
    setFormPermission('read')
    setFormDescription('')
    setFormError('')
    setShowForm(true)
  }

  const openEdit = (rule: ACLRule) => {
    setEditRule(rule)
    setFormPrincipal(rule.principal)
    setFormTopicFilter(rule.topic_filter)
    setFormPermission(rule.permission)
    setFormDescription(rule.description ?? '')
    setFormError('')
    setShowForm(true)
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting) return
    setSubmitting(true)
    setFormError('')
    try {
      const body = JSON.stringify({
        principal: formPrincipal.trim(),
        topic_filter: formTopicFilter.trim(),
        permission: formPermission,
        description: formDescription.trim(),
      })
      const url = editRule ? `/api/v1/acls/${editRule.id}` : '/api/v1/acls'
      const method = editRule ? 'PUT' : 'POST'
      const response = await fetch(url, {
        method,
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body,
      })
      if (!response.ok) {
        const errBody = (await response.json().catch(() => null)) as { error?: string; details?: string[] } | null
        const details = errBody?.details?.join('; ')
        setFormError(details ?? errBody?.error ?? 'Request failed.')
        return
      }
      setShowForm(false)
      fetchRules()
    } catch {
      setFormError('Could not reach the server.')
    } finally {
      setSubmitting(false)
    }
  }

  const handleDelete = async (id: string) => {
    try {
      const response = await fetch(`/api/v1/acls/${id}`, {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${token}` },
      })
      if (!response.ok && response.status !== 204) {
        const errBody = (await response.json().catch(() => null)) as { error?: string } | null
        setError(errBody?.error ?? 'Delete failed.')
        return
      }
      setDeleteConfirmId(null)
      fetchRules()
    } catch {
      setError('Could not reach the server.')
    }
  }

  return (
    <section className="mt-8 space-y-6">
      <div className="flex items-center justify-between gap-4">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">ACL rules</p>
          <p className="mt-1 text-sm text-slate-300">{rules.length} rule{rules.length !== 1 ? 's' : ''} configured</p>
        </div>
        <button
          type="button"
          onClick={openCreate}
          className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400"
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
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Principal</span>
              <input
                type="text"
                required
                value={formPrincipal}
                onChange={(e) => setFormPrincipal(e.target.value)}
                placeholder="username or $client_id"
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
              />
            </label>
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
              </select>
            </label>
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
                disabled={submitting}
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
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Principal</th>
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Topic filter</th>
                <th className="px-4 py-3 text-left text-xs uppercase tracking-[0.18em] text-slate-400">Permission</th>
                <th className="px-4 py-3 text-right text-xs uppercase tracking-[0.18em] text-slate-400">Actions</th>
              </tr>
            </thead>
            <tbody>
              {rules.map((rule) => (
                <tr key={rule.id} className="border-b border-white/5 last:border-0">
                  <td className="px-4 py-3 font-mono text-sm text-slate-200">{rule.principal}</td>
                  <td className="px-4 py-3 font-mono text-sm text-cyan-100">{rule.topic_filter}</td>
                  <td className="px-4 py-3">
                    <span className="rounded-full bg-cyan-400/10 px-2.5 py-1 font-mono text-xs text-cyan-200">{rule.permission}</span>
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
                          className="rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white"
                        >
                          Edit
                        </button>
                        <button
                          type="button"
                          onClick={() => setDeleteConfirmId(rule.id)}
                          className="rounded-xl bg-red-500/20 px-3 py-1.5 text-xs text-red-300 transition hover:bg-red-500/30"
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
    </section>
  )
}

function DeployPanel({ token }: { token: string }) {
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
    fetch('/api/v1/deployments', { headers: { Authorization: `Bearer ${token}` } })
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
        setHistoryError(err.message)
        setHistoryLoading(false)
      })
    return () => { cancelled = true }
  }, [token, historyTick])

  const handlePreview = async () => {
    setPreviewLoading(true)
    setPreviewError('')
    setPreview(null)
    try {
      const response = await fetch('/api/v1/deployments/preview', {
        method: 'POST',
        headers: { Authorization: `Bearer ${token}` },
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
    } catch {
      setPreviewError('Could not reach the server.')
    } finally {
      setPreviewLoading(false)
    }
  }

  const handleApply = async () => {
    setApplying(true)
    setApplyError('')
    setApplyResult(null)
    setConfirmApply(false)
    try {
      const response = await fetch('/api/v1/deployments/apply', {
        method: 'POST',
        headers: { Authorization: `Bearer ${token}` },
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
      fetchHistory()
    } catch {
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
                    disabled={applying}
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
                  className="rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400"
                >
                  Apply
                </button>
              )
            ) : null}
            <button
              type="button"
              onClick={() => { void handlePreview() }}
              disabled={previewLoading}
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
          <div className={`mt-5 rounded-2xl border p-5 ${applyResult.status === 'applied' ? 'border-emerald-300/30 bg-emerald-400/10' : 'border-amber-300/30 bg-amber-400/10'}`}>
            <p className={`text-xs font-semibold uppercase tracking-[0.18em] ${applyResult.status === 'applied' ? 'text-emerald-200' : 'text-amber-200'}`}>
              {applyResult.status}
            </p>
            {applyResult.message ? <p className="mt-2 text-sm text-slate-300">{applyResult.message}</p> : null}
          </div>
        ) : null}

        {preview ? (
          <div className="mt-5 space-y-4">
            {!preview.has_changes ? (
              <div className="rounded-2xl border border-dashed border-white/10 bg-white/[0.03] p-5 text-sm text-slate-300">No pending changes — configuration is up to date.</div>
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
                  <td className="py-3 pr-4 font-mono text-xs text-slate-400">{dep.id.slice(0, 8)}</td>
                  <td className="px-4 py-3">
                    <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${dep.status === 'applied' ? 'bg-emerald-400/10 text-emerald-200' : 'bg-amber-400/10 text-amber-200'}`}>
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

function SeverityBadge({ severity }: { severity: BrokerLog['severity'] }) {
  const className = {
    debug: 'bg-slate-400/10 text-slate-200',
    info: 'bg-cyan-400/10 text-cyan-200',
    warning: 'bg-amber-400/10 text-amber-200',
    error: 'bg-rose-400/10 text-rose-200',
  }[severity]

  return <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${className}`}>{severity}</span>
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
