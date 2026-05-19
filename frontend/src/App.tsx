import { useEffect, useMemo, useState } from 'react'

type BrokerStatus = {
  state: string
  connected: boolean
  brokerUrl: string
  lastChangedAt: string
  lastError?: string
  clientsConnected: number
  clientsTotal: number
  messagesSeen: number
  observedTopics: number
}

type TopicActivity = {
  topic: string
  payload: string
  payloadBytes: number
  truncated: boolean
  isJson: boolean
  seenAt: string
}

type Snapshot = {
  broker: BrokerStatus
  topics: TopicActivity[]
}

type ServerEvent = {
  type: 'snapshot' | 'broker' | 'topic'
  snapshot?: Snapshot
  broker?: BrokerStatus
  topic?: TopicActivity
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
    title: 'Live broker status',
    description:
      'Track broker connectivity, connected clients, and the latest topic activity coming from the development Mosquitto broker.',
  },
  {
    id: 'topics',
    label: 'Topics',
    eyebrow: 'Traffic explorer',
    title: 'Realtime topic explorer',
    description:
      'Inspect recently active topics, payload previews, JSON message bodies, and safe truncation metadata for oversized payloads.',
  },
  {
    id: 'users',
    label: 'Users',
    eyebrow: 'Identity',
    title: 'User management placeholder',
    description: 'Reserved for a future user and credential workflow.',
  },
  {
    id: 'acls',
    label: 'ACLs',
    eyebrow: 'Authorization',
    title: 'ACL workspace placeholder',
    description: 'Reserved for future policy editing and audit-safe ACL changes.',
  },
]

const mockSnapshot: Snapshot = {
  broker: {
    state: 'demo',
    connected: false,
    brokerUrl: 'tcp://localhost:1883',
    lastChangedAt: new Date().toISOString(),
    lastError: 'Waiting for backend websocket on :8080',
    clientsConnected: 0,
    clientsTotal: 0,
    messagesSeen: 2,
    observedTopics: 2,
  },
  topics: [
    {
      topic: 'mcm/demo/status',
      payload: '{\n  "state": "waiting",\n  "source": "mock"\n}',
      payloadBytes: 35,
      truncated: false,
      isJson: true,
      seenAt: new Date().toISOString(),
    },
    {
      topic: 'mcm/demo/text',
      payload: 'Start `mcm server` to replace this mock stream with live broker data.',
      payloadBytes: 66,
      truncated: false,
      isJson: false,
      seenAt: new Date(Date.now() - 30_000).toISOString(),
    },
  ],
}

function App() {
  const [activeId, setActiveId] = useState<string>('dashboard')
  const [snapshot, setSnapshot] = useState<Snapshot>(mockSnapshot)
  const [transport, setTransport] = useState<'connecting' | 'live' | 'mock'>('connecting')

  const activeItem = navItems.find((item) => item.id === activeId) ?? navItems[0]
  const statusTone = snapshot.broker.connected
    ? 'border-emerald-300/35 bg-emerald-400/12 text-emerald-100'
    : 'border-amber-300/35 bg-amber-400/12 text-amber-100'

  useEffect(() => {
    let closed = false
    let socket: WebSocket | null = null
    let retryTimer: number | undefined

    const connect = () => {
      setTransport((current) => (current === 'live' ? current : 'connecting'))

      socket = new WebSocket(resolveWebSocketURL())

      socket.addEventListener('open', () => {
        if (closed) {
          return
        }
        setTransport('live')
      })

      socket.addEventListener('message', (event) => {
        const payload = JSON.parse(event.data) as ServerEvent

        setSnapshot((current) => applyServerEvent(current, payload))
      })

      socket.addEventListener('error', () => {
        if (closed) {
          return
        }
        setTransport('mock')
      })

      socket.addEventListener('close', () => {
        if (closed) {
          return
        }
        setTransport('mock')
        retryTimer = window.setTimeout(connect, 3000)
      })
    }

    connect()

    return () => {
      closed = true
      if (retryTimer !== undefined) {
        window.clearTimeout(retryTimer)
      }
      socket?.close()
    }
  }, [])

  const metrics = useMemo(
    () => [
      {
        label: 'Broker',
        value: snapshot.broker.connected ? 'Online' : titleize(snapshot.broker.state),
      },
      { label: 'Clients', value: String(snapshot.broker.clientsConnected) },
      { label: 'Topics', value: String(snapshot.broker.observedTopics) },
      { label: 'Messages', value: String(snapshot.broker.messagesSeen) },
    ],
    [snapshot],
  )

  return (
    <div className="min-h-screen bg-[radial-gradient(circle_at_top,#164e63_0%,#0f172a_34%,#020617_100%)] text-slate-100">
      <div className="mx-auto flex min-h-screen w-full max-w-7xl flex-col px-4 py-4 sm:px-6 lg:flex-row lg:px-8 lg:py-8">
        <aside className="mb-4 w-full rounded-[2rem] border border-white/10 bg-slate-950/65 p-5 shadow-2xl shadow-slate-950/40 backdrop-blur lg:mb-0 lg:w-80 lg:p-6">
          <div className="flex items-center justify-between gap-4 border-b border-white/10 pb-5">
            <div>
              <p className="text-xs font-semibold uppercase tracking-[0.3em] text-cyan-300">
                MCM
              </p>
              <h1 className="mt-2 text-2xl font-semibold tracking-tight text-white">
                Control Manager
              </h1>
            </div>
            <div className={`rounded-full border px-3 py-1 text-xs font-medium ${statusTone}`}>
              {transport === 'live' ? 'Live' : transport === 'connecting' ? 'Connecting' : 'Mock'}
            </div>
          </div>

          <div className="mt-6 rounded-[1.5rem] border border-white/10 bg-white/[0.04] p-4">
            <p className="text-xs font-semibold uppercase tracking-[0.22em] text-slate-400">
              Broker endpoint
            </p>
            <p className="mt-2 break-all font-mono text-sm text-slate-100">
              {snapshot.broker.brokerUrl}
            </p>
            <p className="mt-3 text-sm leading-6 text-slate-300">
              The dashboard listens for a websocket snapshot first, then broker and topic events. If
              the backend is unavailable, the shell stays usable with mock data until it reconnects.
            </p>
          </div>

          <nav className="mt-8 space-y-2" aria-label="Primary navigation">
            {navItems.map((item) => {
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
                    <span className="block text-xs uppercase tracking-[0.22em] text-slate-400 group-hover:text-slate-300">
                      {item.eyebrow}
                    </span>
                  </span>
                  <span className="font-mono text-xs text-slate-400">
                    {String(navItems.indexOf(item) + 1).padStart(2, '0')}
                  </span>
                </button>
              )
            })}
          </nav>
        </aside>

        <main className="flex-1 lg:pl-6">
          <div className="rounded-[2rem] border border-white/10 bg-slate-950/55 p-6 shadow-2xl shadow-slate-950/40 backdrop-blur sm:p-8">
            <div className="flex flex-col gap-6 border-b border-white/10 pb-8 xl:flex-row xl:items-end xl:justify-between">
              <div className="max-w-2xl">
                <p className="text-sm font-semibold uppercase tracking-[0.35em] text-cyan-300">
                  {activeItem.eyebrow}
                </p>
                <h2 className="mt-3 text-4xl font-semibold tracking-tight text-white sm:text-5xl">
                  {activeItem.title}
                </h2>
                <p className="mt-4 max-w-xl text-base leading-7 text-slate-300 sm:text-lg">
                  {activeItem.description}
                </p>
              </div>

              <div className="grid grid-cols-2 gap-3 sm:min-w-[28rem] lg:grid-cols-4">
                {metrics.map((metric) => (
                  <div
                    key={metric.label}
                    className="rounded-2xl border border-white/10 bg-white/[0.04] px-4 py-4"
                  >
                    <div className="font-mono text-2xl font-semibold text-white">
                      {metric.value}
                    </div>
                    <div className="mt-2 text-xs uppercase tracking-[0.22em] text-slate-400">
                      {metric.label}
                    </div>
                  </div>
                ))}
              </div>
            </div>

            {activeId === 'dashboard' ? (
              <section className="mt-8 grid gap-4 lg:grid-cols-[0.85fr_1.15fr]">
                <article className="rounded-[1.75rem] border border-white/10 bg-[linear-gradient(135deg,rgba(34,211,238,0.16),rgba(15,23,42,0.18)_55%,rgba(16,185,129,0.12))] p-6">
                  <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-100/80">
                    Broker heartbeat
                  </p>
                  <div className="mt-4 flex items-center gap-3">
                    <span
                      className={`h-3 w-3 rounded-full ${snapshot.broker.connected ? 'bg-emerald-300 shadow-[0_0_18px_rgba(110,231,183,0.8)]' : 'bg-amber-300 shadow-[0_0_18px_rgba(252,211,77,0.7)]'}`}
                    />
                    <span className="text-2xl font-semibold text-white">
                      {snapshot.broker.connected ? 'Connected' : titleize(snapshot.broker.state)}
                    </span>
                  </div>
                  <dl className="mt-6 grid gap-4 sm:grid-cols-2">
                    <Metric label="Connected clients" value={String(snapshot.broker.clientsConnected)} />
                    <Metric label="Total clients seen" value={String(snapshot.broker.clientsTotal)} />
                    <Metric label="Messages observed" value={String(snapshot.broker.messagesSeen)} />
                    <Metric label="Tracked topics" value={String(snapshot.broker.observedTopics)} />
                  </dl>
                  <p className="mt-6 text-sm leading-6 text-slate-200/90">
                    Last broker change: {formatTimestamp(snapshot.broker.lastChangedAt)}
                  </p>
                  {snapshot.broker.lastError ? (
                    <p className="mt-3 rounded-2xl border border-amber-200/15 bg-amber-400/10 px-4 py-3 text-sm text-amber-100">
                      {snapshot.broker.lastError}
                    </p>
                  ) : null}
                </article>

                <article className="rounded-[1.75rem] border border-white/10 bg-slate-900/70 p-6">
                  <div className="flex items-center justify-between gap-4">
                    <div>
                      <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">
                        Recent topic activity
                      </p>
                      <h3 className="mt-3 text-2xl font-semibold text-white">Latest payload previews</h3>
                    </div>
                    <button
                      type="button"
                      onClick={() => setActiveId('topics')}
                      className="rounded-full border border-cyan-300/30 bg-cyan-300/12 px-4 py-2 text-sm font-medium text-cyan-50 transition hover:border-cyan-200/50 hover:bg-cyan-300/18"
                    >
                      Open explorer
                    </button>
                  </div>

                  <div className="mt-5 space-y-3">
                    {snapshot.topics.slice(0, 5).map((topic) => (
                      <TopicCard key={topic.topic} topic={topic} compact />
                    ))}
                    {snapshot.topics.length === 0 ? (
                      <EmptyState message="Waiting for topic traffic from the development broker." />
                    ) : null}
                  </div>
                </article>
              </section>
            ) : activeId === 'topics' ? (
              <section className="mt-8">
                <div className="grid gap-4 lg:grid-cols-[0.7fr_1.3fr]">
                  <article className="rounded-[1.75rem] border border-white/10 bg-white/[0.03] p-6">
                    <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">
                      Explorer status
                    </p>
                    <ul className="mt-4 space-y-4 text-sm text-slate-300">
                      <li>Source: {transport === 'live' ? 'WebSocket stream' : 'Mock fallback'}</li>
                      <li>Topics tracked: {snapshot.broker.observedTopics}</li>
                      <li>Safe payload limit: 2048 bytes</li>
                      <li>JSON messages are formatted before display when parsing succeeds.</li>
                    </ul>
                  </article>

                  <article className="rounded-[1.75rem] border border-white/10 bg-slate-900/70 p-6">
                    <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">
                      Live topics
                    </p>
                    <div className="mt-5 space-y-4">
                      {snapshot.topics.map((topic) => (
                        <TopicCard key={topic.topic} topic={topic} />
                      ))}
                      {snapshot.topics.length === 0 ? (
                        <EmptyState message="No MQTT messages have been observed yet." />
                      ) : null}
                    </div>
                  </article>
                </div>
              </section>
            ) : (
              <section className="mt-8 grid gap-4 lg:grid-cols-[1.3fr_0.7fr]">
                <article className="rounded-[1.75rem] border border-white/10 bg-[linear-gradient(135deg,rgba(34,211,238,0.16),rgba(15,23,42,0.08)_55%,rgba(249,115,22,0.18))] p-6">
                  <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-100/80">
                    Placeholder
                  </p>
                  <h3 className="mt-3 text-2xl font-semibold text-white">
                    Shell retained from issue #7
                  </h3>
                  <p className="mt-3 max-w-2xl text-sm leading-7 text-slate-200/90">
                    Issue #10 only activates the dashboard and topic explorer slice. The remaining
                    sections stay as placeholders so later issues can fill them in without reworking
                    the application frame.
                  </p>
                </article>

                <article className="rounded-[1.75rem] border border-dashed border-cyan-300/25 bg-slate-900/70 p-6">
                  <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">
                    Scope
                  </p>
                  <ul className="mt-4 space-y-3 text-sm text-slate-300">
                    <li>Live dashboard broker status</li>
                    <li>Realtime topic explorer</li>
                    <li>Readable JSON payload previews</li>
                    <li>Safe truncation for large payloads</li>
                  </ul>
                </article>
              </section>
            )}
          </div>
        </main>
      </div>
    </div>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-2xl border border-white/10 bg-slate-950/35 px-4 py-4">
      <dt className="text-xs uppercase tracking-[0.22em] text-slate-400">{label}</dt>
      <dd className="mt-2 font-mono text-2xl font-semibold text-white">{value}</dd>
    </div>
  )
}

function TopicCard({ topic, compact = false }: { topic: TopicActivity; compact?: boolean }) {
  return (
    <article className="rounded-[1.5rem] border border-white/10 bg-white/[0.03] p-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <p className="font-mono text-sm text-cyan-100">{topic.topic}</p>
          <p className="mt-2 text-xs uppercase tracking-[0.2em] text-slate-400">
            {formatTimestamp(topic.seenAt)}
          </p>
        </div>
        <div className="flex flex-wrap gap-2 text-xs">
          <Badge label={`${topic.payloadBytes} bytes`} />
          {topic.isJson ? <Badge label="JSON" tone="cyan" /> : null}
          {topic.truncated ? <Badge label="Truncated" tone="amber" /> : null}
        </div>
      </div>
      <pre
        className={`mt-4 overflow-x-auto rounded-2xl border border-white/5 bg-slate-950/50 p-4 font-mono text-xs leading-6 text-slate-100 whitespace-pre-wrap break-words ${
          compact ? 'max-h-44' : 'max-h-80'
        }`}
      >
        {topic.payload}
      </pre>
    </article>
  )
}

function Badge({
  label,
  tone = 'slate',
}: {
  label: string
  tone?: 'slate' | 'cyan' | 'amber'
}) {
  const className =
    tone === 'cyan'
      ? 'border-cyan-300/25 bg-cyan-400/10 text-cyan-100'
      : tone === 'amber'
        ? 'border-amber-300/25 bg-amber-400/10 text-amber-100'
        : 'border-white/10 bg-white/[0.05] text-slate-300'

  return <span className={`rounded-full border px-3 py-1 ${className}`}>{label}</span>
}

function EmptyState({ message }: { message: string }) {
  return (
    <div className="rounded-[1.5rem] border border-dashed border-white/10 bg-white/[0.02] px-5 py-8 text-sm text-slate-400">
      {message}
    </div>
  )
}

function resolveWebSocketURL() {
  const envURL = import.meta.env.VITE_MCM_WS_URL
  if (envURL) {
    return envURL
  }

  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const host =
    window.location.port === '5173'
      ? `${window.location.hostname}:8080`
      : window.location.host

  return `${protocol}//${host}/api/ws`
}

function applyServerEvent(current: Snapshot, event: ServerEvent): Snapshot {
  if (event.type === 'snapshot' && event.snapshot) {
    return event.snapshot
  }

  if (event.type === 'broker' && event.broker) {
    return {
      ...current,
      broker: event.broker,
    }
  }

  if (event.type === 'topic' && event.topic) {
    const topics = [event.topic, ...current.topics.filter((item) => item.topic !== event.topic?.topic)]
      .sort((left, right) => new Date(right.seenAt).getTime() - new Date(left.seenAt).getTime())
      .slice(0, 50)

    return {
      ...current,
      topics,
    }
  }

  return current
}

function titleize(value: string) {
  if (!value) {
    return 'Unknown'
  }

  return value.charAt(0).toUpperCase() + value.slice(1)
}

function formatTimestamp(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return 'Unknown'
  }

  return new Intl.DateTimeFormat(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    month: 'short',
    day: 'numeric',
  }).format(date)
}

export default App
