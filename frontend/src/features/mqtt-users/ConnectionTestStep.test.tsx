import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import UserOnboardingFlow from './UserOnboardingFlow'

// The connection test of the onboarding journey (#353), driven through the
// journey itself: the operator's own client runs the test and MCM only
// watches what the broker reports.

type RouteHandler = (init?: RequestInit) => Response | Promise<Response>

function jsonResponse(body: unknown, init?: ResponseInit) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' }, ...init })
}

function installFetchMock(routes: Record<string, RouteHandler>) {
  return vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const requestUrl = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
    const method = init?.method ?? 'GET'
    const key = `${method.toUpperCase()} ${requestUrl}`
    const handler = routes[key]
    if (!handler) throw new Error(`Unhandled fetch: ${key}`)
    return await handler(init)
  })
}

class MockWebSocket {
  static instances: MockWebSocket[] = []
  readonly url: string
  readonly protocols: string | string[] | undefined
  private listeners = new Map<string, Set<(event: Event | MessageEvent) => void>>()

  constructor(url: string, protocols?: string | string[]) {
    this.url = url
    this.protocols = protocols
    MockWebSocket.instances.push(this)
    queueMicrotask(() => this.emit('open', new Event('open')))
  }

  addEventListener(type: string, listener: (event: Event | MessageEvent) => void) {
    const listeners = this.listeners.get(type) ?? new Set()
    listeners.add(listener)
    this.listeners.set(type, listeners)
  }

  removeEventListener(type: string, listener: (event: Event | MessageEvent) => void) {
    this.listeners.get(type)?.delete(listener)
  }

  close() {
    this.emit('close', new Event('close'))
  }

  emit(type: string, event: Event | MessageEvent) {
    this.listeners.get(type)?.forEach((listener) => listener(event))
  }

  send(event: Record<string, unknown>) {
    act(() => {
      this.emit('message', new MessageEvent('message', { data: JSON.stringify({ observed_at: new Date().toISOString(), ...event }) }))
    })
  }
}

const listeners = {
  specs: [
    { ID: 'default', Port: 1883, Bind: '0.0.0.0', Protocols: ['mqtt'], Options: { allow_anonymous: 'false' } },
    { ID: 'tls', Port: 8883, Bind: '0.0.0.0', Protocols: ['mqtt'], Options: { require_certificate: 'true' }, tls_profile: 'broker' },
  ],
}

const observableDiagnostics = {
  broker: { status: 'connected', monitor: { client_id: 'mcm-ui-1', subscriptions: ['#', '$SYS/#'] } },
  sources: { broker_logs: { available: true, reason: '' } },
}

function routesWith(overrides: Record<string, RouteHandler> = {}): Record<string, RouteHandler> {
  return {
    'POST /api/v1/mqtt-users': () => jsonResponse({ id: 7, username: 'probe', password: 'generated-secret', disabled: false }),
    'POST /api/v1/acls': () => jsonResponse({ id: 'rule-1', principal: 'probe' }),
    'POST /api/v1/deployments/preview': () =>
      jsonResponse({ revision_id: 'rev-1', acl_diff: '+user probe\n', passwd_diff: '+probe:$7$x\n', has_changes: true }),
    'POST /api/v1/deployments/apply': () =>
      jsonResponse({ id: 11, status: 'active_verified', message: 'activation probe: user grant', created_at: '2026-01-01T00:00:00Z' }),
    'POST /api/v1/auth/mfa/step-up': () =>
      jsonResponse({ proof: 'mfa-proof-token', expires_at: new Date(Date.now() + 300000).toISOString() }),
    'GET /api/v1/listeners': () => jsonResponse(listeners),
    'GET /api/v1/broker/diagnostics': () => jsonResponse(observableDiagnostics),
    ...overrides,
  }
}

async function applyJourney(user: ReturnType<typeof userEvent.setup>, permission = 'readwrite', filter = 'probe/allowed', listenerID = '') {
  await user.type(screen.getByPlaceholderText('device-sensor-01'), 'probe')
  await user.click(screen.getByRole('button', { name: 'Create user' }))
  await screen.findByText('User created — save this password')
  await user.type(screen.getByPlaceholderText('sensors/# or device/+/status'), filter)
  await user.selectOptions(screen.getByRole('combobox', { name: 'Permission' }), permission)
  if (listenerID) {
    await user.selectOptions(screen.getByRole('combobox', { name: 'ACL file' }), 'listener')
    await user.type(screen.getByPlaceholderText('tls'), listenerID)
  }
  await user.click(screen.getByRole('button', { name: 'Save access rule' }))
  await user.click(await screen.findByRole('button', { name: 'Preview changes' }))
  await user.type(await screen.findByPlaceholderText('Authenticator code'), '123456')
  await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
  await screen.findByText(/MFA confirmed for this session/)
  await user.click(screen.getByRole('button', { name: 'Apply and verify' }))
}

async function openTest(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('button', { name: 'Test the connection' }))
  return await screen.findByRole('region', { name: 'Connection test' })
}

function stage(name: string) {
  return screen.getByRole('listitem', { name })
}

function payloadIn(region: HTMLElement, prefix: string) {
  const node = within(region).getAllByText(new RegExp(`^${prefix}-[0-9a-f]{8}$`))[0]
  return node.textContent ?? ''
}

beforeEach(() => {
  MockWebSocket.instances = []
  vi.stubGlobal('WebSocket', MockWebSocket)
})

const originalClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard')

afterEach(() => {
  if (originalClipboard) Object.defineProperty(navigator, 'clipboard', originalClipboard)
  else delete (navigator as { clipboard?: unknown }).clipboard
  vi.unstubAllGlobals()
})

describe('one-time password', () => {
  it('can be copied in its step and is not fetched or shown again after it is dismissed', async () => {
    const fetchMock = installFetchMock(routesWith())
    vi.stubGlobal('fetch', fetchMock)
    const writeText = vi.fn(() => Promise.resolve())
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const user = userEvent.setup()
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })

    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)
    await user.type(screen.getByPlaceholderText('device-sensor-01'), 'probe')
    await user.click(screen.getByRole('button', { name: 'Create user' }))
    await screen.findByText('User created — save this password')

    await user.click(screen.getByRole('button', { name: 'Copy password' }))
    expect(writeText).toHaveBeenCalledWith('generated-secret')
    expect(await screen.findByRole('button', { name: 'Copied' })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(screen.queryByText('generated-secret')).not.toBeInTheDocument()
    const reads = fetchMock.mock.calls.filter(([url, init]) => String(url).includes('/mqtt-users') && (init?.method ?? 'GET') === 'GET')
    expect(reads).toHaveLength(0)
  })
})

describe('one-time password copy failure', () => {
  it.each([
    ['the clipboard is unavailable', undefined],
    ['the browser refuses the copy', { writeText: vi.fn(() => Promise.reject(new Error('denied'))) }],
  ])('says so and keeps the password on screen when %s', async (_case, clipboard) => {
    vi.stubGlobal('fetch', installFetchMock(routesWith()))
    Object.defineProperty(navigator, 'clipboard', { value: clipboard, configurable: true })
    const user = userEvent.setup()
    Object.defineProperty(navigator, 'clipboard', { value: clipboard, configurable: true })
    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)
    await user.type(screen.getByPlaceholderText('device-sensor-01'), 'probe')
    await user.click(screen.getByRole('button', { name: 'Create user' }))
    await screen.findByText('User created — save this password')

    await user.click(screen.getByRole('button', { name: 'Copy password' }))
    expect(await screen.findByText('Copy failed: select the password and copy it by hand.')).toBeInTheDocument()
    expect(screen.getByText('generated-secret')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Copied' })).not.toBeInTheDocument()
  })
})

describe('connection test step', () => {
  it('shows the listener, TLS, user and allowed operation without the password', async () => {
    vi.stubGlobal('fetch', installFetchMock(routesWith()))
    const user = userEvent.setup()
    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)
    await applyJourney(user)
    await user.click(screen.getByRole('button', { name: 'Dismiss' }))

    const region = await openTest(user)
    expect(within(region).getByLabelText('Client host')).toHaveValue(window.location.hostname)
    expect(within(region).getByRole('combobox', { name: 'Listener' })).toHaveValue('default')
    expect(within(region).getByText('1883')).toBeInTheDocument()
    expect(within(region).getByText('No (plain MQTT)')).toBeInTheDocument()
    expect(within(region).getByText('probe')).toBeInTheDocument()
    expect(within(region).getByText('readwrite on probe/allowed')).toBeInTheDocument()
    expect(within(region).getByText(/the one-time password shown when the user was created/)).toBeInTheDocument()
    expect(region.textContent).not.toContain('generated-secret')
    expect(region).toHaveTextContent('Use a client id that does not start with mcm-verify-')

    await user.selectOptions(within(region).getByRole('combobox', { name: 'Listener' }), 'tls')
    expect(within(region).getByText('8883')).toBeInTheDocument()
    expect(within(region).getByText('Yes, with a client certificate (mTLS)')).toBeInTheDocument()
  })

  it('distinguishes applied, connection observed and operation verified from the broker events', async () => {
    vi.stubGlobal('fetch', installFetchMock(routesWith()))
    const user = userEvent.setup()
    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)
    await applyJourney(user)
    const region = await openTest(user)

    await waitFor(() => expect(stage('Connection')).toHaveTextContent('Watching'))
    expect(stage('Applied')).toHaveTextContent('Applied and verified by the deployment')
    expect(stage('Operation')).toHaveTextContent('Watching')

    const socket = MockWebSocket.instances.at(-1)!
    expect(socket.url).toMatch(/\/api\/v1\/broker\/events$/)
    expect(socket.protocols).toEqual(['mcm.v1', 'Bearer.tok'])

    // Another account connecting proves nothing about this one.
    socket.send({ type: 'broker_log_entry', kind: 'client_connected', client_id: 'other', username: 'someone-else' })
    expect(stage('Connection')).toHaveTextContent('Watching')

    // The activation probe of the deployment logs in as the new account too;
    // its line reaches the stream a poll later and is MCM's own client.
    for (const probe of ['mcm-verify-sub-1a2b3c', 'mcm-verify-pub-1a2b3c', 'mcm-verify-neg-1a2b3c', 'mcm-verify-deny-1a2b3c']) {
      socket.send({ type: 'broker_log_entry', kind: 'client_connected', client_id: probe, username: 'probe' })
      expect(stage('Connection')).toHaveTextContent('Watching')
    }
    // Its publish carries MCM's own payload, never the unique test payload.
    socket.send({ type: 'topic_message', source: 'observed', topic: 'probe/allowed', payload_preview: 'mcm-verify' })
    expect(stage('Operation')).toHaveTextContent('Watching')

    socket.send({ type: 'broker_log_entry', kind: 'client_connected', client_id: 'app-1', username: 'probe' })
    expect(stage('Connection')).toHaveTextContent('Connection observed: client app-1 as probe')
    expect(stage('Operation')).toHaveTextContent('Watching')

    const payload = payloadIn(region, 'mcm-test')
    socket.send({ type: 'topic_message', source: 'observed', topic: 'probe/allowed', payload_preview: 'something else' })
    expect(stage('Operation')).toHaveTextContent('Watching')
    socket.send({ type: 'topic_message', source: 'observed', topic: 'probe/allowed', payload_preview: payload })
    expect(stage('Operation')).toHaveTextContent(`Operation verified: ${payload} reached probe/allowed`)
    // readwrite: the monitor sees the publish; the subscription stays with the client.
    expect(stage('Operation')).toHaveTextContent('MCM saw the publish only; the subscription of your client is not observed')

    expect(stage('Denied operation')).toHaveTextContent('No message with the denied payload reached the broker')
  })

  it('flags a denied operation whose message reached the broker', async () => {
    vi.stubGlobal('fetch', installFetchMock(routesWith()))
    const user = userEvent.setup()
    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)
    await applyJourney(user)
    const region = await openTest(user)
    await waitFor(() => expect(stage('Connection')).toHaveTextContent('Watching'))

    const denied = payloadIn(region, 'mcm-denied')
    MockWebSocket.instances.at(-1)!.send({ type: 'topic_message', source: 'observed', topic: `${denied}/probe`, payload_preview: denied })
    expect(stage('Denied operation')).toHaveTextContent('The denied operation reached the broker')
  })

  it('says why a stage cannot be observed and labels a manual confirmation as not observed by MCM', async () => {
    vi.stubGlobal(
      'fetch',
      installFetchMock(
        routesWith({
          'GET /api/v1/broker/diagnostics': () =>
            jsonResponse({
              broker: { status: 'connected', monitor: { client_id: 'mcm-ui-1', subscriptions: ['#'] } },
              sources: { broker_logs: { available: false, reason: 'broker log path is not configured' } },
            }),
        }),
      ),
    )
    const user = userEvent.setup()
    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)
    await applyJourney(user, 'read', 'device/+/status')
    await openTest(user)

    await waitFor(() => expect(stage('Connection')).toHaveTextContent('Not observable: broker log path is not configured'))
    expect(stage('Operation')).toHaveTextContent('Not observable: a subscription leaves no trace MCM can read')
    expect(stage('Denied operation')).toHaveTextContent('Watching')

    await user.click(within(stage('Connection')).getByRole('button', { name: 'Confirm manually' }))
    expect(stage('Connection')).toHaveTextContent('Confirmed by you — not observed by MCM')
  })

  it('says the operation cannot be observed when the MCM monitor does not read every topic', async () => {
    vi.stubGlobal(
      'fetch',
      installFetchMock(
        routesWith({
          'GET /api/v1/broker/diagnostics': () =>
            jsonResponse({ broker: { status: 'disconnected', monitor: { subscriptions: [] } }, sources: { broker_logs: { available: true } } }),
        }),
      ),
    )
    const user = userEvent.setup()
    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)
    await applyJourney(user)
    await openTest(user)

    await waitFor(() => expect(stage('Operation')).toHaveTextContent('Not observable: the MCM broker monitor is not subscribed to every topic'))
    expect(stage('Denied operation')).toHaveTextContent('Not observable: the MCM broker monitor is not subscribed to every topic')
    expect(stage('Connection')).toHaveTextContent('Watching')
  })

  it('says the operation cannot be observed when the diagnostics report no monitor subscriptions', async () => {
    vi.stubGlobal(
      'fetch',
      installFetchMock(
        routesWith({
          'GET /api/v1/broker/diagnostics': () =>
            jsonResponse({ broker: { status: 'disconnected' }, sources: { broker_logs: { available: true } } }),
        }),
      ),
    )
    const user = userEvent.setup()
    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)
    await applyJourney(user)
    await openTest(user)

    await waitFor(() => expect(stage('Operation')).toHaveTextContent('Not observable: the MCM broker monitor is not subscribed to every topic'))
    expect(stage('Denied operation')).toHaveTextContent('Not observable: the MCM broker monitor is not subscribed to every topic')
  })

  it('keeps the listener choice and does not reload it when the parent renders again', async () => {
    const fetchMock = installFetchMock(routesWith())
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    const { rerender } = render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)
    await applyJourney(user)
    const region = await openTest(user)
    await user.selectOptions(within(region).getByRole('combobox', { name: 'Listener' }), 'tls')

    rerender(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)
    await waitFor(() => expect(stage('Connection')).toHaveTextContent('Watching'))
    expect(within(region).getByRole('combobox', { name: 'Listener' })).toHaveValue('tls')
    const listenerReads = fetchMock.mock.calls.filter(([url]) => String(url).includes('/api/v1/listeners'))
    expect(listenerReads).toHaveLength(1)
  })

  it('asks for the listener by hand when the rule names one MCM does not list', async () => {
    vi.stubGlobal('fetch', installFetchMock(routesWith()))
    const user = userEvent.setup()
    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)
    await applyJourney(user, 'readwrite', 'probe/allowed', 'edge')
    const region = await openTest(user)

    await waitFor(() => expect(within(region).getByRole('combobox', { name: 'Listener' })).toHaveValue('__other__'))
    expect(region).toHaveTextContent('The rule names listener edge, which MCM does not list')
  })

  it('does not claim an activation proof for a deployment recorded as applied', async () => {
    vi.stubGlobal(
      'fetch',
      installFetchMock(
        routesWith({
          'POST /api/v1/deployments/apply': () =>
            jsonResponse({ id: 13, status: 'applied', message: '', created_at: '2026-01-01T00:00:00Z' }),
        }),
      ),
    )
    const user = userEvent.setup()
    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)
    await applyJourney(user)
    // The apply banner must not claim the proof either.
    expect(await screen.findByText('Applied — no activation proof recorded')).toBeInTheDocument()
    expect(screen.queryByText('Applied and verified')).not.toBeInTheDocument()
    await openTest(user)

    expect(stage('Applied')).toHaveTextContent('Applied. This deployment status carries no activation proof.')
    expect(stage('Applied')).not.toHaveTextContent('verified')
  })

  it('keeps watching, not unobservable, while the diagnostics are still loading', async () => {
    let release: (response: Response) => void = () => {}
    vi.stubGlobal(
      'fetch',
      installFetchMock(
        routesWith({
          'GET /api/v1/broker/diagnostics': () => new Promise<Response>((resolve) => { release = resolve }),
        }),
      ),
    )
    const user = userEvent.setup()
    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)
    await applyJourney(user)
    await openTest(user)

    await waitFor(() => expect(stage('Operation')).toHaveTextContent('Starting the watch'))
    expect(stage('Operation')).not.toHaveTextContent('Not observable')
    release(jsonResponse({ broker: { status: 'connected' }, sources: { broker_logs: { available: true } } }))
    await waitFor(() => expect(stage('Operation')).toHaveTextContent('Not observable: the MCM broker monitor is not subscribed to every topic'))
  })

  it('reports every stage as not observable when the event stream closes', async () => {
    vi.stubGlobal('fetch', installFetchMock(routesWith()))
    const user = userEvent.setup()
    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)
    await applyJourney(user)
    await openTest(user)
    await waitFor(() => expect(stage('Connection')).toHaveTextContent('Watching'))

    act(() => MockWebSocket.instances.at(-1)!.close())
    expect(stage('Connection')).toHaveTextContent('Not observable: the broker event stream is closed')
    expect(stage('Operation')).toHaveTextContent('Not observable: the broker event stream is closed')
  })

  it('is offered only after the deployment left the configuration on the broker', async () => {
    vi.stubGlobal(
      'fetch',
      installFetchMock(
        routesWith({
          'POST /api/v1/deployments/apply': () =>
            jsonResponse({ id: 12, status: 'rolled_back', message: 'verification failed', created_at: '2026-01-01T00:00:00Z' }),
        }),
      ),
    )
    const user = userEvent.setup()
    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)
    await applyJourney(user)

    expect(await screen.findByText('Rolled back')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Test the connection' })).not.toBeInTheDocument()
  })
})
