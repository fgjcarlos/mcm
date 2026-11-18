import { useEffect, useMemo, useRef, useState } from 'react'
import { authenticatedFetch, isUnauthorizedResponseError } from '../api/client'
import type { ListenerListResponse, ListenerSpec } from '../broker-config/types'
import type { ACLPermission, ACLScope } from '../acl/types'
import { Lamp } from '../ui/primitives'
import { bodyText, fieldHint, fieldLabel, input, leverSecondary, notice, select, type LampTone } from '../ui/styles'
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

// Green only for a stage MCM proved (observed or verified); red only when the denied operation got through.
const STAGE_LAMP: Record<StageTone, { tone: LampTone; word: string }> = {
  ok: { tone: 'verified', word: 'verified' },
  waiting: { tone: 'caution', word: 'pending' },
  unknown: { tone: 'caution', word: 'not proven' },
  alert: { tone: 'failed', word: 'failed' },
}

const OTHER_LISTENER = '__other__'
// The activation probe of the deployment connects as the new account with
// client ids built from diagnostics.ProbeClientIDPrefix
// (internal/diagnostics/mqtt.go). Its connection is MCM's own and never
// stands for the operator's client. A Go test
// (internal/diagnostics/probe_prefix_test.go) pins that both values match.
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
  return <code className="rounded-plate bg-frame-high px-1.5 py-0.5 font-mono text-xs text-enamel-bright">{children}</code>
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
  const [unlistedListener, setUnlistedListener] = useState('')
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

  // The parent may pass a new onLogout on every render; the reads below must
  // not run again (nor reset the chosen listener) because of it.
  const onLogoutRef = useRef(onLogout)
  useEffect(() => {
    onLogoutRef.current = onLogout
  })

  useEffect(() => {
    let cancelled = false
    const onUnauthorized = () => onLogoutRef.current()
    authenticatedFetch('/api/v1/listeners', { token, onUnauthorized })
      .then(async (response) => {
        if (!response.ok) throw new Error(`listeners request failed (${response.status})`)
        const data = (await response.json()) as ListenerListResponse
        if (cancelled) return
        const specs = data.specs ?? []
        setListeners(specs)
        const ruleListener = scope === 'listener' ? listenerID.trim() : ''
        if (ruleListener && !specs.some((spec) => spec.ID === ruleListener)) {
          // The rule names a listener MCM does not manage: its port and TLS
          // mode must come from the operator, not from another listener.
          setUnlistedListener(ruleListener)
          setSelectedListener(OTHER_LISTENER)
          return
        }
        setUnlistedListener('')
        setSelectedListener(ruleListener || specs[0]?.ID || OTHER_LISTENER)
      })
      .catch((err) => {
        if (cancelled || isUnauthorizedResponseError(err)) return
        setListenersError('MCM could not list its listeners; enter the listener of your broker below.')
        setSelectedListener(OTHER_LISTENER)
      })
    authenticatedFetch('/api/v1/broker/diagnostics', { token, onUnauthorized })
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
  }, [token, scope, listenerID])

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
    if (operationSeen) {
      // The monitor sees the publish; a subscription leaves no trace it can read.
      const publishOnly =
        allowed.operation === 'subscribe and publish' ? ' MCM saw the publish only; the subscription of your client is not observed.' : ''
      return { tone: 'ok', text: `Operation verified: ${allowed.payload} reached ${allowed.topic}.${publishOnly}`, confirmable: false }
    }
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

  const appliedView: StageView = (() => {
    if (deployment.status === 'active_verified') {
      return { tone: 'ok', text: 'Applied and verified by the deployment. That probe is MCM\'s own; it does not stand for your client.', confirmable: false }
    }
    // `applied` is a legacy status: it records the write, not an activation probe.
    if (deployment.status === 'applied') {
      return { tone: 'unknown', text: 'Applied. This deployment status carries no activation proof.', confirmable: false }
    }
    return { tone: 'waiting', text: 'Applied — the deployment did not prove the activation yet.', confirmable: false }
  })()

  const stages: { id: string; label: string; view: StageView }[] = [
    { id: 'applied', label: 'Applied', view: appliedView },
    { id: 'connection', label: 'Connection', view: connectionView },
    { id: 'operation', label: 'Operation', view: operationView },
    { id: 'denied', label: 'Denied operation', view: deniedView },
  ]

  return (
    <section aria-label="Connection test" className="mt-6 space-y-5">
      <h5 className="text-sm font-semibold text-enamel-bright">Connection test</h5>
      <p className={`max-w-[68ch] ${bodyText}`}>
        Run this test with the MQTT client your application uses. MCM does not run it: it only watches what the broker reports.
      </p>

      <div className="grid gap-4 sm:grid-cols-2">
        <label className="block">
          <span className={fieldLabel}>Client host</span>
          <input
            type="text"
            aria-label="Client host"
            value={host}
            onChange={(e) => setHost(e.target.value)}
            className={`mt-2 w-full font-mono ${input}`}
          />
          <span className={`mt-1 block ${fieldHint}`}>
            The address your client uses to reach the broker. MCM cannot know it; it suggests the host you use to reach MCM.
          </span>
        </label>
        <label className="block">
          <span className={fieldLabel}>Listener</span>
          <select
            aria-label="Listener"
            value={selectedListener}
            onChange={(e) => setSelectedListener(e.target.value)}
            className={`mt-2 w-full ${select}`}
          >
            {listeners.map((spec) => (
              <option key={spec.ID} value={spec.ID}>{spec.ID} — port {spec.Port}</option>
            ))}
            <option value={OTHER_LISTENER}>A listener not managed by MCM</option>
          </select>
          {listenersError ? <span className="mt-1 block text-xs leading-5 text-lamp">{listenersError}</span> : null}
          {unlistedListener && selectedListener === OTHER_LISTENER ? (
            <span className="mt-1 block text-xs leading-5 text-lamp">
              The rule names listener {unlistedListener}, which MCM does not list; enter its port and TLS mode below.
            </span>
          ) : null}
        </label>
        {selectedListener === OTHER_LISTENER ? (
          <>
            <label className="block">
              <span className={fieldLabel}>Listener port</span>
              <input
                type="number"
                min={1}
                max={65535}
                value={otherPort}
                onChange={(e) => setOtherPort(e.target.value)}
                className={`mt-2 w-full font-mono ${input}`}
              />
            </label>
            <label className="block">
              <span className={fieldLabel}>Listener TLS</span>
              <select
                value={otherTLS}
                onChange={(e) => setOtherTLS(e.target.value)}
                className={`mt-2 w-full ${select}`}
              >
                <option value="no">No (plain MQTT)</option>
                <option value="tls">Yes (TLS)</option>
                <option value="mtls">Yes, with a client certificate (mTLS)</option>
              </select>
            </label>
          </>
        ) : null}
      </div>

      <dl className="grid gap-x-6 gap-y-2 border-y border-track py-4 text-sm sm:grid-cols-[auto_1fr]">
        <dt className="text-enamel-muted">Host</dt>
        <dd className="break-words font-mono text-enamel-bright">{host || '—'}</dd>
        <dt className="text-enamel-muted">Port</dt>
        <dd className="break-words font-mono text-enamel-bright">{port}</dd>
        <dt className="text-enamel-muted">TLS</dt>
        <dd className="text-enamel-bright">{tls}</dd>
        {websockets ? (
          <>
            <dt className="text-enamel-muted">Transport</dt>
            <dd className="text-enamel-bright">MQTT over WebSockets</dd>
          </>
        ) : null}
        <dt className="text-enamel-muted">Username</dt>
        <dd className="break-words font-mono text-enamel-bright">{username}</dd>
        <dt className="text-enamel-muted">Password</dt>
        <dd className="text-enamel-bright">the one-time password shown when the user was created. MCM does not keep it and cannot show it again.</dd>
        <dt className="text-enamel-muted">Allowed operation</dt>
        <dd className="break-words font-mono text-enamel-bright">{`${permission} on ${topicFilter}`}</dd>
      </dl>
      {anonymous ? (
        <p className={notice.caution}>
          This listener admits anonymous clients: a connection on it does not prove the credentials. Choose an authenticated listener
          for this test.
        </p>
      ) : null}

      <ol className={`max-w-[68ch] list-decimal space-y-2 pl-5 marker:font-mono marker:text-enamel-muted ${bodyText}`}>
        <li>
          Configure any MQTT 3.1.1 or MQTT 5 client with the host, port, TLS mode, username and password above
          {tls.startsWith('Yes') ? ', and trust the broker certificate authority' : ''}
          {tls.includes('mTLS') ? ' and present a client certificate the broker accepts' : ''}.
        </li>
        <li>
          Use a client id that does not start with {MCM_PROBE_CLIENT_PREFIX}: MCM&rsquo;s own activation probe uses that prefix, so
          MCM ignores those connections.
        </li>
        {plan.allowed ? <li>Allowed operation: {operationSentence(plan.allowed, true)}</li> : null}
        {plan.denied ? <li>Operation that must stay denied: {operationSentence(plan.denied, false)}</li> : null}
        <li>
          Node-RED is one optional client: <Code>docs/integrations/node-red.md</Code> shows the same settings in its MQTT nodes.
        </li>
      </ol>

      <ul aria-label="Connection test stages" className="divide-y divide-track border-y border-track-strong">
        {stages.map(({ id, label, view }) => (
          <li key={id} aria-label={label} className="py-3 text-sm">
            <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
              <p className="font-semibold text-enamel-bright">{label}</p>
              <Lamp tone={STAGE_LAMP[view.tone].tone}>{STAGE_LAMP[view.tone].word}</Lamp>
            </div>
            <p className={`mt-1 ${view.tone === 'alert' ? 'text-danger-text' : 'text-enamel'}`}>{view.text}</p>
            {view.confirmable ? (
              <button
                type="button"
                onClick={() => setConfirmed((current) => ({ ...current, [id]: true }))}
                className={`mt-2 ${leverSecondary}`}
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
          className={leverSecondary}
        >
          Watch again
        </button>
      ) : null}
    </section>
  )
}
