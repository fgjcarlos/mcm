import { useEffect, useMemo, useState } from 'react'

type BrokerEvent = {
  type: 'broker_status' | 'topic_message' | 'broker_log'
  status?: 'connected' | 'disconnected'
  topic?: string
  payload_preview?: string
  payload_format?: 'json' | 'text'
  truncated?: boolean
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

function App() {
  const [activeId, setActiveId] = useState<string>(navItems[0].id)
  const [brokerStatus, setBrokerStatus] = useState<'connected' | 'disconnected'>('disconnected')
  const [streamState, setStreamState] = useState<'connecting' | 'connected' | 'disconnected'>('connecting')
  const [topics, setTopics] = useState<TopicMessage[]>([])
  const [logs, setLogs] = useState<BrokerLog[]>([])
  const [auditEvents, setAuditEvents] = useState<AuditEvent[]>([])
  const [auditError, setAuditError] = useState<string>('')
  const activeItem = navItems.find((item) => item.id === activeId) ?? navItems[0]

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
          setTopics((current) => [event as TopicMessage, ...current].slice(0, 20))
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
    const token = window.localStorage.getItem('mcm_token')
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
  }, [])

  const uniqueTopicCount = useMemo(() => new Set(topics.map((topic) => topic.topic)).size, [topics])
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

            {activeId === 'audit' ? (
              <AuditPanel events={auditEvents} error={auditError} />
            ) : activeId === 'logs' ? (
              <LogsPanel logs={logs} streamState={streamState} />
            ) : (
              <TopicsPanel topics={topics} latestTopic={latestTopic} />
            )}
          </div>
        </main>
      </div>
    </div>
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

function TopicsPanel({ topics, latestTopic }: { topics: TopicMessage[]; latestTopic?: TopicMessage }) {
  return (
    <section className="mt-8 grid gap-4 lg:grid-cols-[0.85fr_1.15fr]">
      <article className="rounded-[1.75rem] border border-white/10 bg-[linear-gradient(135deg,rgba(34,211,238,0.16),rgba(15,23,42,0.08)_55%,rgba(249,115,22,0.18))] p-6">
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-100/80">Latest message</p>
        {latestTopic ? (
          <>
            <h3 className="mt-3 break-all text-2xl font-semibold text-white">{latestTopic.topic}</h3>
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
                  <p className="break-all font-mono text-sm text-cyan-100">{topic.topic}</p>
                  <span className="text-xs text-slate-400">{new Date(topic.observed_at).toLocaleTimeString()}</span>
                </div>
                <p className="mt-2 line-clamp-2 break-all text-sm text-slate-300">{topic.payload_preview}</p>
              </div>
            ))
          )}
        </div>
      </article>
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

export default App
