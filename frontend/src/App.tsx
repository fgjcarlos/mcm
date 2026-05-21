import { useEffect, useMemo, useState } from 'react'

type SparkplugMetadata = {
  namespace: string
  group_id: string
  message_type: string
  edge_node_id: string
  device_id?: string
}

type BrokerEvent = {
  type: 'broker_status' | 'topic_message' | 'broker_log'
  status?: 'connected' | 'disconnected'
  topic?: string
  payload_preview?: string
  payload_format?: 'json' | 'text'
  truncated?: boolean
  sparkplug?: SparkplugMetadata
  source?: string
  severity?: 'debug' | 'info' | 'warning' | 'error'
  message?: string
  observed_at: string
}

type TopicMessage = BrokerEvent & { type: 'topic_message'; topic: string }
type BrokerLog = BrokerEvent & { type: 'broker_log'; source: string; severity: 'debug' | 'info' | 'warning' | 'error'; message: string }

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

type BrokerTrafficItem = {
  name: string
  count: number
  percentage: number
}

type BrokerRatePoint = {
  timestamp: string
  count: number
}

type BrokerTrafficMetrics = {
  window_seconds: number
  message_count: number
  message_rate_per_minute: number
  rate_points: BrokerRatePoint[]
  top_topics: BrokerTrafficItem[]
  top_clients: BrokerTrafficItem[]
  top_clients_available: boolean
  top_clients_note: string
  persistence: string
}

type StatusResponse = {
  broker: {
    status: 'connected' | 'disconnected'
    metrics: {
      traffic: BrokerTrafficMetrics
    }
  }
}

type NavItem = {
  id: string
  label: string
  eyebrow: string
  title: string
  description: string
}

const navItems: NavItem[] = [
  {
    id: 'dashboard',
    label: 'Dashboard',
    eyebrow: 'Broker overview',
    title: 'Live broker snapshot',
    description: 'Connection state and the latest topic traffic from the configured Mosquitto broker.',
  },
  {
    id: 'topics',
    label: 'Topics',
    eyebrow: 'Traffic',
    title: 'Topic explorer',
    description: 'Inspect incoming topic names and safe payload previews as messages arrive.',
  },
  {
    id: 'logs',
    label: 'Logs',
    eyebrow: 'Operations',
    title: 'Realtime broker logs',
    description: 'Monitor connection transitions and MCM broker operational events as they are ingested.',
  },
  {
    id: 'users',
    label: 'Users',
    eyebrow: 'Identity',
    title: 'User directory placeholder',
    description: 'Account provisioning, credential resets, and status views for broker users land here next.',
  },
  {
    id: 'acls',
    label: 'ACLs',
    eyebrow: 'Authorization',
    title: 'ACL policy workspace',
    description: 'Topic permissions, policy reviews, and audit-safe change workflows.',
  },
  {
    id: 'security',
    label: 'Security',
    eyebrow: 'Audit',
    title: 'Recent security events',
    description: 'Review failed admin logins, disabled-user login attempts, protected API failures, and ACL API audit hooks.',
  },
  {
    id: 'audit',
    label: 'Audit',
    eyebrow: 'Security',
    title: 'Administrative audit log',
    description: 'Review recent administrative changes, actors, affected resources, and outcomes.',
  },
  {
    id: 'settings',
    label: 'Settings',
    eyebrow: 'System',
    title: 'Platform settings placeholder',
    description: 'Global defaults, integration configuration, and broker-level safety controls.',
  },
]

const trafficWindowSeconds = 300
const defaultClientNote = 'Client identity is not included in MQTT application messages observed via wildcard subscriptions. Enable broker-side client metrics or log ingestion to populate this widget in a future release.'

const emptyTrafficMetrics: BrokerTrafficMetrics = {
  window_seconds: trafficWindowSeconds,
  message_count: 0,
  message_rate_per_minute: 0,
  rate_points: [],
  top_topics: [],
  top_clients: [],
  top_clients_available: false,
  top_clients_note: defaultClientNote,
  persistence: 'Waiting for broker metrics.',
}

function App() {
  const [activeId, setActiveId] = useState<string>(navItems[0].id)
  const [brokerStatus, setBrokerStatus] = useState<'connected' | 'disconnected'>('disconnected')
  const [streamState, setStreamState] = useState<'connecting' | 'connected' | 'disconnected'>('connecting')
  const [topics, setTopics] = useState<TopicMessage[]>([])
  const [trafficEvents, setTrafficEvents] = useState<TopicMessage[]>([])
  const [trafficMetrics, setTrafficMetrics] = useState<BrokerTrafficMetrics>(emptyTrafficMetrics)
  const [logs, setLogs] = useState<BrokerLog[]>([])
  const [securityEvents, setSecurityEvents] = useState<SecurityEvent[]>([])
  const [securityError, setSecurityError] = useState<string>('')
  const [auditEvents, setAuditEvents] = useState<AuditEvent[]>([])
  const [auditError, setAuditError] = useState<string>('')
  const activeItem = navItems.find((item) => item.id === activeId) ?? navItems[0]

  useEffect(() => {
    let cancelled = false
    fetch('/api/v1/status')
      .then((response) => (response.ok ? response.json() : Promise.reject(new Error('status request failed'))))
      .then((status: StatusResponse) => {
        if (cancelled) return
        setBrokerStatus(status.broker.status)
        setTrafficMetrics(status.broker.metrics.traffic ?? emptyTrafficMetrics)
      })
      .catch(() => {
        if (!cancelled) setTrafficMetrics(emptyTrafficMetrics)
      })

    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const socket = new WebSocket(`${protocol}//${window.location.host}/api/v1/broker/events`)

    socket.addEventListener('open', () => setStreamState('connected'))
    socket.addEventListener('close', () => setStreamState('disconnected'))
    socket.addEventListener('error', () => setStreamState('disconnected'))
    socket.addEventListener('message', (message) => {
      try {
        const event = JSON.parse(message.data) as BrokerEvent
        if (event.type === 'broker_status' && event.status) {
          setBrokerStatus(event.status)
        }
        if (event.type === 'topic_message' && event.topic) {
          const topicEvent = event as TopicMessage
          setTopics((current) => [topicEvent, ...current].slice(0, 20))
          setTrafficEvents((current) => pruneTrafficEvents([topicEvent, ...current]))
        }
        if (event.type === 'broker_log' && event.source && event.severity && event.message) {
          setLogs((current) => [event as BrokerLog, ...current].slice(0, 100))
        }
      } catch {
        setStreamState('disconnected')
      }
    })

    return () => socket.close()
  }, [])

  useEffect(() => {
    if (activeId !== 'security') return

    const token = window.localStorage.getItem('mcm_admin_token')
    const headers: HeadersInit = token ? { Authorization: `Bearer ${token}` } : {}
    fetch('/api/v1/security/events?limit=50', { headers })
      .then(async (response) => {
        if (!response.ok) {
          throw new Error(response.status === 401 ? 'Sign in with an admin token to view security events.' : 'Unable to load security events.')
        }
        return response.json() as Promise<{ events?: SecurityEvent[] }>
      })
      .then((body) => {
        setSecurityEvents(body.events ?? [])
        setSecurityError('')
      })
      .catch((error: Error) => setSecurityError(error.message))
  }, [activeId])

  useEffect(() => {
    if (activeId !== 'audit') return

    const token = window.localStorage.getItem('mcm_token') ?? window.localStorage.getItem('mcm_admin_token')
    const headers: HeadersInit = token ? { Authorization: `Bearer ${token}` } : {}

    fetch('/api/v1/audit-events?limit=25', { headers })
      .then(async (response) => {
        if (!response.ok) {
          throw new Error(response.status === 401 ? 'Sign in with an admin token to view audit events.' : 'Failed to load audit events.')
        }
        return response.json() as Promise<{ events?: AuditEvent[] }>
      })
      .then((body) => {
        setAuditEvents(body.events ?? [])
        setAuditError('')
      })
      .catch((error: Error) => setAuditError(error.message))
  }, [activeId])

  const uniqueTopicCount = useMemo(() => new Set(topics.map((topic) => topic.topic)).size, [topics])
  const liveTrafficMetrics = useMemo(
    () => (trafficEvents.length > 0 ? buildTrafficMetrics(trafficEvents, trafficMetrics) : trafficMetrics),
    [trafficEvents, trafficMetrics],
  )
  const latestTopic = topics[0]

  return (
    <div className="min-h-screen bg-[radial-gradient(circle_at_top,#1d4e89_0%,#0f172a_38%,#020617_100%)] text-slate-100">
      <div className="mx-auto flex min-h-screen w-full max-w-7xl flex-col px-4 py-4 sm:px-6 lg:flex-row lg:px-8 lg:py-8">
        <aside className="mb-4 w-full rounded-[2rem] border border-white/10 bg-slate-950/65 p-5 shadow-2xl shadow-slate-950/40 backdrop-blur lg:mb-0 lg:w-80 lg:p-6">
          <div className="flex items-center justify-between gap-4 border-b border-white/10 pb-5">
            <div>
              <p className="text-xs font-semibold uppercase tracking-[0.3em] text-cyan-300">MCM</p>
              <h1 className="mt-2 text-2xl font-semibold tracking-tight text-white">Control Manager</h1>
            </div>
            <div className="rounded-full border border-cyan-400/30 bg-cyan-400/10 px-3 py-1 text-xs font-medium text-cyan-100">Alpha</div>
          </div>

          <div className="mt-6 rounded-2xl border border-white/10 bg-white/[0.04] p-4">
            <div className="flex items-center justify-between">
              <span className="text-xs uppercase tracking-[0.22em] text-slate-400">Broker</span>
              <span className={`h-3 w-3 rounded-full ${brokerStatus === 'connected' ? 'bg-emerald-400 shadow-[0_0_20px_rgba(52,211,153,0.9)]' : 'bg-rose-400'}`} />
            </div>
            <p className="mt-2 text-lg font-semibold capitalize text-white">{brokerStatus}</p>
            <p className="mt-1 text-xs text-slate-400">Event stream: {streamState}</p>
          </div>

          <nav className="mt-8 space-y-2" aria-label="Primary navigation">
            {navItems.map((item, index) => {
              const isActive = item.id === activeItem.id
              return (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => setActiveId(item.id)}
                  className={`group flex w-full items-center justify-between rounded-2xl border px-4 py-3 text-left transition ${
                    isActive
                      ? 'border-cyan-300/40 bg-cyan-300/12 text-white shadow-lg shadow-cyan-950/30'
                      : 'border-white/5 bg-white/[0.03] text-slate-300 hover:border-white/15 hover:bg-white/[0.06] hover:text-white'
                  }`}
                >
                  <span>
                    <span className="block text-sm font-semibold">{item.label}</span>
                    <span className="block text-xs uppercase tracking-[0.22em] text-slate-400 group-hover:text-slate-300">{item.eyebrow}</span>
                  </span>
                  <span className="font-mono text-xs text-slate-400">{String(index + 1).padStart(2, '0')}</span>
                </button>
              )
            })}
          </nav>
        </aside>

        <main className="flex-1 lg:pl-6">
          <div className="rounded-[2rem] border border-white/10 bg-slate-950/55 p-6 shadow-2xl shadow-slate-950/40 backdrop-blur sm:p-8">
            <div className="flex flex-col gap-6 border-b border-white/10 pb-8 xl:flex-row xl:items-end xl:justify-between">
              <div className="max-w-2xl">
                <p className="text-sm font-semibold uppercase tracking-[0.35em] text-cyan-300">{activeItem.eyebrow}</p>
                <h2 className="mt-3 text-4xl font-semibold tracking-tight text-white sm:text-5xl">{activeItem.title}</h2>
                <p className="mt-4 max-w-xl text-base leading-7 text-slate-300 sm:text-lg">{activeItem.description}</p>
              </div>

              <div className="grid grid-cols-3 gap-3 sm:min-w-[24rem]">
                <Metric label="Status" value={brokerStatus} />
                <Metric label="Topics" value={String(uniqueTopicCount).padStart(2, '0')} />
                <Metric label="Logs" value={String(logs.length).padStart(2, '0')} />
              </div>
            </div>

            {activeId === 'logs' ? (
              <LogsPanel logs={logs} streamState={streamState} />
            ) : activeId === 'security' ? (
              <SecurityPanel events={securityEvents} error={securityError} />
            ) : activeId === 'audit' ? (
              <AuditPanel events={auditEvents} error={auditError} />
            ) : activeId === 'dashboard' ? (
              <DashboardPanel metrics={liveTrafficMetrics} topics={topics} latestTopic={latestTopic} />
            ) : (
              <TopicsPanel topics={topics} latestTopic={latestTopic} />
            )}
          </div>
        </main>
      </div>
    </div>
  )
}

function DashboardPanel({ metrics, topics, latestTopic }: { metrics: BrokerTrafficMetrics; topics: TopicMessage[]; latestTopic?: TopicMessage }) {
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
        </article>

        <HotspotCard title="Top topics" empty="No topic activity in the recent window." items={metrics.top_topics} />

        <article className="rounded-[1.75rem] border border-white/10 bg-slate-900/70 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Top clients</p>
          {metrics.top_clients_available && metrics.top_clients.length > 0 ? (
            <TrafficBars items={metrics.top_clients} />
          ) : (
            <div className="mt-4 rounded-2xl border border-dashed border-amber-300/25 bg-amber-400/10 p-4 text-sm leading-6 text-amber-50">
              <p className="font-semibold">Not available from subscribed messages</p>
              <p className="mt-2 text-amber-50/80">{metrics.top_clients_note || defaultClientNote}</p>
            </div>
          )}
        </article>
      </div>

      <div className="grid gap-4 lg:grid-cols-[0.85fr_1.15fr]">
        <article className="rounded-[1.75rem] border border-white/10 bg-white/[0.04] p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Metric source</p>
          <p className="mt-3 text-sm leading-6 text-slate-300">{metrics.persistence || 'Recent traffic is held in memory for this browser session.'}</p>
        </article>
        <article className="rounded-[1.75rem] border border-white/10 bg-white/[0.04] p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Latest activity</p>
          <p className="mt-3 text-sm text-slate-300">{latestTopic ? latestTopic.topic : topics.length === 0 ? 'Waiting for topic traffic.' : `${topics.length} recent messages loaded.`}</p>
          {latestTopic?.sparkplug ? <SparkplugDetails metadata={latestTopic.sparkplug} compact /> : null}
        </article>
      </div>
    </section>
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
        visiblePoints.map((point) => (
          <div key={point.timestamp} className="flex flex-1 flex-col items-center justify-end gap-1">
            <div className="w-full rounded-t bg-cyan-300/80" style={{ height: `${Math.max(8, (point.count / maxCount) * 100)}%` }} title={`${point.count} messages`} />
            <span className="text-[0.6rem] text-slate-400">{new Date(point.timestamp).toLocaleTimeString([], { minute: '2-digit', second: '2-digit' })}</span>
          </div>
        ))
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
            {latestTopic.sparkplug ? <SparkplugDetails metadata={latestTopic.sparkplug} /> : null}
            <pre className="mt-4 max-h-64 overflow-auto rounded-2xl border border-white/10 bg-slate-950/70 p-4 text-sm text-slate-100">{latestTopic.payload_preview}</pre>
            <p className="mt-3 text-xs uppercase tracking-[0.22em] text-slate-300">
              {latestTopic.payload_format}{latestTopic.truncated ? ' · truncated' : ''}
            </p>
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
                    {topic.sparkplug ? <SparkplugBadge metadata={topic.sparkplug} /> : null}
                  </div>
                  <span className="text-xs text-slate-400">{new Date(topic.observed_at).toLocaleTimeString()}</span>
                </div>
                {topic.sparkplug ? <SparkplugDetails metadata={topic.sparkplug} compact /> : null}
                <p className="mt-2 line-clamp-2 break-all text-sm text-slate-300">{topic.payload_preview}</p>
              </div>
            ))
          )}
        </div>
      </article>
    </section>
  )
}

function SparkplugBadge({ metadata }: { metadata: SparkplugMetadata }) {
  return (
    <span className="rounded-full bg-orange-400/10 px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] text-orange-200">
      Sparkplug {metadata.message_type}
    </span>
  )
}

function SparkplugDetails({ metadata, compact = false }: { metadata: SparkplugMetadata; compact?: boolean }) {
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
    </div>
  )
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

function LogsPanel({ logs, streamState }: { logs: BrokerLog[]; streamState: 'connecting' | 'connected' | 'disconnected' }) {
  return (
    <section className="mt-8 rounded-[1.75rem] border border-white/10 bg-slate-900/70 p-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Log feed</p>
          <p className="mt-2 text-sm text-slate-300">WebSocket state: <span className="capitalize text-white">{streamState}</span></p>
        </div>
        <span className={`rounded-full px-3 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${streamState === 'connected' ? 'bg-emerald-400/10 text-emerald-200' : 'bg-rose-400/10 text-rose-200'}`}>
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

function SeverityBadge({ severity }: { severity: BrokerLog['severity'] }) {
  const className = {
    debug: 'bg-slate-400/10 text-slate-200',
    info: 'bg-cyan-400/10 text-cyan-200',
    warning: 'bg-amber-400/10 text-amber-200',
    error: 'bg-rose-400/10 text-rose-200',
  }[severity]

  return <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${className}`}>{severity}</span>
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-2xl border border-white/10 bg-white/[0.04] px-4 py-4">
      <div className="font-mono text-2xl font-semibold capitalize text-white">{value}</div>
      <div className="mt-2 text-xs uppercase tracking-[0.22em] text-slate-400">{label}</div>
    </div>
  )
}

function pruneTrafficEvents(events: TopicMessage[]) {
  const cutoff = Date.now() - trafficWindowSeconds * 1000
  return events.filter((event) => new Date(event.observed_at).getTime() >= cutoff).slice(0, 5000)
}

function buildTrafficMetrics(events: TopicMessage[], base: BrokerTrafficMetrics): BrokerTrafficMetrics {
  const pruned = pruneTrafficEvents(events)
  const topicCounts = new Map<string, number>()
  const bucketCounts = new Map<number, number>()
  let messageCount = base.message_count

  base.top_topics.forEach((item) => topicCounts.set(item.name, item.count))
  base.rate_points.forEach((point) => bucketCounts.set(new Date(point.timestamp).getTime(), point.count))

  pruned.forEach((event) => {
    messageCount += 1
    topicCounts.set(event.topic, (topicCounts.get(event.topic) ?? 0) + 1)
    const observedAt = new Date(event.observed_at)
    observedAt.setSeconds(0, 0)
    bucketCounts.set(observedAt.getTime(), (bucketCounts.get(observedAt.getTime()) ?? 0) + 1)
  })

  const now = new Date()
  now.setSeconds(0, 0)
  const ratePoints: BrokerRatePoint[] = []
  for (let offset = Math.floor(trafficWindowSeconds / 60); offset >= 0; offset -= 1) {
    const timestamp = new Date(now.getTime() - offset * 60_000)
    ratePoints.push({ timestamp: timestamp.toISOString(), count: bucketCounts.get(timestamp.getTime()) ?? 0 })
  }

  const topTopics = [...topicCounts.entries()]
    .map(([name, count]) => ({ name, count, percentage: messageCount === 0 ? 0 : (count * 100) / messageCount }))
    .sort((left, right) => right.count - left.count || left.name.localeCompare(right.name))
    .slice(0, 5)

  return {
    ...base,
    window_seconds: trafficWindowSeconds,
    message_count: messageCount,
    message_rate_per_minute: messageCount / (trafficWindowSeconds / 60),
    rate_points: ratePoints,
    top_topics: topTopics,
    top_clients_available: false,
    top_clients_note: base.top_clients_note || defaultClientNote,
  }
}

export default App