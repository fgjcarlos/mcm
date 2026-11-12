import { useEffect, useMemo, useState } from 'react'
import { authenticatedFetch, isUnauthorizedResponseError } from '../api/client'
import type { ListenerListResponse, ListenerSpec } from '../broker-config/types'
import type { ACLPermission, ACLScope } from '../acl/types'
import { newTestNonce, planConnectionTest, type PlannedOperation } from './connectionTest'

// The last step of the onboarding journey (#353): the connection data of
// the chosen listener and a manual test any MQTT client can run.
//
// The test is run by the operator's own client. MCM never infers its
// success from the activation probe of the deployment: it only watches the
// broker. A connection is observed from the broker log line that names the
// account; an operation is verified when the MCM monitor (which reads `#`)
// sees the unique payload on the test topic. Whatever MCM cannot observe is
// said so, and a manual confirmation stays labelled as not observed by MCM.

type Deployment = { status: string; message?: string }

type BrokerEvent = {
  type?: string
  kind?: string
  client_id?: string
  username?: string
  source?: string
  topic?: string
  payload_preview?: string
}

type Diagnostics = {
  broker?: { monitor?: { subscriptions?: string[] } }
  sources?: { broker_logs?: { available?: boolean; reason?: string } }
}

type ConnectionTestStepProps = {
  token: string
  onLogout: () => void
  role: string
  username: string
  topicFilter: string
  permission: ACLPermission
  scope: ACLScope
  listenerID: string
  deployment: Deployment
}

type StreamState = 'connecting' | 'open' | 'closed'
type StageTone = 'ok' | 'waiting' | 'unknown' | 'alert'
type StageView = { tone: StageTone; text: string; confirmable: boolean }

const OTHER_LISTENER = '__other__'
// The activation probe of the deployment (internal/diagnostics) connects as
// the new account with these client ids. Its connection is MCM's own and
// never stands for the operator's client.
const MCM_PROBE_CLIENT_PREFIX = 'mcm-verify-'
const LOG_ROLES = ['auditor', 'operator', 'admin']

function tlsLabel(listener: ListenerSpec) {
  if (!listener.tls_profile) return 'No (plain MQTT)'
  return listener.Options?.require_certificate === 'true' ? 'Yes, with a client certificate (mTLS)' : 'Yes (TLS)'
}

function operationSentence(op: PlannedOperation, allowed: boolean) {
  if (op.operation === 'subscribe and publish') {
    return (
      <>Subscribe to <Code>{op.topic}</Code>, then publish exactly <Code>{op.payload}</Code> to it. Your client should receive the message back.</>
    )
  }
  if (op.operation === 'publish') {
    return allowed ? (
      <>Publish exactly <Code>{op.payload}</Code> to <Code>{op.topic}</Code>.</>
    ) : (
      <>
        Publish exactly <Code>{op.payload}</Code> to <Code>{op.topic}</Code>. Mosquitto drops a refused publish without telling an MQTT
        3.1.1 client; an MQTT 5 client publishing with QoS 1 gets a &ldquo;not authorized&rdquo; reason code.
      </>
    )
  }
  return allowed ? (
    <>
      Subscribe to <Code>{op.topic}</Code> and publish a message to it from another account allowed to write it. Your client should
      receive it. The MCM monitor only reads, so it cannot publish one for you.
    </>
  ) : (
    <>Subscribe to <Code>{op.topic}</Code>. The broker should refuse the subscription (SUBACK failure 0x80, or &ldquo;not authorized&rdquo; in MQTT 5).</>
  )
}

function Code({ children }: { children: string }) {
  return <code className="rounded bg-slate-950/70 px-1.5 py-0.5 font-mono text-xs text-cyan-100">{children}</code>
}

export default function ConnectionTestStep({
  token,
  onLogout,
  role,
  username,
  topicFilter,
  permission,
  scope,
  listenerID,
  deployment,
}: ConnectionTestStepProps) {
  const [nonce] = useState(newTestNonce)
  const plan = useMemo(() => planConnectionTest(topicFilter, permission, nonce), [topicFilter, permission, nonce])

  const [host, setHost] = useState(window.location.hostname)
  const [listeners, setListeners] = useState<ListenerSpec[]>([])
  const [listenersError, setListenersError] = useState('')
  const [selectedListener, setSelectedListener] = useState('')
  const [otherPort, setOtherPort] = useState('1883')
  const [otherTLS, setOtherTLS] = useState('no')

  const [diagnostics, setDiagnostics] = useState<Diagnostics | null>(null)
  const [diagnosticsError, setDiagnosticsError] = useState('')
  const [stream, setStream] = useState<StreamState>('connecting')
  const [watchRun, setWatchRun] = useState(0)

  const [connection, setConnection] = useState<{ clientID: string } | null>(null)
  const [operationSeen, setOperationSeen] = useState(false)
  const [deniedSeen, setDeniedSeen] = useState(false)
  const [confirmed, setConfirmed] = useState<Record<string, boolean>>({})

  useEffect(() => {
    let cancelled = false
    authenticatedFetch('/api/v1/listeners', { token, onUnauthorized: onLogout })
      .then(async (response) => {
        if (!response.ok) throw new Error(`listeners request failed (${response.status})`)
        const data = (await response.json()) as ListenerListResponse
        if (cancelled) return
        const specs = data.specs ?? []
        setListeners(specs)
        const preferred = scope === 'listener' ? specs.find((spec) => spec.ID === listenerID.trim()) : undefined
        setSelectedListener(preferred?.ID ?? specs[0]?.ID ?? OTHER_LISTENER)
      })
      .catch((err) => {
        if (cancelled || isUnauthorizedResponseError(err)) return
        setListenersError('MCM could not list its listeners; enter the listener of your broker below.')
        setSelectedListener(OTHER_LISTENER)
      })
    authenticatedFetch('/api/v1/broker/diagnostics', { token, onUnauthorized: onLogout })
      .then(async (response) => {
        if (!response.ok) throw new Error(`the broker diagnostics answered ${response.status}`)
        const data = (await response.json()) as Diagnostics
        if (!cancelled) setDiagnostics(data)
      })
      .catch((err) => {
        if (cancelled || isUnauthorizedResponseError(err)) return
        setDiagnosticsError(`MCM could not read the broker diagnostics (${(err as Error).message})`)
      })
    return () => {
      cancelled = true
    }
  }, [token, onLogout, scope, listenerID])

  // The socket opens with the step, before the client connects: the broker
  // log entries are not replayed to a late subscriber.
  useEffect(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const socket = new WebSocket(`${protocol}//${window.location.host}/api/v1/broker/events`, ['mcm.v1', `Bearer.${token}`])
    let active = true
    const onOpen = () => { if (active) setStream('open') }
    const onClose = () => { if (active) setStream('closed') }
    const onMessage = (message: MessageEvent) => {
      if (!active) return
      let event: BrokerEvent
      try {
        event = JSON.parse(String(message.data)) as BrokerEvent
      } catch {
        return
      }
      if (event.type === 'broker_log_entry' && event.kind === 'client_connected' && event.username === username) {
        if ((event.client_id ?? '').startsWith(MCM_PROBE_CLIENT_PREFIX)) return
        setConnection({ clientID: event.client_id ?? '' })
        return
      }
      if (event.type !== 'topic_message' || event.source !== 'observed') return
      const payload = (event.payload_preview ?? '').trim()
      if (plan.allowed?.payload && event.topic === plan.allowed.topic && payload === plan.allowed.payload) setOperationSeen(true)
      if (plan.denied?.payload && event.topic === plan.denied.topic && payload === plan.denied.payload) setDeniedSeen(true)
    }
    socket.addEventListener('open', onOpen)
    socket.addEventListener('close', onClose)
    socket.addEventListener('error', onClose)
    socket.addEventListener('message', onMessage as (event: Event) => void)
    return () => {
      active = false
      socket.removeEventListener('open', onOpen)
      socket.removeEventListener('close', onClose)
      socket.removeEventListener('error', onClose)
      socket.removeEventListener('message', onMessage as (event: Event) => void)
      socket.close()
    }
  }, [token, username, plan, watchRun])

  const listener = listeners.find((spec) => spec.ID === selectedListener)
  const port = listener ? String(listener.Port) : otherPort
  const tls = listener ? tlsLabel(listener) : otherTLS === 'mtls' ? 'Yes, with a client certificate (mTLS)' : otherTLS === 'tls' ? 'Yes (TLS)' : 'No (plain MQTT)'
  const anonymous = listener?.Options?.allow_anonymous === 'true' || listener?.Options?.listener_allow_anonymous === 'true'
  const websockets = listener?.Protocols?.includes('websockets') ?? false

  const loading = stream === 'connecting' || (diagnostics === null && diagnosticsError === '')
  // Diagnostics without a subscription list mean no monitor reads the
  // topics; null only while the diagnostics are still loading.
  const monitorReadsAll = diagnostics ? (diagnostics.broker?.monitor?.subscriptions?.includes('#') ?? false) : null
  const logs = diagnostics?.sources?.broker_logs

  const confirmedView: StageView = { tone: 'unknown', text: 'Confirmed by you — not observed by MCM.', confirmable: false }
  const unobservable = (reason: string): StageView => ({ tone: 'unknown', text: `Not observable: ${reason}.`, confirmable: true })

  const connectionView: StageView = (() => {
    if (connection) return { tone: 'ok', text: `Connection observed: client ${connection.clientID} as ${username}.`, confirmable: false }
    if (confirmed.connection) return confirmedView
    if (stream === 'closed') return unobservable('the broker event stream is closed')
    if (!LOG_ROLES.includes(role)) return unobservable('your role does not receive broker log entries')
    if (diagnosticsError) return unobservable(diagnosticsError)
    if (logs && logs.available === false) return unobservable(logs.reason || 'the broker log is not ingested by MCM')
    if (loading) return { tone: 'waiting', text: 'Starting the watch…', confirmable: false }
    return { tone: 'waiting', text: `Watching for a connection authenticated as ${username}.`, confirmable: false }
  })()

  const operationView: StageView = (() => {
    const allowed = plan.allowed
    if (!allowed) return { tone: 'unknown', text: 'No operation is granted: the rule denies this topic filter.', confirmable: false }
    if (operationSeen) return { tone: 'ok', text: `Operation verified: ${allowed.payload} reached ${allowed.topic}.`, confirmable: false }
    if (confirmed.operation) return confirmedView
    if (!allowed.observable) return unobservable('a subscription leaves no trace MCM can read')
    if (stream === 'closed') return unobservable('the broker event stream is closed')
    if (diagnosticsError) return unobservable(diagnosticsError)
    if (monitorReadsAll === false) return unobservable('the MCM broker monitor is not subscribed to every topic')
    if (loading) return { tone: 'waiting', text: 'Starting the watch…', confirmable: false }
    return { tone: 'waiting', text: `Watching ${allowed.topic} for ${allowed.payload}.`, confirmable: false }
  })()

  const deniedView: StageView = (() => {
    const denied = plan.denied
    if (!denied) return { tone: 'unknown', text: 'No denied topic can be proposed: this grant covers every topic.', confirmable: false }
    if (deniedSeen) {
      return { tone: 'alert', text: `The denied operation reached the broker: ${denied.payload} arrived on ${denied.topic}. Another rule grants it to this account.`, confirmable: false }
    }
    if (confirmed.denied) return confirmedView
    if (!denied.observable) return unobservable('a refused subscription is reported only to your client')
    if (stream === 'closed') return unobservable('the broker event stream is closed')
    if (diagnosticsError) return unobservable(diagnosticsError)
    if (monitorReadsAll === false) return unobservable('the MCM broker monitor is not subscribed to every topic')
    if (loading) return { tone: 'waiting', text: 'Starting the watch…', confirmable: false }
    return {
      tone: 'waiting',
      text: 'Watching. No message with the denied payload reached the broker so far. That absence is consistent with the denial; MCM cannot see the refusal itself.',
      confirmable: false,
    }
  })()

  const appliedView: StageView =
    deployment.status === 'active_verified' || deployment.status === 'applied'
      ? { tone: 'ok', text: 'Applied and verified by the deployment. That probe is MCM\'s own; it does not stand for your client.', confirmable: false }
      : { tone: 'waiting', text: 'Applied — the deployment did not prove the activation yet.', confirmable: false }

  const stages: { id: string; label: string; view: StageView }[] = [
    { id: 'applied', label: 'Applied', view: appliedView },
    { id: 'connection', label: 'Connection', view: connectionView },
    { id: 'operation', label: 'Operation', view: operationView },
    { id: 'denied', label: 'Denied operation', view: deniedView },
  ]

  const toneClass: Record<StageTone, string> = {
    ok: 'border-emerald-300/30 bg-emerald-400/10 text-emerald-50',
    waiting: 'border-white/10 bg-white/[0.03] text-slate-200',
    unknown: 'border-amber-300/30 bg-amber-400/10 text-amber-50',
    alert: 'border-rose-300/30 bg-rose-400/10 text-rose-50',
  }

  return (
    <section aria-label="Connection test" className="mt-6 space-y-5">
      <p className="text-sm text-slate-300">
        Run this test with the MQTT client your application uses. MCM does not run it: it only watches what the broker reports.
      </p>

      <div className="grid gap-4 sm:grid-cols-2">
        <label className="block">
          <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Client host</span>
          <input
            type="text"
            aria-label="Client host"
            value={host}
            onChange={(e) => setHost(e.target.value)}
            className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
          />
          <span className="mt-1 block text-xs text-slate-400">
            The address your client uses to reach the broker. MCM cannot know it; it suggests the host you use to reach MCM.
          </span>
        </label>
        <label className="block">
          <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Listener</span>
          <select
            aria-label="Listener"
            value={selectedListener}
            onChange={(e) => setSelectedListener(e.target.value)}
            className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30"
          >
            {listeners.map((spec) => (
              <option key={spec.ID} value={spec.ID}>{spec.ID} — port {spec.Port}</option>
            ))}
            <option value={OTHER_LISTENER}>A listener not managed by MCM</option>
          </select>
          {listenersError ? <span className="mt-1 block text-xs text-amber-200">{listenersError}</span> : null}
        </label>
        {selectedListener === OTHER_LISTENER ? (
          <>
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Listener port</span>
              <input
                type="number"
                min={1}
                max={65535}
                value={otherPort}
                onChange={(e) => setOtherPort(e.target.value)}
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none"
              />
            </label>
            <label className="block">
              <span className="text-xs uppercase tracking-[0.18em] text-cyan-300">Listener TLS</span>
              <select
                value={otherTLS}
                onChange={(e) => setOtherTLS(e.target.value)}
                className="mt-2 w-full rounded-xl border border-white/10 bg-slate-950/60 px-4 py-2.5 text-sm text-white outline-none"
              >
                <option value="no">No (plain MQTT)</option>
                <option value="tls">Yes (TLS)</option>
                <option value="mtls">Yes, with a client certificate (mTLS)</option>
              </select>
            </label>
          </>
        ) : null}
      </div>

      <dl className="grid gap-x-6 gap-y-2 rounded-2xl border border-white/10 bg-slate-950/40 p-5 text-sm sm:grid-cols-[auto_1fr]">
        <dt className="text-slate-400">Host</dt>
        <dd className="font-mono text-slate-100">{host || '—'}</dd>
        <dt className="text-slate-400">Port</dt>
        <dd className="font-mono text-slate-100">{port}</dd>
        <dt className="text-slate-400">TLS</dt>
        <dd className="text-slate-100">{tls}</dd>
        {websockets ? (
          <>
            <dt className="text-slate-400">Transport</dt>
            <dd className="text-slate-100">MQTT over WebSockets</dd>
          </>
        ) : null}
        <dt className="text-slate-400">Username</dt>
        <dd className="font-mono text-slate-100">{username}</dd>
        <dt className="text-slate-400">Password</dt>
        <dd className="text-slate-100">the one-time password shown when the user was created. MCM does not keep it and cannot show it again.</dd>
        <dt className="text-slate-400">Allowed operation</dt>
        <dd className="font-mono text-slate-100">{`${permission} on ${topicFilter}`}</dd>
      </dl>
      {anonymous ? (
        <p className="rounded-2xl border border-amber-300/30 bg-amber-400/10 px-4 py-3 text-sm text-amber-50">
          This listener admits anonymous clients: a connection on it does not prove the credentials. Choose an authenticated listener
          for this test.
        </p>
      ) : null}

      <ol className="list-decimal space-y-2 pl-5 text-sm text-slate-300">
        <li>
          Configure any MQTT 3.1.1 or MQTT 5 client with the host, port, TLS mode, username and password above
          {tls.startsWith('Yes') ? ', and trust the broker certificate authority' : ''}
          {tls.includes('mTLS') ? ' and present a client certificate the broker accepts' : ''}.
        </li>
        {plan.allowed ? <li>Allowed operation: {operationSentence(plan.allowed, true)}</li> : null}
        {plan.denied ? <li>Operation that must stay denied: {operationSentence(plan.denied, false)}</li> : null}
        <li>
          Node-RED is one optional client: <Code>docs/integrations/node-red.md</Code> shows the same settings in its MQTT nodes.
        </li>
      </ol>

      <ul aria-label="Connection test stages" className="space-y-2">
        {stages.map(({ id, label, view }) => (
          <li key={id} aria-label={label} className={`rounded-2xl border px-4 py-3 text-sm ${toneClass[view.tone]}`}>
            <p className="text-xs font-semibold uppercase tracking-[0.18em]">{label}</p>
            <p className="mt-1">{view.text}</p>
            {view.confirmable ? (
              <button
                type="button"
                onClick={() => setConfirmed((current) => ({ ...current, [id]: true }))}
                className="mt-2 rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-200 transition hover:border-white/20 hover:text-white"
              >
                Confirm manually
              </button>
            ) : null}
          </li>
        ))}
      </ul>

      {stream === 'closed' ? (
        <button
          type="button"
          onClick={() => { setStream('connecting'); setWatchRun((run) => run + 1) }}
          className="rounded-xl border border-white/10 px-4 py-2 text-sm text-slate-300 transition hover:border-white/20 hover:text-white"
        >
          Watch again
        </button>
      ) : null}
    </section>
  )
}
