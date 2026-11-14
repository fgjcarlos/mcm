import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'

type RouteHandler = (init?: RequestInit, url?: string) => Response | Promise<Response>

class LocalStorageMock implements Storage {
  private store = new Map<string, string>()

  get length() {
    return this.store.size
  }

  clear() {
    this.store.clear()
  }

  getItem(key: string) {
    return this.store.get(key) ?? null
  }

  key(index: number) {
    return Array.from(this.store.keys())[index] ?? null
  }

  removeItem(key: string) {
    this.store.delete(key)
  }

  setItem(key: string, value: string) {
    this.store.set(key, String(value))
  }
}

class MockWebSocket {
  static instances: MockWebSocket[] = []

  readonly url: string
  readonly protocols: string | string[] | undefined
  readyState = 0
  private listeners = new Map<string, Set<(event: Event | MessageEvent) => void>>()

  constructor(url: string, protocols?: string | string[]) {
    this.url = url
    this.protocols = protocols
    MockWebSocket.instances.push(this)

    queueMicrotask(() => {
      this.readyState = 1
      this.emit('open', new Event('open'))
    })
  }

  addEventListener(type: string, listener: EventListenerOrEventListenerObject) {
    const callback =
      typeof listener === 'function'
        ? listener
        : (event: Event | MessageEvent) => listener.handleEvent(event)
    const listeners = this.listeners.get(type) ?? new Set<(event: Event | MessageEvent) => void>()
    listeners.add(callback)
    this.listeners.set(type, listeners)
  }

  removeEventListener(type: string, listener: EventListenerOrEventListenerObject) {
    const listeners = this.listeners.get(type)
    if (!listeners) return

    const callback =
      typeof listener === 'function'
        ? listener
        : (event: Event | MessageEvent) => listener.handleEvent(event)
    listeners.delete(callback)
  }

  close() {
    this.readyState = 3
    this.emit('close', new Event('close'))
  }

  send() {}

  emit(type: string, event: Event | MessageEvent) {
    for (const listener of this.listeners.get(type) ?? []) {
      listener(event)
    }
  }
}

function jsonResponse(body: unknown, init?: ResponseInit) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
}

function installFetchMock(routes: Record<string, RouteHandler>) {
  return vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const requestUrl = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
    const key = `${method.toUpperCase()} ${requestUrl}`
    // A listing request carries its query (?limit=&offset=...); fall back to the bare path.
    const handler = routes[key] ?? routes[key.split('?')[0]] ?? (key === 'POST /api/v1/auth/mfa/step-up'
      ? () => jsonResponse({ proof: 'recent-proof', expires_at: new Date(Date.now() + 300000).toISOString() })
      : undefined)

    if (!handler) {
      throw new Error(`Unhandled fetch: ${key}`)
    }

    return await handler(init, requestUrl)
  })
}

// The paged listing envelope of issue #356 (S10) for a single page.
function envelope(rowsKey: string, rows: unknown[]) {
  return jsonResponse({ [rowsKey]: rows, total: rows.length, limit: 25, offset: 0 })
}

// Answers like a paged listing endpoint: keeps the rows that match the
// request query, echoes limit/offset and counts the matches.
function pagedRoute<T>(rowsKey: string, all: T[], matches: (row: T, params: URLSearchParams) => boolean = () => true): RouteHandler {
  return (_init, url) => {
    const params = new URL(url ?? '', 'http://localhost').searchParams
    const found = all.filter((row) => matches(row, params))
    const limit = Number(params.get('limit') ?? 50)
    const offset = Number(params.get('offset') ?? 0)
    return jsonResponse({ [rowsKey]: found.slice(offset, offset + limit), total: found.length, limit, offset })
  }
}

function includesParam(value: string, params: URLSearchParams, name: string) {
  return value.toLowerCase().includes((params.get(name) ?? '').toLowerCase())
}

function authenticatedRoutes(token: string): Record<string, RouteHandler> {
  return {
    'GET /api/v1/auth/me': (init) => {
      expect(init?.headers).toEqual(expect.objectContaining({ Authorization: `Bearer ${token}` }))

      return jsonResponse({
        id: 9,
        username: 'restored-operator',
        disabled: false,
        role: 'operator',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
      })
    },
    'GET /api/v1/status': () =>
      jsonResponse({
        broker: {
          status: 'connected',
          metrics: {
            traffic: {
              window_seconds: 300,
              message_count: 1,
              message_rate_per_minute: 0.2,
              rate_points: [],
              top_topics: [],
              top_clients: [],
              top_clients_available: false,
              top_clients_note: 'Unavailable in tests.',
              persistence: 'In-memory',
            },
          },
        },
      }),
  }
}

describe('App', () => {
  const localStorageMock = new LocalStorageMock()

  beforeEach(() => {
    localStorageMock.clear()
    Object.defineProperty(window, 'localStorage', {
      value: localStorageMock,
      configurable: true,
    })
    MockWebSocket.instances = []
    window.history.replaceState(null, '', '/')
    vi.stubGlobal('WebSocket', MockWebSocket)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('signs in and persists the admin token', async () => {
    const fetchMock = installFetchMock({
      'POST /api/v1/auth/login': () =>
        jsonResponse({
          token: 'token-123',
          user: {
            id: 7,
            username: 'operator',
            disabled: false,
            role: 'operator',
            created_at: '2026-01-01T00:00:00Z',
            updated_at: '2026-01-01T00:00:00Z',
          },
        }),
      'GET /api/v1/auth/me': () =>
        jsonResponse({
          id: 7,
          username: 'operator',
          disabled: false,
          role: 'operator',
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-01T00:00:00Z',
        }),
      'GET /api/v1/status': () =>
        jsonResponse({
          broker: {
            status: 'connected',
            metrics: {
              traffic: {
                window_seconds: 300,
                message_count: 0,
                message_rate_per_minute: 0,
                rate_points: [],
                top_topics: [],
                top_clients: [],
                top_clients_available: false,
                top_clients_note: 'Unavailable in tests.',
                persistence: 'In-memory',
              },
            },
          },
        }),
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)

    const user = userEvent.setup()
    await user.type(screen.getByLabelText(/username/i), 'operator')
    await user.type(screen.getByLabelText(/password/i), 'secret-pass')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))

    await screen.findByText('Signed in')

    expect(window.localStorage.getItem('mcm_admin_token')).toBe('token-123')
    expect(MockWebSocket.instances).toHaveLength(1)
    expect(MockWebSocket.instances[0]?.protocols).toEqual(['mcm.v1', 'Bearer.token-123'])
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/auth/me',
      expect.objectContaining({
        headers: expect.objectContaining({ Authorization: 'Bearer token-123' }),
      }),
    )
  })

  it('clears a stale token when session restore is unauthorized', async () => {
    window.localStorage.setItem('mcm_admin_token', 'expired-token')

    const fetchMock = installFetchMock({
      'GET /api/v1/auth/me': () => new Response(null, { status: 401 }),
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)

    await screen.findByRole('heading', { name: 'Sign in' })

    expect(window.localStorage.getItem('mcm_admin_token')).toBeNull()
    expect(MockWebSocket.instances).toHaveLength(0)
  })

  it('renders separate broker diagnostic sources and applies the query filters', async () => {
    window.localStorage.setItem('mcm_admin_token', 'diagnostic-token')
    const diagnostics = {
      sources: {
        observed_traffic: { message_count: 7, window_seconds: 300, message_rate_per_minute: 1.4, top_topics: [{ name: 'plant/line-1/state', count: 5, percentage: 71 }], top_clients_note: 'Client IDs are not included in observed messages.', rate_points: [], top_clients: [], top_clients_available: false, persistence: 'memory' },
        mcm_events: { status_events: 2, topic_messages: 9, event_subscribers: 3, last_message_at: '2026-01-01T00:00:00Z', note: 'Produced by MCM' },
        broker_logs: { available: false, reason: 'log file permission denied', entries: [{ observed_at: '2026-01-01T00:00:00Z', severity: 'warning', kind: 'client-connected', client_id: 'broker-client-7', message: 'Connected' }], severity_counts: { error: 0, warning: 1, info: 0, debug: 0 }, limits: { max_entries: 200, max_entries_per_read: 50, max_message_bytes: 1024, max_age_seconds: 900 }, note: 'Broker-owned logs.' },
        internal_metrics: { endpoint: '/metrics', format: 'prometheus', counters: { mcm_broker_status: 1 }, note: 'Produced by MCM' },
      },
      broker_sys: { available: true, reason: '', metrics: { 'broker/clients/connected': { value: '3', observed_at: '2026-01-01T00:00:00Z' } }, note: 'Broker $SYS feed.' },
      diagnostics: {
        tcp: { status: 'reachable', address: 'broker:1883', latency_ms: 2.5, error: '' },
        tls: { status: 'disabled', version: '', cipher: '', error: '' },
        mqtt: { status: 'connected', client_id: 'monitor-1', subscriptions: ['#', '$SYS/#'], error: '' },
      },
    }
    const fetchMock = installFetchMock({
      ...authenticatedRoutes('diagnostic-token'),
      'GET /api/v1/auth/me': () => jsonResponse({ id: 8, username: 'auditor', disabled: false, role: 'auditor', created_at: '', updated_at: '' }),
      'GET /api/v1/broker/diagnostics': () => jsonResponse(diagnostics),
      'GET /api/v1/broker/diagnostics?severity=warning&limit=17': () => jsonResponse(diagnostics),
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)
    await screen.findByText('Signed in')
    const user = userEvent.setup()
    await user.click(screen.getByRole('link', { name: /broker diagnostics/i }))
    expect(await screen.findByText('Observed traffic')).toBeInTheDocument()
    expect(screen.getByText('MCM events')).toBeInTheDocument()
    expect(screen.getByText('Broker logs')).toBeInTheDocument()
    expect(screen.getByText('Internal metrics')).toBeInTheDocument()
    expect(screen.getByText('Unavailable')).toBeInTheDocument()
    expect(screen.getByText('log file permission denied')).toBeInTheDocument()
    expect(screen.queryByText('No broker log entries in this window.')).not.toBeInTheDocument()
    expect(screen.getByText('7')).toBeInTheDocument()
    expect(screen.getByText('plant/line-1/state')).toBeInTheDocument()
    expect(screen.getByText('Client IDs are not included in observed messages.')).toBeInTheDocument()
    expect(screen.getByText('broker/clients/connected')).toBeInTheDocument()
    expect(screen.getAllByText('3')).toHaveLength(2)
    expect(screen.getByText(/tcp/i).closest('div')).toHaveTextContent('reachable')
    expect(screen.getByText('2.5 ms')).toBeInTheDocument()
    expect(screen.getByText('monitor-1')).toBeInTheDocument()
    await user.selectOptions(screen.getByLabelText('Minimum severity'), 'warning')
    await user.clear(screen.getByLabelText('Entry limit'))
    await user.type(screen.getByLabelText('Entry limit'), '17')
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/broker/diagnostics?severity=warning&limit=17', expect.anything()))
  })

  it('renders broker log entry client IDs only when the broker entry carries one', async () => {
    window.localStorage.setItem('mcm_admin_token', 'structured-logs-token')
    const diagnostics = {
      sources: {
        observed_traffic: { message_count: 0, window_seconds: 300, message_rate_per_minute: 0, top_topics: [], top_clients: [], top_clients_available: false, top_clients_note: 'No client identity in observed traffic.', rate_points: [], persistence: '' },
        mcm_events: { status_events: 0, topic_messages: 0, event_subscribers: 0, last_message_at: null, note: 'MCM events.' },
        broker_logs: { available: true, reason: '', entries: [
          { observed_at: '2026-01-01T00:00:00Z', severity: 'info', kind: 'connected', client_id: 'named-client', topic: 'broker/topic', message: 'Named client entry' },
          { observed_at: '2026-01-01T00:00:01Z', severity: 'debug', kind: 'notice', message: 'Entry without client id' },
        ], severity_counts: { error: 0, warning: 0, info: 1, debug: 1 }, limits: { max_entries: 200, max_entries_per_read: 50, max_message_bytes: 1024, max_age_seconds: 900 }, note: 'Broker-owned logs.' },
        internal_metrics: { endpoint: '/metrics', format: 'prometheus', counters: {}, note: 'MCM metrics.' },
      },
      broker_sys: { available: true, reason: '', metrics: {}, note: '' },
      diagnostics: { tcp: { status: 'reachable', address: 'broker:1883', latency_ms: 1, error: '' }, tls: { status: 'disabled', version: '', cipher: '', error: '' }, mqtt: { status: 'connected', client_id: '', subscriptions: [], error: '' } },
    }
    vi.stubGlobal('fetch', installFetchMock({
      ...authenticatedRoutes('structured-logs-token'),
      'GET /api/v1/auth/me': () => jsonResponse({ id: 8, username: 'auditor', disabled: false, role: 'auditor', created_at: '', updated_at: '' }),
      'GET /api/v1/broker/diagnostics': () => jsonResponse(diagnostics),
    }))
    render(<App />)
    await screen.findByText('Signed in')
    await userEvent.setup().click(screen.getByRole('link', { name: /broker diagnostics/i }))
    expect(await screen.findByText('Named client entry')).toBeInTheDocument()
    expect(screen.getByText('named-client')).toBeInTheDocument()
    expect(screen.getByText('broker/topic')).toBeInTheDocument()
    const untaggedEntry = screen.getByText('Entry without client id').closest('article')
    expect(untaggedEntry).not.toHaveTextContent('Client ID')
    expect(untaggedEntry).not.toHaveTextContent('named-client')
  })

  it('groups MCM events separately from Mosquitto broker-log entries', async () => {
    window.localStorage.setItem('mcm_admin_token', 'logs-token')
    vi.stubGlobal('fetch', installFetchMock(authenticatedRoutes('logs-token')))
    render(<App />)
    await screen.findByText('Signed in')
    const user = userEvent.setup()
    await user.click(screen.getByRole('link', { name: /LogsOperations/i }))
    MockWebSocket.instances[0]?.emit('message', new MessageEvent('message', { data: JSON.stringify({ type: 'broker_log', source: 'mcm', severity: 'info', message: 'MCM line', observed_at: '2026-01-01T00:00:00Z' }) }))
    MockWebSocket.instances[0]?.emit('message', new MessageEvent('message', { data: JSON.stringify({ type: 'broker_log_entry', source: 'mosquitto_log', severity: 'warning', message: 'Broker line', observed_at: '2026-01-01T00:00:01Z' }) }))
    expect(await screen.findByText('MCM line')).toBeInTheDocument()
    expect(screen.getByText('Broker line')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'MCM events and logs' })).toContainElement(screen.getByText('MCM line'))
    expect(screen.getByRole('region', { name: 'Broker logs (Mosquitto)' })).toContainElement(screen.getByText('Broker line'))
  })

  describe('live Logs view (issue #357)', () => {
    async function openLogs() {
      window.localStorage.setItem('mcm_admin_token', 'logs-token')
      vi.stubGlobal('fetch', installFetchMock(authenticatedRoutes('logs-token')))
      render(<App />)
      await screen.findByText('Signed in')
      const user = userEvent.setup()
      await user.click(screen.getByRole('link', { name: /LogsOperations/i }))
      return user
    }

    function emitLog(source: 'mcm' | 'mosquitto_log', severity: string, message: string) {
      MockWebSocket.instances[0]?.emit('message', new MessageEvent('message', { data: JSON.stringify({ type: source === 'mcm' ? 'broker_log' : 'broker_log_entry', source, severity, message, observed_at: '2026-01-01T00:00:00Z' }) }))
    }

    it('keeps the row being read in place and focused while paused', async () => {
      const user = await openLogs()
      emitLog('mcm', 'info', 'Row being read')
      const row = await screen.findByRole('button', { name: /Row being read/ })
      await user.click(screen.getByRole('button', { name: 'Pause' }))
      row.focus()
      emitLog('mcm', 'warning', 'Arrived while paused')
      emitLog('mcm', 'error', 'Also arrived while paused')
      expect(await screen.findByRole('status', { name: 'Live feed state' })).toHaveTextContent('Paused · 2 new events since pausing')
      expect(document.activeElement).toBe(row)
      expect(row).toHaveTextContent('Row being read')
      expect(row).toBeInTheDocument()
      expect(screen.queryByText('Arrived while paused')).not.toBeInTheDocument()
    })

    it('filters, expands an event and clearly returns to live', async () => {
      const user = await openLogs()
      emitLog('mcm', 'info', 'Routine sync')
      emitLog('mosquitto_log', 'error', 'Socket error on client-7')
      await screen.findByText('Socket error on client-7')

      await user.click(within(screen.getByRole('group', { name: 'Severity' })).getByRole('button', { name: 'error' }))
      expect(screen.queryByText('Routine sync')).not.toBeInTheDocument()
      expect(screen.getByRole('region', { name: 'MCM events and logs' })).toHaveTextContent('No events match the filters.')
      await user.type(screen.getByRole('searchbox', { name: 'Search messages' }), 'client-7')
      expect(screen.getByText('Socket error on client-7')).toBeInTheDocument()

      const row = screen.getByRole('button', { name: /Socket error on client-7/ })
      expect(row).toHaveAttribute('aria-expanded', 'false')
      await user.click(row)
      expect(row).toHaveAttribute('aria-expanded', 'true')
      const detail = screen.getByRole('region', { name: 'Event detail' })
      expect(detail).toHaveTextContent('Broker logs (Mosquitto)')
      expect(detail).toHaveTextContent('2026-01-01T00:00:00Z')
      expect(detail).toHaveTextContent('Socket error on client-7')

      await user.click(screen.getByRole('button', { name: 'Pause' }))
      emitLog('mosquitto_log', 'error', 'Second socket error on client-7')
      await user.click(screen.getByRole('button', { name: 'Resume live' }))
      expect(screen.getByRole('status', { name: 'Live feed state' })).toHaveTextContent('Back to live · 1 event arrived while paused; the live window now starts with the newest events.')
      expect(screen.getByText('Second socket error on client-7')).toBeInTheDocument()

      await user.click(screen.getByRole('button', { name: 'Clear filters' }))
      expect(screen.getByText('Routine sync')).toBeInTheDocument()
    })

    it('states the live window limit and tells no activity from a lost stream', async () => {
      await openLogs()
      expect(screen.getByText(/the newest 100 events received while this page is open/)).toBeInTheDocument()
      expect(screen.getByText(/no history to page through/)).toBeInTheDocument()
      await waitFor(() => expect(screen.getByRole('region', { name: 'MCM events and logs' })).toHaveTextContent('Connected · no events received yet.'))
      MockWebSocket.instances[0]?.emit('close', new Event('close'))
      expect(await screen.findByText('Not connected to the live event stream (reconnecting). New events will not arrive until it reconnects.')).toBeInTheDocument()
      expect(screen.getByRole('region', { name: 'MCM events and logs' })).toHaveTextContent('No events: the live stream is reconnecting.')
    })

    it('does not call a paused, empty window idle while events keep arriving', async () => {
      const user = await openLogs()
      await waitFor(() => expect(screen.getByRole('region', { name: 'MCM events and logs' })).toHaveTextContent('Connected · no events received yet.'))
      await user.click(screen.getByRole('button', { name: 'Pause' }))
      emitLog('mcm', 'info', 'Arrived during the pause')
      expect(await screen.findByRole('status', { name: 'Live feed state' })).toHaveTextContent('Paused · 1 new event since pausing')
      expect(screen.getByRole('region', { name: 'MCM events and logs' })).toHaveTextContent('Paused before any events arrived. Resume live to see them.')
    })

    it('only calls a paused, empty source region paused when that source has new events', async () => {
      const user = await openLogs()
      await waitFor(() => expect(screen.getByRole('region', { name: 'MCM events and logs' })).toHaveTextContent('Connected · no events received yet.'))
      await user.click(screen.getByRole('button', { name: 'Pause' }))
      emitLog('mosquitto_log', 'info', 'Broker line during the pause')
      expect(await screen.findByRole('region', { name: 'Broker logs (Mosquitto)' })).toHaveTextContent('Paused before any events arrived. Resume live to see them.')
      expect(screen.getByRole('region', { name: 'MCM events and logs' })).toHaveTextContent('Connected · no events received yet.')
    })

    it('says when more events arrived while paused than the live window keeps, before and after resuming', async () => {
      const user = await openLogs()
      emitLog('mcm', 'info', 'Row being read')
      await screen.findByRole('button', { name: /Row being read/ })
      await user.click(screen.getByRole('button', { name: 'Pause' }))
      for (let index = 1; index <= 101; index += 1) emitLog('mcm', 'info', `Paused arrival ${index}`)
      expect(await screen.findByRole('status', { name: 'Live feed state' })).toHaveTextContent('Paused · 101 new events since pausing. More arrived than the live window keeps, so the oldest of them will not be shown.')

      await user.click(screen.getByRole('button', { name: 'Resume live' }))
      expect(screen.getByRole('status', { name: 'Live feed state' })).toHaveTextContent('Back to live · 101 events arrived while paused; only the newest 100 are kept, so the oldest of them were dropped.')
      expect(screen.getByText('Paused arrival 101')).toBeInTheDocument()
      expect(screen.queryByText('Paused arrival 1')).not.toBeInTheDocument()
      expect(screen.queryByText('Row being read')).not.toBeInTheDocument()
    })

    it('shows only the selected source', async () => {
      const user = await openLogs()
      emitLog('mcm', 'info', 'MCM only line')
      emitLog('mosquitto_log', 'info', 'Broker only line')
      await screen.findByText('Broker only line')
      await user.selectOptions(screen.getByRole('combobox', { name: 'Source' }), 'mosquitto_log')
      expect(screen.queryByRole('region', { name: 'MCM events and logs' })).not.toBeInTheDocument()
      expect(screen.getByText('Broker only line')).toBeInTheDocument()
    })
  })

  it('renders populated dashboard traffic widgets and latest broker activity', async () => {
    window.localStorage.setItem('mcm_admin_token', 'dashboard-token')

    const fetchMock = installFetchMock({
      'GET /api/v1/auth/me': () =>
        jsonResponse({
          id: 8,
          username: 'dashboard-operator',
          disabled: false,
          role: 'operator',
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-01T00:00:00Z',
        }),
      'GET /api/v1/status': () =>
        jsonResponse({
          broker: {
            status: 'connected',
            metrics: {
              traffic: {
                snapshot_at: '2026-01-01T00:01:00Z',
                window_seconds: 300,
                message_count: 6,
                message_rate_per_minute: 1.2,
                rate_points: [
                  { timestamp: '2026-01-01T00:00:00Z', count: 2 },
                  { timestamp: '2026-01-01T00:01:00Z', count: 4 },
                ],
                top_topics: [{ name: 'factory/line-1/temperature', count: 4, percentage: 67 }],
                top_clients: [{ name: 'edge-client-01', count: 6, percentage: 100 }],
                top_clients_available: true,
                top_clients_note: '',
                persistence: 'persisted broker metric events are used when available',
              },
            },
          },
        }),
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)

    await screen.findByText('Signed in')
    expect(await screen.findByText('1.2')).toBeInTheDocument()
    expect(screen.getByLabelText(/4 messages at/i)).toBeInTheDocument()
    expect(screen.getByText('edge-client-01')).toBeInTheDocument()
    expect(screen.getByText('persisted broker metric events are used when available')).toBeInTheDocument()

    MockWebSocket.instances[0]?.emit(
      'message',
      new MessageEvent('message', {
        data: JSON.stringify({
          type: 'topic_message',
          source: 'observed',
          topic: 'factory/line-1/temperature',
          payload_preview: '{"temperature":21.5}',
          payload_format: 'json',
          payload_bytes: 20,
          observed_at: '2026-01-01T00:02:00Z',
        }),
      }),
    )

    expect(await screen.findByText('{"temperature":21.5}')).toBeInTheDocument()
    expect(screen.getByText('7 messages in the last 5 minutes.')).toBeInTheDocument()
    expect(screen.getByText('json')).toBeInTheDocument()
    expect(screen.getByText('20')).toBeInTheDocument()
  })

  it('restores an operator session and loads the MQTT users view', async () => {
    window.localStorage.setItem('mcm_admin_token', 'restored-token')

    const fetchMock = installFetchMock({
      ...authenticatedRoutes('restored-token'),
      'GET /api/v1/mqtt-users': (init) => {
        expect(init?.headers).toEqual(expect.objectContaining({ Authorization: 'Bearer restored-token' }))

        return envelope('users', [
          {
            id: 42,
            username: 'device-sensor-01',
            disabled: false,
            created_at: '2026-01-01T00:00:00Z',
            updated_at: '2026-01-01T00:00:00Z',
          },
        ])
      },
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)

    await screen.findByText('Signed in')
    const user = userEvent.setup()
    await user.click(screen.getByRole('link', { name: /users/i }))

    expect(window.location.pathname).toBe('/users')
    expect(screen.getByRole('link', { name: /users/i })).toHaveAttribute('aria-current', 'page')
    await screen.findByText('device-sensor-01')

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/mqtt-users?limit=25&offset=0',
        expect.objectContaining({
          headers: expect.objectContaining({ Authorization: 'Bearer restored-token' }),
        }),
      )
    })
  })

  it('completes the MQTT onboarding journey and clears the pending deploy banner', async () => {
    window.localStorage.setItem('mcm_admin_token', 'journey-token')

    vi.stubGlobal('fetch', installFetchMock({
      'GET /api/v1/auth/me': () =>
        jsonResponse({
          id: 9,
          username: 'journey-admin',
          disabled: false,
          role: 'admin',
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-01T00:00:00Z',
        }),
      'GET /api/v1/status': () =>
        jsonResponse({ broker: { status: 'connected', metrics: { traffic: { window_seconds: 300, message_count: 0, message_rate_per_minute: 0, rate_points: [], top_topics: [], top_clients: [], top_clients_available: false, persistence: 'In-memory' } } } }),
      'GET /api/v1/mqtt-users': () => envelope('users', []),
      'POST /api/v1/mqtt-users': () => jsonResponse({ id: 7, username: 'probe', password: 'generated-secret' }),
      'POST /api/v1/acls': () => jsonResponse({ id: 'rule-1', principal: 'probe' }),
      'POST /api/v1/deployments/preview': () =>
        jsonResponse({ revision_id: 'rev-1', acl_diff: '+user probe\n', passwd_diff: '+probe:$7$generated-secret\n', has_changes: true }),
      'POST /api/v1/deployments/apply': () =>
        jsonResponse({ id: 11, status: 'active_verified', message: 'activation probe: user grant', created_at: '2026-01-01T00:00:00Z' }),
    }))

    render(<App />)

    await screen.findByText('Signed in')
    const user = userEvent.setup()
    await user.click(screen.getByRole('link', { name: /users/i }))

    await user.click(await screen.findByRole('button', { name: 'Add User' }))
    await user.type(screen.getByPlaceholderText('device-sensor-01'), 'probe')
    await user.click(screen.getByRole('button', { name: 'Create user' }))

    // The saved user is not applied yet, so the pending deploy banner covers it.
    expect(await screen.findByText('Pending deploy')).toBeInTheDocument()

    await user.type(screen.getByPlaceholderText('sensors/# or device/+/status'), 'probe/allowed')
    await user.click(screen.getByRole('button', { name: 'Save access rule' }))
    await user.click(await screen.findByRole('button', { name: 'Preview changes' }))
    await user.type(await screen.findByPlaceholderText('Authenticator code'), '123456')
    await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
    await screen.findByText(/MFA confirmed for this session/)
    await user.click(screen.getByRole('button', { name: 'Apply and verify' }))

    expect(await screen.findByText('Applied and verified')).toBeInTheDocument()
    await waitFor(() => {
      expect(screen.queryByText('Pending deploy')).not.toBeInTheDocument()
    })
  })

  it('logs out when the initial authenticated status request is unauthorized', async () => {
    window.localStorage.setItem('mcm_admin_token', 'expired-status-token')

    const fetchMock = installFetchMock({
      'GET /api/v1/auth/me': () =>
        jsonResponse({
          id: 10,
          username: 'status-operator',
          disabled: false,
          role: 'operator',
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-01T00:00:00Z',
        }),
      'GET /api/v1/status': (init) => {
        expect(init?.headers).toEqual(expect.objectContaining({ Authorization: 'Bearer expired-status-token' }))

        return new Response(null, { status: 401 })
      },
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)

    await screen.findByRole('heading', { name: 'Sign in' })
    expect(window.localStorage.getItem('mcm_admin_token')).toBeNull()
  })

  it('logs out when the ACL panel receives an unauthorized response', async () => {
    window.localStorage.setItem('mcm_admin_token', 'expired-acl-token')

    const fetchMock = installFetchMock({
      ...authenticatedRoutes('expired-acl-token'),
      'GET /api/v1/acls': (init) => {
        expect(init?.headers).toEqual(expect.objectContaining({ Authorization: 'Bearer expired-acl-token' }))

        return new Response(null, { status: 401 })
      },
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)

    await screen.findByText('Signed in')
    const user = userEvent.setup()
    await user.click(screen.getByRole('link', { name: /acls/i }))

    await screen.findByRole('heading', { name: 'Sign in' })
    expect(window.localStorage.getItem('mcm_admin_token')).toBeNull()
  })

  it('updates the topic explorer when a broker event arrives without a page refresh', async () => {
    window.localStorage.setItem('mcm_admin_token', 'topic-live-token')

    const fetchMock = installFetchMock({
      ...authenticatedRoutes('topic-live-token'),
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)

    await screen.findByText('Signed in')
    const user = userEvent.setup()
    await user.click(screen.getByRole('link', { name: /topics/i }))

    expect(await screen.findByText('Connected · no messages received yet.')).toBeInTheDocument()
    MockWebSocket.instances[0]?.emit(
      'message',
      new MessageEvent('message', {
        data: JSON.stringify({
          type: 'topic_message',
          source: 'observed',
          topic: 'factory/line-1/temperature',
          payload_preview: '{"temperature":21.5}',
          payload_format: 'json',
          payload_bytes: 20,
          observed_at: '2026-01-01T00:00:00Z',
        }),
      }),
    )

    expect(await screen.findAllByText('factory/line-1/temperature')).toHaveLength(2)
    expect(window.location.pathname).toBe('/topics')
  })

  describe('live Topics view (issue #357)', () => {
    async function openTopics() {
      window.localStorage.setItem('mcm_admin_token', 'topics-live-token')
      vi.stubGlobal('fetch', installFetchMock(authenticatedRoutes('topics-live-token')))
      render(<App />)
      await screen.findByText('Signed in')
      const user = userEvent.setup()
      await user.click(screen.getByRole('link', { name: /topics/i }))
      return user
    }

    function emitTopic(topic: string, payload: string) {
      MockWebSocket.instances[0]?.emit('message', new MessageEvent('message', { data: JSON.stringify({ type: 'topic_message', source: 'observed', topic, payload_preview: payload, payload_format: 'json', payload_bytes: payload.length, observed_at: '2026-01-01T00:00:00Z' }) }))
    }

    it('keeps the row being read in place and focused while paused', async () => {
      const user = await openTopics()
      emitTopic('plant/a/row-being-read', '{"t":1}')
      const row = await screen.findByRole('button', { name: /plant\/a\/row-being-read/ })
      await user.click(screen.getByRole('button', { name: 'Pause' }))
      row.focus()
      emitTopic('plant/b/arrived-while-paused', '{"t":2}')
      emitTopic('plant/c/also-arrived', '{"t":3}')
      expect(await screen.findByRole('status', { name: 'Live feed state' })).toHaveTextContent('Paused · 2 new messages since pausing')
      expect(document.activeElement).toBe(row)
      expect(row).toBeInTheDocument()
      expect(row).toHaveTextContent('plant/a/row-being-read')
      expect(screen.queryByText('plant/b/arrived-while-paused')).not.toBeInTheDocument()
      expect(screen.getByRole('region', { name: 'Message detail' })).toHaveTextContent('plant/a/row-being-read')
    })

    it('does not call a paused, empty window idle while messages keep arriving', async () => {
      const user = await openTopics()
      await waitFor(() => expect(screen.getByRole('region', { name: 'Topic messages' })).toHaveTextContent('Connected · no messages received yet.'))
      await user.click(screen.getByRole('button', { name: 'Pause' }))
      emitTopic('plant/a/during-pause', '{}')
      expect(await screen.findByRole('status', { name: 'Live feed state' })).toHaveTextContent('Paused · 1 new message since pausing')
      expect(screen.getByRole('region', { name: 'Topic messages' })).toHaveTextContent('Paused before any messages arrived. Resume live to see them.')
    })

    it('filters with an MQTT wildcard, shows the selected payload and clearly returns to live', async () => {
      const user = await openTopics()
      emitTopic('factory/line-1/temperature', '{"temperature":21.5}')
      emitTopic('factory/line-2/temperature', '{"temperature":30}')
      emitTopic('office/hvac/status', '"on"')
      await screen.findByRole('button', { name: /office\/hvac\/status/ })

      const filter = screen.getByRole('searchbox', { name: 'Filter topics' })
      await user.type(filter, 'factory/+/temperature')
      expect(screen.queryByRole('button', { name: /office\/hvac\/status/ })).not.toBeInTheDocument()
      expect(screen.getAllByRole('button', { name: /factory\/line-\d\/temperature/ })).toHaveLength(2)

      const row = screen.getByRole('button', { name: /factory\/line-1\/temperature/ })
      expect(row).toHaveAttribute('aria-pressed', 'false')
      await user.click(row)
      expect(row).toHaveAttribute('aria-pressed', 'true')
      const detail = screen.getByRole('region', { name: 'Message detail' })
      expect(detail).toHaveTextContent('factory/line-1/temperature')
      expect(detail).toHaveTextContent('{"temperature":21.5}')

      await user.clear(filter)
      await user.type(filter, 'HVAC')
      expect(screen.getByRole('button', { name: /office\/hvac\/status/ })).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: /factory/ })).not.toBeInTheDocument()
      expect(screen.getByRole('region', { name: 'Message detail' })).toHaveTextContent('office/hvac/status')
      expect(screen.getByText('The selected message is hidden by the filter; showing the newest match.')).toBeInTheDocument()

      await user.clear(filter)
      await user.type(filter, 'nothing/#')
      expect(screen.getByRole('region', { name: 'Topic messages' })).toHaveTextContent('No messages match the filter.')

      await user.click(screen.getByRole('button', { name: 'Clear filter' }))
      expect(screen.getByRole('region', { name: 'Message detail' })).toHaveTextContent('{"temperature":21.5}')

      await user.click(screen.getByRole('button', { name: 'Pause' }))
      emitTopic('factory/line-3/temperature', '{"temperature":18}')
      await user.click(screen.getByRole('button', { name: 'Resume live' }))
      expect(screen.getByRole('status', { name: 'Live feed state' })).toHaveTextContent('Back to live · 1 message arrived while paused; the live window now starts with the newest messages.')
      expect(screen.getByRole('button', { name: /factory\/line-3\/temperature/ })).toBeInTheDocument()
      expect(screen.getByRole('region', { name: 'Message detail' })).toHaveTextContent('factory/line-1/temperature')
    })

    it('falls back to the newest message and says so when the selected one leaves the live window', async () => {
      const user = await openTopics()
      emitTopic('window/oldest', '"first"')
      await user.click(await screen.findByRole('button', { name: /window\/oldest/ }))
      for (let index = 1; index <= 20; index += 1) emitTopic(`window/newer-${index}`, `"${index}"`)
      await screen.findByRole('button', { name: /window\/newer-20/ })
      expect(screen.queryByRole('button', { name: /window\/oldest/ })).not.toBeInTheDocument()
      expect(screen.getByRole('region', { name: 'Message detail' })).toHaveTextContent('window/newer-20')
      expect(screen.getByText('The selected message left the live window; showing the newest message.')).toBeInTheDocument()
    })

    it('states the live window limit and tells no activity from a lost stream', async () => {
      await openTopics()
      expect(screen.getByText(/the newest 20 messages received while this page is open/)).toBeInTheDocument()
      expect(screen.getByText(/no history to page through/)).toBeInTheDocument()
      await waitFor(() => expect(screen.getByRole('region', { name: 'Topic messages' })).toHaveTextContent('Connected · no messages received yet.'))
      MockWebSocket.instances[0]?.emit('close', new Event('close'))
      expect(await screen.findByText('Not connected to the live event stream (reconnecting). New messages will not arrive until it reconnects.')).toBeInTheDocument()
      expect(screen.getByRole('region', { name: 'Topic messages' })).toHaveTextContent('No messages: the live stream is reconnecting.')
    })

    it('shows the newest message by default without marking its row as pressed', async () => {
      const user = await openTopics()
      emitTopic('plant/a/older', '"older"')
      emitTopic('plant/b/newest', '"newest"')
      const newest = await screen.findByRole('button', { name: /plant\/b\/newest/ })
      const older = screen.getByRole('button', { name: /plant\/a\/older/ })
      const detail = screen.getByRole('region', { name: 'Message detail' })
      expect(detail).toHaveTextContent('Newest message')
      expect(detail).toHaveTextContent('plant/b/newest')
      expect(newest).toHaveAttribute('aria-pressed', 'false')
      expect(older).toHaveAttribute('aria-pressed', 'false')

      await user.click(older)
      expect(detail).toHaveTextContent('Selected message')
      expect(detail).toHaveTextContent('plant/a/older')
      expect(older).toHaveAttribute('aria-pressed', 'true')
      expect(newest).toHaveAttribute('aria-pressed', 'false')
    })

    it('keeps the chosen message in the detail while paused', async () => {
      const user = await openTopics()
      emitTopic('plant/a/chosen', '"chosen"')
      emitTopic('plant/b/newer', '"newer"')
      await user.click(await screen.findByRole('button', { name: /plant\/a\/chosen/ }))
      await user.click(screen.getByRole('button', { name: 'Pause' }))
      emitTopic('plant/c/during-pause', '"later"')
      expect(await screen.findByRole('status', { name: 'Live feed state' })).toHaveTextContent('Paused · 1 new message since pausing')
      const detail = screen.getByRole('region', { name: 'Message detail' })
      expect(detail).toHaveTextContent('Selected message')
      expect(detail).toHaveTextContent('plant/a/chosen')
      expect(detail).toHaveTextContent('"chosen"')
      expect(screen.queryByText('plant/c/during-pause')).not.toBeInTheDocument()
    })

    it('explains an invalid wildcard filter and clears the filter on request', async () => {
      const user = await openTopics()
      emitTopic('factory/line-1/temperature', '1')
      emitTopic('office/hvac/status', '2')
      await screen.findByRole('button', { name: /office\/hvac\/status/ })
      const filter = screen.getByRole('searchbox', { name: 'Filter topics' })
      const hint = 'Not a valid MQTT topic filter: + and # must fill a whole level, and # must be the last level.'
      expect(screen.queryByText(hint)).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Clear filter' })).not.toBeInTheDocument()

      await user.type(filter, 'factory/line+/#')
      expect(screen.getByText(hint)).toBeInTheDocument()
      expect(screen.getByRole('region', { name: 'Topic messages' })).toHaveTextContent('No messages match the filter.')

      await user.click(screen.getByRole('button', { name: 'Clear filter' }))
      expect(filter).toHaveValue('')
      expect(screen.queryByText(hint)).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Clear filter' })).not.toBeInTheDocument()
      expect(screen.getByRole('button', { name: /factory\/line-1\/temperature/ })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: /office\/hvac\/status/ })).toBeInTheDocument()
    })

    it('tells a filter without matches apart from no activity', async () => {
      const user = await openTopics()
      emitTopic('plant/a/temp', '1')
      await screen.findByRole('button', { name: /plant\/a\/temp/ })
      await user.type(screen.getByRole('searchbox', { name: 'Filter topics' }), 'zzz')
      expect(screen.getByRole('region', { name: 'Topic messages' })).toHaveTextContent('No messages match the filter.')
      expect(screen.getByRole('region', { name: 'Topic messages' })).not.toHaveTextContent('no messages received yet')
    })
  })

  it('restores an operator session and loads the ACL policy workspace', async () => {
    window.localStorage.setItem('mcm_admin_token', 'acl-token')

    const fetchMock = installFetchMock({
      ...authenticatedRoutes('acl-token'),
      'GET /api/v1/acls': (init) => {
        expect(init?.headers).toEqual(expect.objectContaining({ Authorization: 'Bearer acl-token' }))

        return envelope('rules', [
          {
            id: 'rule-1',
            principal: 'device-writer',
            topic_filter: 'factory/line-1/#',
            permission: 'write',
            description: 'Line 1 telemetry writer',
          },
        ])
      },
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)

    await screen.findByText('Signed in')
    const user = userEvent.setup()
    await user.click(screen.getByRole('link', { name: /acls/i }))

    await screen.findByRole('heading', { name: 'ACL policy workspace' })
    expect(await screen.findByText('device-writer')).toBeInTheDocument()
    expect(screen.getByText('factory/line-1/#')).toBeInTheDocument()
    expect(screen.getByText('write')).toBeInTheDocument()

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/acls?limit=25&offset=0',
        expect.objectContaining({
          headers: expect.objectContaining({ Authorization: 'Bearer acl-token' }),
        }),
      )
    })
  })

  it('shows the deploy not-configured state when deployment APIs are unavailable', async () => {
    window.localStorage.setItem('mcm_admin_token', 'deploy-token')

    const fetchMock = installFetchMock({
      ...authenticatedRoutes('deploy-token'),
      'GET /api/v1/deployments': (init) => {
        expect(init?.headers).toEqual(expect.objectContaining({ Authorization: 'Bearer deploy-token' }))

        return jsonResponse({ error: 'deploy service not configured' }, { status: 422 })
      },
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)

    await screen.findByText('Signed in')
    const user = userEvent.setup()
    await user.click(screen.getByRole('link', { name: /deploy/i }))

    await screen.findByRole('heading', { name: 'Mosquitto configuration deploy' })
    expect(await screen.findByText('Not configured')).toBeInTheDocument()
    expect(screen.getByText('Deploy functionality is not configured on this MCM instance.')).toBeInTheDocument()
  })

  it('logs out when the deploy panel receives an unauthorized response', async () => {
    window.localStorage.setItem('mcm_admin_token', 'expired-deploy-token')

    const fetchMock = installFetchMock({
      ...authenticatedRoutes('expired-deploy-token'),
      'GET /api/v1/deployments': (init) => {
        expect(init?.headers).toEqual(expect.objectContaining({ Authorization: 'Bearer expired-deploy-token' }))

        return new Response(null, { status: 401 })
      },
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)

    await screen.findByText('Signed in')
    const user = userEvent.setup()
    await user.click(screen.getByRole('link', { name: /deploy/i }))

    await screen.findByRole('heading', { name: 'Sign in' })
    expect(window.localStorage.getItem('mcm_admin_token')).toBeNull()
  })

  it('logs out when the MQTT users panel receives an unauthorized response', async () => {
    window.localStorage.setItem('mcm_admin_token', 'expired-users-token')

    const fetchMock = installFetchMock({
      ...authenticatedRoutes('expired-users-token'),
      'GET /api/v1/mqtt-users': (init) => {
        expect(init?.headers).toEqual(expect.objectContaining({ Authorization: 'Bearer expired-users-token' }))

        return new Response(null, { status: 401 })
      },
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)

    await screen.findByText('Signed in')
    const user = userEvent.setup()
    await user.click(screen.getByRole('link', { name: /users/i }))

    await screen.findByRole('heading', { name: 'Sign in' })
    expect(window.localStorage.getItem('mcm_admin_token')).toBeNull()
  })

  it('opens the routed topics page on a direct browser load', async () => {
    window.localStorage.setItem('mcm_admin_token', 'topics-token')
    window.history.replaceState(null, '', '/topics')

    const fetchMock = installFetchMock({
      ...authenticatedRoutes('topics-token'),
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)

    await screen.findByRole('heading', { name: 'Topic explorer' })
    expect(window.location.pathname).toBe('/topics')
    expect(screen.getByRole('link', { name: /topics/i })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: /dashboard/i })).toHaveAttribute('href', '/dashboard')
  })

  it('navigates across ACL and deploy panels in an authenticated session', async () => {
    window.localStorage.setItem('mcm_admin_token', 'multi-panel-token')

    const fetchMock = installFetchMock({
      ...authenticatedRoutes('multi-panel-token'),
      'GET /api/v1/acls': () =>
        envelope('rules', [
          {
            id: 'rule-2',
            principal: 'analytics-reader',
            topic_filter: 'analytics/+/state',
            permission: 'read',
          },
        ]),
      'GET /api/v1/deployments': () =>
        envelope('deployments', [
          {
            id: 'dep-2026-01-01',
            status: 'applied',
            message: 'Configuration applied successfully.',
            created_at: '2026-01-01T00:00:00Z',
          },
        ]),
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)

    await screen.findByText('Signed in')
    const user = userEvent.setup()
    await user.click(screen.getByRole('link', { name: /acls/i }))
    expect(await screen.findByText('analytics-reader')).toBeInTheDocument()

    await user.click(screen.getByRole('link', { name: /deploy/i }))
    expect(window.location.pathname).toBe('/deploy')
    expect(await screen.findByText('Configuration preview')).toBeInTheDocument()
    expect(await screen.findByText('Configuration applied successfully.')).toBeInTheDocument()

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/acls?limit=25&offset=0',
        expect.objectContaining({
          headers: expect.objectContaining({ Authorization: 'Bearer multi-panel-token' }),
        }),
      )
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/deployments?limit=25&offset=0',
        expect.objectContaining({
          headers: expect.objectContaining({ Authorization: 'Bearer multi-panel-token' }),
        }),
      )
    })
  })

  describe('paged searchable listings (issue #356)', () => {
    type Rule = { id: string; principal: string; topic_filter: string; permission: string }
    type Dep = { id: string; status: string; message: string; created_at: string }

    const rules: Rule[] = [
      { id: 'r1', principal: 'sensor-a', topic_filter: 'plant/a/#', permission: 'read' },
      { id: 'r2', principal: 'sensor-b', topic_filter: 'plant/b/#', permission: 'write' },
      { id: 'r3', principal: 'gateway', topic_filter: 'plant/a/cmd', permission: 'readwrite' },
    ]
    const deployments: Dep[] = [
      { id: 'dep-1', status: 'active_verified', message: 'Verified deploy', created_at: '2026-01-02T00:00:00Z' },
      { id: 'dep-2', status: 'rolled_back', message: 'Rolled back deploy', created_at: '2026-01-01T00:00:00Z' },
    ]
    const users = Array.from({ length: 60 }, (_, i) => ({ id: i + 1, username: `device-${String(i + 1).padStart(2, '0')}`, disabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }))

    function listingRoutes(token: string, data: { rules?: Rule[]; deployments?: Dep[] } = {}) {
      return {
        ...authenticatedRoutes(token),
        'GET /api/v1/mqtt-users': pagedRoute('users', users, (row, params) => includesParam(row.username, params, 'q')),
        'GET /api/v1/acls': pagedRoute('rules', data.rules ?? rules, (row, params) => includesParam(row.principal, params, 'user') && includesParam(row.topic_filter, params, 'topic')),
        'GET /api/v1/deployments': pagedRoute('deployments', data.deployments ?? deployments, (row, params) => !params.get('status') || row.status === params.get('status')),
      } satisfies Record<string, RouteHandler>
    }

    function urls(fetchMock: ReturnType<typeof installFetchMock>, path: string) {
      return fetchMock.mock.calls.map(([input]) => String(input)).filter((url) => url.startsWith(`${path}?`))
    }

    async function openPanel(token: string, link: RegExp, data?: Parameters<typeof listingRoutes>[1]) {
      window.localStorage.setItem('mcm_admin_token', token)
      const fetchMock = installFetchMock(listingRoutes(token, data))
      vi.stubGlobal('fetch', fetchMock)
      render(<App />)
      await screen.findByText('Signed in')
      const user = userEvent.setup()
      await user.click(screen.getByRole('link', { name: link }))
      return { user, fetchMock }
    }

    it('filters the ACL rules by user and by topic through the server (S7)', async () => {
      const { user, fetchMock } = await openPanel('acl-filter-token', /acls/i)
      expect(await screen.findByText('gateway')).toBeInTheDocument()
      expect(screen.getByText('3 rules')).toBeInTheDocument()

      await user.type(screen.getByRole('textbox', { name: 'Filter by user' }), 'sensor')
      await waitFor(() => expect(screen.queryByText('gateway')).not.toBeInTheDocument())
      expect(screen.getByText('sensor-a')).toBeInTheDocument()
      expect(screen.getByText('sensor-b')).toBeInTheDocument()
      expect(urls(fetchMock, '/api/v1/acls')).toContain('/api/v1/acls?user=sensor&limit=25&offset=0')

      await user.type(screen.getByRole('textbox', { name: 'Filter by topic' }), 'plant/a')
      await waitFor(() => expect(screen.queryByText('sensor-b')).not.toBeInTheDocument())
      expect(screen.getByText('sensor-a')).toBeInTheDocument()
      expect(screen.getByText('1 rule')).toBeInTheDocument()
      expect(urls(fetchMock, '/api/v1/acls')).toContain('/api/v1/acls?user=sensor&topic=plant%2Fa&limit=25&offset=0')
    })

    it('tells no ACL rules apart from no matching ACL rule (S16)', async () => {
      const { user } = await openPanel('acl-empty-token', /acls/i, { rules: [] })
      expect(await screen.findByText('No ACL rules configured yet.')).toBeInTheDocument()
      expect(screen.queryByText('No ACL rules match the filters.')).not.toBeInTheDocument()

      await user.type(screen.getByRole('textbox', { name: 'Filter by topic' }), 'x')
      expect(await screen.findByText('No ACL rules match the filters.')).toBeInTheDocument()
      expect(screen.queryByText('No ACL rules configured yet.')).not.toBeInTheDocument()
    })

    it('filters the deployment history by status through the server (S7)', async () => {
      const { user, fetchMock } = await openPanel('deploy-filter-token', /deploy/i)
      expect(await screen.findByText('Verified deploy')).toBeInTheDocument()
      expect(screen.getByText('Rolled back deploy')).toBeInTheDocument()
      expect(screen.getByText('2 deployments')).toBeInTheDocument()

      await user.selectOptions(screen.getByRole('combobox', { name: 'Filter by status' }), 'rolled_back')
      await waitFor(() => expect(screen.queryByText('Verified deploy')).not.toBeInTheDocument())
      expect(screen.getByText('Rolled back deploy')).toBeInTheDocument()
      expect(screen.getByText('1 deployment')).toBeInTheDocument()
      expect(urls(fetchMock, '/api/v1/deployments')).toContain('/api/v1/deployments?status=rolled_back&limit=25&offset=0')

      await user.selectOptions(screen.getByRole('combobox', { name: 'Filter by status' }), 'failed')
      expect(await screen.findByText('No deployments match the filter.')).toBeInTheDocument()
      expect(screen.queryByText('No deployments recorded yet.')).not.toBeInTheDocument()
    })

    it('tells no deployments apart from no matching deployment (S16)', async () => {
      await openPanel('deploy-empty-token', /deploy/i, { deployments: [] })
      expect(await screen.findByText('No deployments recorded yet.')).toBeInTheDocument()
      expect(screen.queryByText('No deployments match the filter.')).not.toBeInTheDocument()
    })

    it('restores the search, filters and page of each listing when coming back to it (S14)', async () => {
      const { user, fetchMock } = await openPanel('round-trip-token', /^MQTT users/)
      expect(await screen.findByText('device-01')).toBeInTheDocument()
      await user.type(screen.getByRole('searchbox', { name: 'Search users' }), 'device')
      await waitFor(() => expect(window.location.search).toBe('?users.q=device'))
      await user.click(screen.getByRole('button', { name: 'Next page' }))
      expect(await screen.findByText('device-26')).toBeInTheDocument()
      expect(window.location.search).toBe('?users.q=device&users.page=2')

      await user.click(screen.getByRole('link', { name: /acls/i }))
      expect(await screen.findByText('gateway')).toBeInTheDocument()
      // The users context does not leak into another panel's URL.
      expect(window.location.search).toBe('')
      await user.type(screen.getByRole('textbox', { name: 'Filter by user' }), 'sensor')
      await waitFor(() => expect(window.location.search).toBe('?acl.user=sensor'))

      // Logs keeps opening unfiltered from the sidebar.
      await user.click(screen.getByRole('link', { name: /^Logs/ }))
      expect(window.location.pathname).toBe('/logs')
      expect(window.location.search).toBe('')

      await user.click(screen.getByRole('link', { name: /^MQTT users/ }))
      expect(window.location.pathname).toBe('/users')
      expect(window.location.search).toBe('?users.q=device&users.page=2')
      expect(await screen.findByText('device-26')).toBeInTheDocument()
      expect(screen.getByRole('searchbox', { name: 'Search users' })).toHaveValue('device')
      expect(screen.getByText('Page 2 of 3')).toBeInTheDocument()
      expect(urls(fetchMock, '/api/v1/mqtt-users').at(-1)).toBe('/api/v1/mqtt-users?q=device&limit=25&offset=25')

      await user.click(screen.getByRole('link', { name: /acls/i }))
      expect(window.location.search).toBe('?acl.user=sensor')
      expect(await screen.findByText('sensor-a')).toBeInTheDocument()
      expect(screen.queryByText('gateway')).not.toBeInTheDocument()
      expect(screen.getByRole('textbox', { name: 'Filter by user' })).toHaveValue('sensor')

      // The browser back button returns to the listing with its context too.
      window.history.back()
      await waitFor(() => expect(window.location.pathname).toBe('/users'))
      expect(window.location.search).toBe('?users.q=device&users.page=2')
      expect(await screen.findByText('device-26')).toBeInTheDocument()
    })
  })

  describe('role-aware ACL panel', () => {
    function aclRoutes(_token: string, role: string): Record<string, RouteHandler> {
      return {
        'GET /api/v1/auth/me': () =>
          jsonResponse({
            id: 1,
            username: 'test-user',
            disabled: false,
            role,
            created_at: '2026-01-01T00:00:00Z',
            updated_at: '2026-01-01T00:00:00Z',
          }),
        'GET /api/v1/status': () =>
          jsonResponse({
            broker: {
              status: 'connected',
              metrics: {
                traffic: {
                  window_seconds: 300,
                  message_count: 0,
                  message_rate_per_minute: 0,
                  rate_points: [],
                  top_topics: [],
                  top_clients: [],
                  top_clients_available: false,
                  top_clients_note: '',
                  persistence: 'In-memory',
                },
              },
            },
          }),
        'GET /api/v1/acls': () => envelope('rules', []),
      }
    }

    it('viewer: Add Rule button is disabled with a requires-role title', async () => {
      window.localStorage.setItem('mcm_admin_token', 'viewer-acl-token')
      vi.stubGlobal('fetch', installFetchMock(aclRoutes('viewer-acl-token', 'viewer')))

      render(<App />)
      await screen.findByText('Signed in')
      await userEvent.setup().click(screen.getByRole('link', { name: /acls/i }))
      await screen.findByRole('heading', { name: 'ACL policy workspace' })

      const addBtn = await screen.findByRole('button', { name: 'Add Rule' })
      expect(addBtn).toBeDisabled()
      expect(addBtn).toHaveAttribute('title', 'Requires operator role or higher')
    })

    it('operator: Add Rule button is enabled', async () => {
      window.localStorage.setItem('mcm_admin_token', 'operator-acl-token')
      vi.stubGlobal('fetch', installFetchMock(aclRoutes('operator-acl-token', 'operator')))

      render(<App />)
      await screen.findByText('Signed in')
      await userEvent.setup().click(screen.getByRole('link', { name: /acls/i }))
      await screen.findByRole('heading', { name: 'ACL policy workspace' })

      const addBtn = await screen.findByRole('button', { name: 'Add Rule' })
      expect(addBtn).toBeEnabled()
      expect(addBtn).not.toHaveAttribute('title')
    })

    it('admin: Add Rule button is enabled', async () => {
      window.localStorage.setItem('mcm_admin_token', 'admin-acl-token')
      vi.stubGlobal('fetch', installFetchMock(aclRoutes('admin-acl-token', 'admin')))

      render(<App />)
      await screen.findByText('Signed in')
      await userEvent.setup().click(screen.getByRole('link', { name: /acls/i }))
      await screen.findByRole('heading', { name: 'ACL policy workspace' })

      const addBtn = await screen.findByRole('button', { name: 'Add Rule' })
      expect(addBtn).toBeEnabled()
    })
  })

  describe('role-aware Deploy panel', () => {
    function deployRoutes(_token: string, role: string): Record<string, RouteHandler> {
      return {
        'GET /api/v1/auth/me': () =>
          jsonResponse({
            id: 2,
            username: 'test-deploy-user',
            disabled: false,
            role,
            created_at: '2026-01-01T00:00:00Z',
            updated_at: '2026-01-01T00:00:00Z',
          }),
        'GET /api/v1/status': () =>
          jsonResponse({
            broker: {
              status: 'connected',
              metrics: {
                traffic: {
                  window_seconds: 300,
                  message_count: 0,
                  message_rate_per_minute: 0,
                  rate_points: [],
                  top_topics: [],
                  top_clients: [],
                  top_clients_available: false,
                  top_clients_note: '',
                  persistence: 'In-memory',
                },
              },
            },
          }),
        'GET /api/v1/deployments': () => envelope('deployments', []),
      }
    }

    it('viewer: Preview Changes button is disabled with a requires-role title', async () => {
      window.localStorage.setItem('mcm_admin_token', 'viewer-deploy-token')
      vi.stubGlobal('fetch', installFetchMock(deployRoutes('viewer-deploy-token', 'viewer')))

      render(<App />)
      await screen.findByText('Signed in')
      await userEvent.setup().click(screen.getByRole('link', { name: /deploy/i }))
      await screen.findByRole('heading', { name: 'Mosquitto configuration deploy' })

      const previewBtn = await screen.findByRole('button', { name: 'Preview Changes' })
      expect(previewBtn).toBeDisabled()
      expect(previewBtn).toHaveAttribute('title', 'Requires operator role or higher')
    })

    it('operator: Preview Changes button is enabled, but Apply is not shown until preview has changes', async () => {
      window.localStorage.setItem('mcm_admin_token', 'operator-deploy-token')
      vi.stubGlobal('fetch', installFetchMock(deployRoutes('operator-deploy-token', 'operator')))

      render(<App />)
      await screen.findByText('Signed in')
      await userEvent.setup().click(screen.getByRole('link', { name: /deploy/i }))
      await screen.findByRole('heading', { name: 'Mosquitto configuration deploy' })

      const previewBtn = await screen.findByRole('button', { name: 'Preview Changes' })
      expect(previewBtn).toBeEnabled()
    })

    it('admin: Preview Changes button is enabled', async () => {
      window.localStorage.setItem('mcm_admin_token', 'admin-deploy-token')
      vi.stubGlobal('fetch', installFetchMock(deployRoutes('admin-deploy-token', 'admin')))

      render(<App />)
      await screen.findByText('Signed in')
      await userEvent.setup().click(screen.getByRole('link', { name: /deploy/i }))
      await screen.findByRole('heading', { name: 'Mosquitto configuration deploy' })

      const previewBtn = await screen.findByRole('button', { name: 'Preview Changes' })
      expect(previewBtn).toBeEnabled()
    })

    // FIX 4: auditor mirrors viewer in Deploy panel
    it('auditor: Preview Changes button is disabled with a requires-role title', async () => {
      window.localStorage.setItem('mcm_admin_token', 'auditor-deploy-token')
      vi.stubGlobal('fetch', installFetchMock(deployRoutes('auditor-deploy-token', 'auditor')))

      render(<App />)
      await screen.findByText('Signed in')
      await userEvent.setup().click(screen.getByRole('link', { name: /deploy/i }))
      await screen.findByRole('heading', { name: 'Mosquitto configuration deploy' })

      const previewBtn = await screen.findByRole('button', { name: 'Preview Changes' })
      expect(previewBtn).toBeDisabled()
      expect(previewBtn).toHaveAttribute('title', 'Requires operator role or higher')
    })
  })

  // FIX 4: auditor mirrors viewer in ACL panel
  describe('role-aware ACL panel — auditor', () => {
    function aclRoutesAuditor(): Record<string, RouteHandler> {
      return {
        'GET /api/v1/auth/me': () =>
          jsonResponse({
            id: 3,
            username: 'auditor-user',
            disabled: false,
            role: 'auditor',
            created_at: '2026-01-01T00:00:00Z',
            updated_at: '2026-01-01T00:00:00Z',
          }),
        'GET /api/v1/status': () =>
          jsonResponse({
            broker: {
              status: 'connected',
              metrics: {
                traffic: {
                  window_seconds: 300,
                  message_count: 0,
                  message_rate_per_minute: 0,
                  rate_points: [],
                  top_topics: [],
                  top_clients: [],
                  top_clients_available: false,
                  top_clients_note: '',
                  persistence: 'In-memory',
                },
              },
            },
          }),
        'GET /api/v1/acls': () => envelope('rules', []),
      }
    }

    it('auditor: Add Rule button is disabled with a requires-role title', async () => {
      window.localStorage.setItem('mcm_admin_token', 'auditor-acl-token')
      vi.stubGlobal('fetch', installFetchMock(aclRoutesAuditor()))

      render(<App />)
      await screen.findByText('Signed in')
      await userEvent.setup().click(screen.getByRole('link', { name: /acls/i }))
      await screen.findByRole('heading', { name: 'ACL policy workspace' })

      const addBtn = await screen.findByRole('button', { name: 'Add Rule' })
      expect(addBtn).toBeDisabled()
      expect(addBtn).toHaveAttribute('title', 'Requires operator role or higher')
    })
  })

  // FIX 2: ACL panel shows friendly 403 message, does not log out
  describe('ACL panel 403 forbidden handling', () => {
    function aclRoutesWithForbiddenSubmit(): Record<string, RouteHandler> {
      return {
        'GET /api/v1/auth/me': () =>
          jsonResponse({
            id: 4,
            username: 'operator-acl',
            disabled: false,
            role: 'operator',
            created_at: '2026-01-01T00:00:00Z',
            updated_at: '2026-01-01T00:00:00Z',
          }),
        'GET /api/v1/status': () =>
          jsonResponse({
            broker: {
              status: 'connected',
              metrics: {
                traffic: {
                  window_seconds: 300,
                  message_count: 0,
                  message_rate_per_minute: 0,
                  rate_points: [],
                  top_topics: [],
                  top_clients: [],
                  top_clients_available: false,
                  top_clients_note: '',
                  persistence: 'In-memory',
                },
              },
            },
          }),
        'GET /api/v1/acls': () => envelope('rules', []),
        'POST /api/v1/acls': () => jsonResponse({ error: 'insufficient role' }, { status: 403 }),
      }
    }

    it('shows the friendly forbidden message when ACL create returns 403 and does not log out', async () => {
      window.localStorage.setItem('mcm_admin_token', 'operator-acl-forbidden-token')
      vi.stubGlobal('fetch', installFetchMock(aclRoutesWithForbiddenSubmit()))

      render(<App />)
      await screen.findByText('Signed in')
      await userEvent.setup().click(screen.getByRole('link', { name: /acls/i }))
      await screen.findByRole('heading', { name: 'ACL policy workspace' })

      // Open the create form
      const addBtn = await screen.findByRole('button', { name: 'Add Rule' })
      await userEvent.setup().click(addBtn)

      // Fill in required fields and submit
      const user = userEvent.setup()
      await user.type(screen.getByPlaceholderText('mqtt-username'), 'test-client')
      await user.type(screen.getByPlaceholderText('sensors/# or device/+/status'), 'sensors/#')
      await user.click(screen.getByRole('button', { name: 'Create rule' }))

      // Panel must show friendly 403 message
      expect(await screen.findByText("You don't have permission to perform this action.")).toBeInTheDocument()

      // Must still be signed in (not logged out)
      expect(screen.queryByRole('heading', { name: 'Sign in' })).not.toBeInTheDocument()
    })
  })

  it('shows ACL guidance and orphan rules, then clears pending deploy after a verified apply', async () => {
    window.localStorage.setItem('mcm_admin_token', 'issue-297-token')
    const applyRequest = vi.fn()
    const fetchMock = installFetchMock({
      ...authenticatedRoutes('issue-297-token'),
      'GET /api/v1/auth/me': () => jsonResponse({
        id: 9,
        username: 'issue-297-admin',
        disabled: false,
        role: 'admin',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
      }),
      'GET /api/v1/acls': () => envelope('rules', []),
      'POST /api/v1/acls': () => jsonResponse({
        id: 'rule-new',
        principal: 'new-device',
        topic_filter: 'factory/#',
        permission: 'read',
      }, { status: 201 }),
      'GET /api/v1/deployments': () => envelope('deployments', []),
      'POST /api/v1/deployments/preview': () => jsonResponse({
        revision_id: 'revision-297',
        acl_diff: '-user retired-device\n+user new-device',
        passwd_diff: '',
        orphan_rules: [
          {
            id: 'orphan-1',
            principal: 'retired-device',
            topic_filter: 'factory/retired/#',
            permission: 'read',
            description: 'Retired device rule',
          },
        ],
        has_changes: true,
      }),
      'POST /api/v1/deployments/apply': (init) => {
        applyRequest(init)
        return jsonResponse({
          id: 17,
          status: 'active_verified',
          message: 'Configuration verified successfully.',
          created_at: '2026-01-01T00:00:00Z',
        })
      },
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)

    const user = userEvent.setup()
    await screen.findByText('Signed in')
    await user.click(screen.getByRole('link', { name: /acls/i }))
    await screen.findByRole('heading', { name: 'ACL policy workspace' })
    await user.click(screen.getByRole('button', { name: 'Add Rule' }))

    expect(screen.getByText(/Use an MQTT username from the Users panel/)).toBeInTheDocument()
    expect(screen.queryByPlaceholderText('username or $client_id')).not.toBeInTheDocument()
    await user.type(screen.getByPlaceholderText('mqtt-username'), 'new-device')
    await user.type(screen.getByPlaceholderText('sensors/# or device/+/status'), 'factory/#')
    await user.click(screen.getByRole('button', { name: 'Create rule' }))

    expect(await screen.findByText('Pending deploy')).toBeInTheDocument()
    await user.click(screen.getByRole('link', { name: /deploy/i }))
    await screen.findByRole('heading', { name: 'Mosquitto configuration deploy' })
    expect(screen.getByText('Pending deploy')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Preview Changes' }))
    expect(await screen.findByText('Orphan ACL rules')).toBeInTheDocument()
    expect(screen.getByText('retired-device')).toBeInTheDocument()
    expect(screen.getByText('factory/retired/#')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Apply' }))
    await user.type(screen.getByLabelText('MFA code'), '123456')
    await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
    await screen.findByText(/MFA confirmed/)
    await user.click(screen.getByRole('button', { name: 'Confirm Apply' }))

    await screen.findByText('Configuration verified successfully.')
    expect(applyRequest).toHaveBeenCalledWith(expect.objectContaining({
      body: JSON.stringify({ revision_id: 'revision-297' }),
    }))
    await waitFor(() => {
      expect(screen.queryByText('Pending deploy')).not.toBeInTheDocument()
    })
  })

  it('restores the pending deploy banner from the reload-safe marker', async () => {
    window.localStorage.setItem('mcm_admin_token', 'reload-token')
    window.localStorage.setItem('mcm_deploy_pending', '1')
    vi.stubGlobal('fetch', installFetchMock(authenticatedRoutes('reload-token')))

    render(<App />)

    await screen.findByText('Signed in')
    expect(screen.getByText('Pending deploy')).toBeInTheDocument()
  })

  it('keeps the pending deploy banner when apply fails', async () => {
    window.localStorage.setItem('mcm_admin_token', 'apply-error-token')
    vi.stubGlobal('fetch', installFetchMock({
      ...authenticatedRoutes('apply-error-token'),
      'GET /api/v1/auth/me': () => jsonResponse({
        id: 9,
        username: 'apply-error-admin',
        disabled: false,
        role: 'admin',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
      }),
      'GET /api/v1/acls': () => envelope('rules', []),
      'POST /api/v1/acls': () => jsonResponse({
        id: 'rule-error',
        principal: 'new-device',
        topic_filter: 'factory/#',
        permission: 'read',
      }, { status: 201 }),
      'GET /api/v1/deployments': () => envelope('deployments', []),
      'POST /api/v1/deployments/preview': () => jsonResponse({
        revision_id: 'revision-error',
        acl_diff: '+user new-device',
        passwd_diff: '',
        orphan_rules: [],
        has_changes: true,
      }),
      'POST /api/v1/deployments/apply': () => jsonResponse({ error: 'apply failed' }, { status: 500 }),
    }))

    render(<App />)
    const user = userEvent.setup()
    await screen.findByText('Signed in')
    await user.click(screen.getByRole('link', { name: /acls/i }))
    await screen.findByRole('heading', { name: 'ACL policy workspace' })
    await user.click(screen.getByRole('button', { name: 'Add Rule' }))
    await user.type(screen.getByPlaceholderText('mqtt-username'), 'new-device')
    await user.type(screen.getByPlaceholderText('sensors/# or device/+/status'), 'factory/#')
    await user.click(screen.getByRole('button', { name: 'Create rule' }))
    expect(await screen.findByText('Pending deploy')).toBeInTheDocument()

    await user.click(screen.getByRole('link', { name: /deploy/i }))
    await screen.findByRole('heading', { name: 'Mosquitto configuration deploy' })
    await user.click(screen.getByRole('button', { name: 'Preview Changes' }))
    await screen.findByRole('button', { name: 'Apply' })
    await user.click(screen.getByRole('button', { name: 'Apply' }))
    await user.type(screen.getByLabelText('MFA code'), '123456')
    await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
    await screen.findByText(/MFA confirmed/)
    await user.click(screen.getByRole('button', { name: 'Confirm Apply' }))

    expect(await screen.findByText('apply failed')).toBeInTheDocument()
    expect(screen.getByText('Pending deploy')).toBeInTheDocument()
    expect(window.localStorage.getItem('mcm_deploy_pending')).toBe('1')
  })

  it('does not let a mutation callback that started before apply restore the banner', async () => {
    window.localStorage.setItem('mcm_admin_token', 'ordered-mutation-token')
    let resolveCreate: ((response: Response) => void) | undefined
    const pendingCreate = new Promise<Response>((resolve) => { resolveCreate = resolve })
    vi.stubGlobal('fetch', installFetchMock({
      ...authenticatedRoutes('ordered-mutation-token'),
      'GET /api/v1/auth/me': () => jsonResponse({
        id: 9,
        username: 'ordered-admin',
        disabled: false,
        role: 'admin',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
      }),
      'GET /api/v1/acls': () => envelope('rules', []),
      'POST /api/v1/acls': () => pendingCreate,
      'GET /api/v1/deployments': () => envelope('deployments', []),
      'POST /api/v1/deployments/preview': () => jsonResponse({
        revision_id: 'revision-ordered',
        acl_diff: '+user new-device',
        passwd_diff: '',
        orphan_rules: [],
        has_changes: true,
      }),
      'POST /api/v1/deployments/apply': () => jsonResponse({
        id: 18,
        status: 'active_verified',
        created_at: '2026-01-01T00:00:00Z',
      }),
    }))

    render(<App />)
    const user = userEvent.setup()
    await screen.findByText('Signed in')
    await user.click(screen.getByRole('link', { name: /acls/i }))
    await screen.findByRole('heading', { name: 'ACL policy workspace' })
    await user.click(screen.getByRole('button', { name: 'Add Rule' }))
    await user.type(screen.getByPlaceholderText('mqtt-username'), 'new-device')
    await user.type(screen.getByPlaceholderText('sensors/# or device/+/status'), 'factory/#')
    await user.click(screen.getByRole('button', { name: 'Create rule' }))

    await user.click(screen.getByRole('link', { name: /deploy/i }))
    await screen.findByRole('heading', { name: 'Mosquitto configuration deploy' })
    await user.click(screen.getByRole('button', { name: 'Preview Changes' }))
    await screen.findByRole('button', { name: 'Apply' })
    await user.click(screen.getByRole('button', { name: 'Apply' }))
    await user.type(screen.getByLabelText('MFA code'), '123456')
    await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
    await screen.findByText(/MFA confirmed/)
    await user.click(screen.getByRole('button', { name: 'Confirm Apply' }))
    await screen.findByText('active_verified')
    expect(screen.queryByText('Pending deploy')).not.toBeInTheDocument()

    resolveCreate?.(jsonResponse({
      id: 'rule-ordered',
      principal: 'new-device',
      topic_filter: 'factory/#',
      permission: 'read',
    }, { status: 201 }))
    await waitFor(() => {
      expect(screen.queryByText('Pending deploy')).not.toBeInTheDocument()
    })
    expect(window.localStorage.getItem('mcm_deploy_pending')).toBeNull()
  })

  it('keeps the pending deploy banner when a mutation starts during apply', async () => {
    window.localStorage.setItem('mcm_admin_token', 'during-apply-token')
    let resolveApply: ((response: Response) => void) | undefined
    const pendingApply = new Promise<Response>((resolve) => { resolveApply = resolve })
    const applyRequest = vi.fn()
    vi.stubGlobal('fetch', installFetchMock({
      ...authenticatedRoutes('during-apply-token'),
      'GET /api/v1/auth/me': () => jsonResponse({
        id: 9,
        username: 'during-apply-admin',
        disabled: false,
        role: 'admin',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
      }),
      'GET /api/v1/acls': () => envelope('rules', []),
      'POST /api/v1/acls': () => jsonResponse({
        id: 'rule-during-apply',
        principal: 'new-device',
        topic_filter: 'factory/#',
        permission: 'read',
      }, { status: 201 }),
      'GET /api/v1/deployments': () => envelope('deployments', []),
      'POST /api/v1/deployments/preview': () => jsonResponse({
        revision_id: 'revision-during-apply',
        acl_diff: '+user new-device',
        passwd_diff: '',
        orphan_rules: [],
        has_changes: true,
      }),
      'POST /api/v1/deployments/apply': (init) => {
        applyRequest(init)
        return pendingApply
      },
    }))

    render(<App />)
    const user = userEvent.setup()
    await screen.findByText('Signed in')
    await user.click(screen.getByRole('link', { name: /deploy/i }))
    await screen.findByRole('heading', { name: 'Mosquitto configuration deploy' })
    await user.click(screen.getByRole('button', { name: 'Preview Changes' }))
    await screen.findByRole('button', { name: 'Apply' })
    await user.click(screen.getByRole('button', { name: 'Apply' }))
    await user.type(screen.getByLabelText('MFA code'), '123456')
    await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
    await screen.findByText(/MFA confirmed/)
    await user.click(screen.getByRole('button', { name: 'Confirm Apply' }))
    await waitFor(() => expect(applyRequest).toHaveBeenCalled())

    await user.click(screen.getByRole('link', { name: /acls/i }))
    await screen.findByRole('heading', { name: 'ACL policy workspace' })
    await user.click(screen.getByRole('button', { name: 'Add Rule' }))
    await user.type(screen.getByPlaceholderText('mqtt-username'), 'new-device')
    await user.type(screen.getByPlaceholderText('sensors/# or device/+/status'), 'factory/#')
    await user.click(screen.getByRole('button', { name: 'Create rule' }))
    expect(await screen.findByText('Pending deploy')).toBeInTheDocument()

    resolveApply?.(jsonResponse({
      id: 19,
      status: 'active_verified',
      created_at: '2026-01-01T00:00:00Z',
    }))
    await waitFor(() => expect(window.localStorage.getItem('mcm_deploy_pending')).toBe('1'))
    expect(screen.getByText('Pending deploy')).toBeInTheDocument()
  })

  // The listener scope of a deployment is only in effect once the listener
  // configuration declares the file of the listener. The preview tells the
  // operator which scoped rules wait for that step and which ones can never
  // work.
  it('shows the listener scope of the deployment preview', async () => {
    window.localStorage.setItem('mcm_admin_token', 'scope-token')
    vi.stubGlobal('fetch', installFetchMock({
      ...authenticatedRoutes('scope-token'),
      'GET /api/v1/deployments': () => envelope('deployments', []),
      'POST /api/v1/deployments/preview': () => jsonResponse({
        revision_id: 'revision-scope',
        acl_diff: '+user plant\n+topic readwrite plant/#',
        passwd_diff: '',
        scope_pending_activation: [
          'listener "plant" does not declare "/mosquitto/config/acl.plant" in the broker configuration yet; apply the listener configuration to activate its scoped rules',
        ],
        has_changes: true,
      }),
    }))

    render(<App />)
    const user = userEvent.setup()
    await screen.findByText('Signed in')
    await user.click(screen.getByRole('link', { name: /deploy/i }))
    await screen.findByRole('heading', { name: 'Mosquitto configuration deploy' })
    await user.click(screen.getByRole('button', { name: 'Preview Changes' }))

    expect(await screen.findByText('Pending listener activation')).toBeInTheDocument()
    expect(screen.getByText(/apply the listener configuration to activate its scoped rules/)).toBeInTheDocument()
    expect(screen.queryByText('Listener scope problems')).not.toBeInTheDocument()
  })

  it('refuses a deployment whose listener scope can never work', async () => {
    window.localStorage.setItem('mcm_admin_token', 'scope-broken-token')
    vi.stubGlobal('fetch', installFetchMock({
      ...authenticatedRoutes('scope-broken-token'),
      'GET /api/v1/deployments': () => envelope('deployments', []),
      'POST /api/v1/deployments/preview': () => jsonResponse({
        revision_id: 'revision-scope-broken',
        acl_diff: '+user plant',
        passwd_diff: '',
        scope_issues: [
          'listener "plant" does not exist; its scoped rules would never apply',
        ],
        has_changes: true,
      }),
    }))

    render(<App />)
    const user = userEvent.setup()
    await screen.findByText('Signed in')
    await user.click(screen.getByRole('link', { name: /deploy/i }))
    await screen.findByRole('heading', { name: 'Mosquitto configuration deploy' })
    await user.click(screen.getByRole('button', { name: 'Preview Changes' }))

    expect(await screen.findByText('Listener scope problems')).toBeInTheDocument()
    expect(screen.getByText(/its scoped rules would never apply/)).toBeInTheDocument()
    expect(screen.queryByText('Pending listener activation')).not.toBeInTheDocument()
  })

  it('shows the impact of the import and the files of the listeners', async () => {
    window.localStorage.setItem('mcm_admin_token', 'impact-token')
    vi.stubGlobal('fetch', installFetchMock({
      ...authenticatedRoutes('impact-token'),
      'GET /api/v1/deployments': () => envelope('deployments', []),
      'POST /api/v1/deployments/preview': () => jsonResponse({
        revision_id: 'revision-impact',
        acl_diff: '-topic read sensors/#',
        passwd_diff: '',
        at_risk_entries: ['user alice|sensors/#|read'],
        preserved_entries: ['topic readwrite devices/%u/data'],
        listener_acl_files: [
          {
            listener_id: 'plant',
            path: '/mosquitto/config/acl.plant',
            diff: '+user plant\n+topic readwrite plant/#',
          },
        ],
        removed_acl_files: ['/mosquitto/config/acl.retired'],
        has_changes: true,
      }),
    }))

    render(<App />)
    const user = userEvent.setup()
    await screen.findByText('Signed in')
    await user.click(screen.getByRole('link', { name: /deploy/i }))
    await screen.findByRole('heading', { name: 'Mosquitto configuration deploy' })
    await user.click(screen.getByRole('button', { name: 'Preview Changes' }))

    expect(await screen.findByText('Access at risk')).toBeInTheDocument()
    expect(screen.getByText('user alice|sensors/#|read')).toBeInTheDocument()
    expect(screen.getByText('Preserved entries')).toBeInTheDocument()
    expect(screen.getByText('topic readwrite devices/%u/data')).toBeInTheDocument()
    expect(screen.getByText('Listener file — plant')).toBeInTheDocument()
    expect(screen.getByText('/mosquitto/config/acl.plant')).toBeInTheDocument()
    expect(screen.getByText('Files the deploy would delete')).toBeInTheDocument()
    expect(screen.getByText('/mosquitto/config/acl.retired')).toBeInTheDocument()
  })

  async function openDeployPreview(token: string, preview: Record<string, unknown>) {
    window.localStorage.setItem('mcm_admin_token', token)
    vi.stubGlobal('fetch', installFetchMock({
      ...authenticatedRoutes(token),
      'GET /api/v1/auth/me': () => jsonResponse({ id: 1, username: 'deploy-admin', disabled: false, role: 'admin', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }),
      'GET /api/v1/deployments': () => envelope('deployments', []),
      'POST /api/v1/deployments/preview': () => jsonResponse({ revision_id: 'revision-358', acl_diff: '', passwd_diff: '', has_changes: true, ...preview }),
    }))
    render(<App />)
    const user = userEvent.setup()
    await screen.findByText('Signed in')
    await user.click(screen.getByRole('link', { name: /deploy/i }))
    await screen.findByRole('heading', { name: 'Mosquitto configuration deploy' })
    expect(screen.queryByRole('button', { name: 'Apply' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Preview Changes' }))
  }

  it('states the real scope of the deploy and its counters before the operator confirms', async () => {
    await openDeployPreview('scope-summary-token', {
      acl_diff: '+user plant\n+topic readwrite plant/#',
      passwd_diff: '+plant:hash',
      summary: { users_added: 2, users_removed: 1, users_rotated: 3, has_passwd_changes: true, topics_added: 4, topics_removed: 5, has_acl_changes: true },
    })

    const scope = (await screen.findByText('What this deploy covers')).closest('section') as HTMLElement
    expect(scope).not.toBeNull()
    expect(within(scope).getByText(/every pending change saved in MCM, including changes saved outside this task/i)).toBeInTheDocument()
    expect(within(scope).getByText(/cannot tell which task or person saved a pending change/i)).toBeInTheDocument()
    expect(within(scope).getByText(/broker-wide password and ACL files/i)).toBeInTheDocument()
    expect(within(scope).getByText(/listener files are listed separately/i)).toBeInTheDocument()
    const counter = (label: string) => within(scope).getByText(label).parentElement as HTMLElement
    expect(counter('MQTT users added')).toHaveTextContent('2')
    expect(counter('MQTT users removed')).toHaveTextContent('1')
    expect(counter('MQTT users rotated')).toHaveTextContent('3')
    expect(counter('ACL topics added')).toHaveTextContent('4')
    expect(counter('ACL topics removed')).toHaveTextContent('5')
    expect(screen.getByRole('button', { name: 'Apply' })).toBeEnabled()
    expect(screen.queryByRole('button', { name: 'Confirm Apply' })).not.toBeInTheDocument()
  })

  it('keeps the scope statement and omits the counters when the preview has no summary', async () => {
    await openDeployPreview('scope-nosummary-token', { acl_diff: '+user plant' })

    expect(await screen.findByText('What this deploy covers')).toBeInTheDocument()
    expect(screen.getByText(/including changes saved outside this task/i)).toBeInTheDocument()
    expect(screen.queryByText('MQTT users added')).not.toBeInTheDocument()
  })

  it('shows scope blockers expanded, above the warnings, and refuses the apply control', async () => {
    await openDeployPreview('blocker-token', {
      acl_diff: '+user plant',
      orphan_rules: [{ id: 'orphan-1', principal: 'retired-device', topic_filter: 'factory/#', permission: 'read' }],
      scope_issues: ['listener "plant" does not exist; its scoped rules would never apply'],
    })

    const heading = await screen.findByText('Listener scope problems')
    expect(heading).toBeVisible()
    expect(screen.getByText(/its scoped rules would never apply/)).toBeVisible()
    expect(heading.closest('details')).toBeNull()
    expect(within(heading.parentElement as HTMLElement).getByText('1')).toBeInTheDocument()
    expect(heading.compareDocumentPosition(screen.getByText('Orphan ACL rules')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(heading.compareDocumentPosition(screen.getByText('ACL diff')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled()
    expect(screen.getByText(/deploy is refused until the scope problems are resolved/i)).toBeInTheDocument()
  })

  it('keeps diffs and file bodies behind an on-demand disclosure but file deletions outside it', async () => {    await openDeployPreview('disclosure-token', {
      acl_diff: '+user plant\n+topic readwrite plant/#',
      passwd_diff: '+plant:hash',
      preserved_entries: ['topic readwrite devices/%u/data'],
      listener_acl_files: [{ listener_id: 'plant', path: '/mosquitto/config/acl.plant', diff: '+user plant' }],
      removed_acl_files: ['/mosquitto/config/acl.retired'],
    })

    const summary = await screen.findByText(/diffs and file contents/i)
    const disclosure = summary.closest('details') as HTMLDetailsElement
    expect(disclosure).not.toBeNull()
    expect(disclosure.open).toBe(false)
    expect(within(disclosure).getByText('ACL diff')).toBeInTheDocument()
    expect(within(disclosure).getByText('Password file diff')).toBeInTheDocument()
    expect(within(disclosure).getByText('Preserved entries')).toBeInTheDocument()
    expect(within(disclosure).getByText('Listener file — plant')).toBeInTheDocument()
    expect(screen.getByText('Files the deploy would delete').closest('details')).toBeNull()
    expect(screen.getByText('/mosquitto/config/acl.retired').closest('details')).toBeNull()
  })

  it('refuses the confirmed apply too while a scope blocker is open', async () => {
    await openDeployPreview('blocked-confirm-token', {
      acl_diff: '+user plant',
      scope_issues: ['listener "plant" does not exist; its scoped rules would never apply'],
    })

    const apply = await screen.findByRole('button', { name: 'Apply' })
    expect(apply).toBeDisabled()
    // A confirmation left over from a previous preview must not survive it.
    await userEvent.setup().click(screen.getByRole('button', { name: 'Preview Changes' }))
    await screen.findByText('Listener scope problems')
    expect(screen.queryByRole('button', { name: 'Confirm Apply' })).not.toBeInTheDocument()
  })

  it('shows the blocking entries the API returns when it refuses an apply', async () => {
    window.localStorage.setItem('mcm_admin_token', 'apply-details-token')
    vi.stubGlobal('fetch', installFetchMock({
      ...authenticatedRoutes('apply-details-token'),
      'GET /api/v1/auth/me': () => jsonResponse({ id: 1, username: 'details-admin', disabled: false, role: 'admin', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }),
      'GET /api/v1/deployments': () => envelope('deployments', []),
      'POST /api/v1/deployments/preview': () => jsonResponse({ revision_id: 'revision-details', acl_diff: '-topic read sensors/#', passwd_diff: '', has_changes: true }),
      'POST /api/v1/deployments/apply': () => jsonResponse({
        error: 'the deploy would drop ACL access that is not managed by MCM',
        details: ['user alice|sensors/#|read'],
      }, { status: 409 }),
    }))

    render(<App />)
    const user = userEvent.setup()
    await screen.findByText('Signed in')
    await user.click(screen.getByRole('link', { name: /deploy/i }))
    await screen.findByRole('heading', { name: 'Mosquitto configuration deploy' })
    await user.click(screen.getByRole('button', { name: 'Preview Changes' }))
    await screen.findByRole('button', { name: 'Apply' })
    await user.click(screen.getByRole('button', { name: 'Apply' }))
    await user.type(screen.getByLabelText('MFA code'), '123456')
    await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
    await screen.findByText(/MFA confirmed/)
    await user.click(screen.getByRole('button', { name: 'Confirm Apply' }))

    expect(await screen.findByText(/the deploy would drop ACL access that is not managed by MCM/)).toBeInTheDocument()
    expect(screen.getByText(/user alice\|sensors\/#\|read/)).toBeInTheDocument()
  })

  // FIX 2: Deploy panel shows friendly 403 message, does not log out
  describe('Deploy panel 403 forbidden handling', () => {
    function deployRoutesWithForbiddenPreview(): Record<string, RouteHandler> {
      return {
        'GET /api/v1/auth/me': () =>
          jsonResponse({
            id: 5,
            username: 'operator-deploy',
            disabled: false,
            role: 'operator',
            created_at: '2026-01-01T00:00:00Z',
            updated_at: '2026-01-01T00:00:00Z',
          }),
        'GET /api/v1/status': () =>
          jsonResponse({
            broker: {
              status: 'connected',
              metrics: {
                traffic: {
                  window_seconds: 300,
                  message_count: 0,
                  message_rate_per_minute: 0,
                  rate_points: [],
                  top_topics: [],
                  top_clients: [],
                  top_clients_available: false,
                  top_clients_note: '',
                  persistence: 'In-memory',
                },
              },
            },
          }),
        'GET /api/v1/deployments': () => envelope('deployments', []),
        'POST /api/v1/deployments/preview': () =>
          jsonResponse({ error: 'insufficient role' }, { status: 403 }),
      }
    }

    it('shows the friendly forbidden message when Preview returns 403 and does not log out', async () => {
      window.localStorage.setItem('mcm_admin_token', 'operator-deploy-forbidden-token')
      vi.stubGlobal('fetch', installFetchMock(deployRoutesWithForbiddenPreview()))

      render(<App />)
      await screen.findByText('Signed in')
      await userEvent.setup().click(screen.getByRole('link', { name: /deploy/i }))
      await screen.findByRole('heading', { name: 'Mosquitto configuration deploy' })

      const previewBtn = await screen.findByRole('button', { name: 'Preview Changes' })
      await userEvent.setup().click(previewBtn)

      // Panel must show friendly 403 message
      expect(await screen.findByText("You don't have permission to perform this action.")).toBeInTheDocument()

      // Must still be signed in (not logged out)
      expect(screen.queryByRole('heading', { name: 'Sign in' })).not.toBeInTheDocument()
    })
  })
  it('organizes existing routes into five collapsible operator areas with the requested defaults', async () => {
    window.localStorage.setItem('mcm_admin_token', 'navigation-token')
    vi.stubGlobal('fetch', installFetchMock(authenticatedRoutes('navigation-token')))
    render(<App />)
    for (const name of ['Configuration', 'MQTT Access', 'Changes', 'Diagnostics', 'Panel administration']) {
      expect(await screen.findByRole('heading', { name, level: 2 })).toBeInTheDocument()
    }
    for (const group of ['Configuration', 'MQTT Access', 'Changes', 'Diagnostics', 'Panel administration']) {
      const expanded = group === 'Configuration' || group === 'Diagnostics'
      const control = screen.getByRole('button', { name: `${expanded ? 'Collapse' : 'Expand'} ${group}` })
      expect(control).toHaveAttribute('aria-controls', `navigation-${group.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`)
      expect(control).toHaveAttribute('aria-expanded', expanded ? 'true' : 'false')
    }
    expect(screen.getByRole('region', { name: 'Configuration' })).toBeInTheDocument()
    expect(screen.getByText('Observation · read-only')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /^MQTT users/ })).toHaveAttribute('href', '/users')
    expect(screen.getByRole('link', { name: /ACLs/ })).toHaveAttribute('href', '/acls')
    expect(screen.getByRole('link', { name: /Configuration revisions/ })).toHaveAttribute('href', '/changes')
  })

  it('toggles a collapsed group with a button and hides its entries from assistive technology and the tab order', async () => {
    window.localStorage.setItem('mcm_admin_token', 'toggle-navigation-token')
    vi.stubGlobal('fetch', installFetchMock(authenticatedRoutes('toggle-navigation-token')))
    render(<App />)
    await screen.findByText('Signed in')
    const user = userEvent.setup()
    const control = screen.getByRole('button', { name: 'Expand MQTT Access' })
    const region = screen.getByRole('region', { name: 'MQTT Access' })
    expect(region).toHaveClass('max-h-0')
    // A collapsed disclosure keeps its entries out of the accessibility tree
    // and out of the tab order: the toggle button is the only way in.
    expect(region).toHaveAttribute('inert')
    await user.click(control)
    expect(screen.getByRole('button', { name: 'Collapse MQTT Access' })).toHaveAttribute('aria-expanded', 'true')
    expect(region).toHaveClass('max-h-[40rem]')
    expect(region).not.toHaveAttribute('inert')
    await user.keyboard('{Enter}')
    expect(screen.getByRole('button', { name: 'Expand MQTT Access' })).toHaveAttribute('aria-expanded', 'false')
  })

  it('auto-expands the active group on a direct URL load and keeps the active link current', async () => {
    window.localStorage.setItem('mcm_admin_token', 'direct-navigation-token')
    window.history.replaceState(null, '', '/logs')
    vi.stubGlobal('fetch', installFetchMock(authenticatedRoutes('direct-navigation-token')))
    render(<App />)
    await screen.findByRole('heading', { name: 'Realtime broker and MCM events' })
    expect(screen.getByRole('button', { name: 'Collapse Diagnostics' })).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('link', { name: /Logs/ })).toHaveAttribute('aria-current', 'page')
  })

  it('preserves role filtering and omits groups with no visible entries', async () => {
    window.localStorage.setItem('mcm_admin_token', 'viewer-navigation-token')
    vi.stubGlobal('fetch', installFetchMock({
      ...authenticatedRoutes('viewer-navigation-token'),
      'GET /api/v1/auth/me': () => jsonResponse({ id: 1, username: 'viewer', disabled: false, role: 'viewer', created_at: '', updated_at: '' }),
    }))
    render(<App />)
    await screen.findByText('Signed in')
    expect(screen.getByRole('link', { name: /^MQTT users/ })).toHaveAttribute('href', '/users')
    expect(screen.getByRole('link', { name: /Dashboard/ })).toHaveAttribute('href', '/dashboard')
    expect(screen.queryByRole('heading', { name: 'Configuration', level: 2 })).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Changes', level: 2 })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /Configuration revisions/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /Certificates/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /Panel operators/ })).not.toBeInTheDocument()
  })

  it('separates MQTT users from panel operators in unambiguous groups (#355)', async () => {
    window.localStorage.setItem('mcm_admin_token', 'grouped-accounts-token')
    vi.stubGlobal('fetch', installFetchMock({
      ...authenticatedRoutes('grouped-accounts-token'),
      'GET /api/v1/auth/me': () => jsonResponse({ id: 1, username: 'admin', disabled: false, role: 'admin', created_at: '', updated_at: '' }),
    }))
    render(<App />)
    await screen.findByText('Signed in')
    const mqttAccess = screen.getByRole('region', { name: 'MQTT Access' })
    const panelAdministration = screen.getByRole('region', { name: 'Panel administration' })
    expect(within(mqttAccess).getByRole('link', { name: /^MQTT users/ })).toHaveAttribute('href', '/users')
    expect(within(panelAdministration).getByRole('link', { name: /^Panel operators/ })).toHaveAttribute('href', '/admin-users')
    expect(within(mqttAccess).queryByRole('link', { name: /operators/i })).not.toBeInTheDocument()
    expect(within(panelAdministration).queryByRole('link', { name: /MQTT/ })).not.toBeInTheDocument()
    expect(screen.getByText('Broker clients · devices and apps')).toBeInTheDocument()
    expect(screen.getByText('MCM console · operator accounts')).toBeInTheDocument()
  })

  it('folds the navigation behind a keyboard-operable menu on narrow screens (#355)', async () => {
    window.localStorage.setItem('mcm_admin_token', 'mobile-menu-token')
    vi.stubGlobal('fetch', installFetchMock(authenticatedRoutes('mobile-menu-token')))
    render(<App />)
    await screen.findByText('Signed in')
    const user = userEvent.setup()
    const toggle = screen.getByRole('button', { name: 'Menu' })
    const panel = document.getElementById(toggle.getAttribute('aria-controls') ?? '')
    // Closed by default below lg: the content comes first, the desktop sidebar stays visible.
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(toggle).toHaveClass('lg:hidden')
    expect(panel).toHaveClass('hidden', 'lg:block')
    expect(panel).toContainElement(screen.getByRole('navigation', { name: 'Primary navigation' }))

    toggle.focus()
    await user.keyboard('{Enter}')
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(panel).not.toHaveClass('hidden')

    screen.getByRole('link', { name: /^Dashboard/ }).focus()
    await user.keyboard('{Escape}')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(panel).toHaveClass('hidden')
    expect(toggle).toHaveFocus()

    await user.keyboard(' ')
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    const logs = screen.getByRole('link', { name: /^Logs/ })
    expect(logs).toHaveAttribute('href', '/logs')
    await user.click(logs)
    expect(window.location.pathname).toBe('/logs')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(toggle).toHaveFocus()
  })

  describe('the pending deploy notice (issue #358)', () => {
    function noticeRoutes(token: string, role: string): Record<string, RouteHandler> {
      return {
        ...authenticatedRoutes(token),
        'GET /api/v1/auth/me': () => jsonResponse({
          id: 9,
          username: `notice-${role}`,
          disabled: false,
          role,
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-01T00:00:00Z',
        }),
        'GET /api/v1/acls': () => envelope('rules', []),
        'POST /api/v1/acls': () => jsonResponse({
          id: 'rule-notice',
          principal: 'new-device',
          topic_filter: 'factory/#',
          permission: 'read',
        }, { status: 201 }),
        'GET /api/v1/mqtt-users': () => envelope('users', []),
        'GET /api/v1/deployments': () => envelope('deployments', []),
      }
    }

    it('offers an operator the review of the saved changes without an apply action', async () => {
      window.localStorage.setItem('mcm_admin_token', 'notice-operator-token')
      vi.stubGlobal('fetch', installFetchMock(noticeRoutes('notice-operator-token', 'operator')))

      render(<App />)
      const user = userEvent.setup()
      await screen.findByText('Signed in')
      await user.click(screen.getByRole('link', { name: /acls/i }))
      await screen.findByRole('heading', { name: 'ACL policy workspace' })
      await user.click(screen.getByRole('button', { name: 'Add Rule' }))
      await user.type(screen.getByPlaceholderText('mqtt-username'), 'new-device')
      await user.type(screen.getByPlaceholderText('sensors/# or device/+/status'), 'factory/#')
      await user.click(screen.getByRole('button', { name: 'Create rule' }))

      expect(await screen.findByText('Pending deploy')).toBeInTheDocument()
      const review = screen.getByRole('button', { name: 'Preview changes' })
      expect(screen.queryByRole('button', { name: 'Review and apply' })).not.toBeInTheDocument()
      expect(screen.getByText('Applying them needs the admin role and MFA.')).toBeInTheDocument()

      await user.click(review)
      expect(window.location.pathname).toBe('/deploy')
    })

    it('offers an admin the review of the saved changes', async () => {
      window.localStorage.setItem('mcm_admin_token', 'notice-admin-token')
      vi.stubGlobal('fetch', installFetchMock(noticeRoutes('notice-admin-token', 'admin')))

      render(<App />)
      const user = userEvent.setup()
      await screen.findByText('Signed in')
      await user.click(screen.getByRole('link', { name: /acls/i }))
      await screen.findByRole('heading', { name: 'ACL policy workspace' })
      await user.click(screen.getByRole('button', { name: 'Add Rule' }))
      await user.type(screen.getByPlaceholderText('mqtt-username'), 'new-device')
      await user.type(screen.getByPlaceholderText('sensors/# or device/+/status'), 'factory/#')
      await user.click(screen.getByRole('button', { name: 'Create rule' }))

      expect(await screen.findByText('Pending deploy')).toBeInTheDocument()
      await user.click(screen.getByRole('button', { name: 'Review and apply' }))
      expect(window.location.pathname).toBe('/deploy')
    })

    it('explains to a viewer without offering an action they cannot take', async () => {
      window.localStorage.setItem('mcm_admin_token', 'notice-viewer-token')
      window.localStorage.setItem('mcm_deploy_pending', '1')
      window.history.replaceState(null, '', '/users')
      vi.stubGlobal('fetch', installFetchMock(noticeRoutes('notice-viewer-token', 'viewer')))

      render(<App />)
      await screen.findByText('Signed in')

      expect(await screen.findByText('Pending deploy')).toBeInTheDocument()
      expect(screen.getByText('Previewing them needs the operator role or higher; applying them needs the admin role and MFA.')).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: /changes/ })).not.toBeInTheDocument()
    })
  })

  describe('operational dashboard home (#354)', () => {
    function homeRoutes(
      { role = 'operator', broker = 'connected', messageCount = 0, statusFails = false, observedAt, lastMessageAt }: { role?: string; broker?: 'connected' | 'disconnected'; messageCount?: number; statusFails?: boolean; observedAt?: string; lastMessageAt?: string } = {},
    ): Record<string, RouteHandler> {
      return {
        'GET /api/v1/auth/me': () =>
          jsonResponse({ id: 21, username: `home-${role}`, disabled: false, role, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }),
        'GET /api/v1/status': () =>
          statusFails
            ? new Response('unavailable', { status: 503 })
            : jsonResponse({
                broker: {
                  status: broker,
                  observed_at: observedAt ?? new Date(Date.now() - 120_000).toISOString(),
                  target: { host: 'mosquitto', port: 1883, address: 'mosquitto:1883', tls_enabled: false },
                  metrics: {
                    last_message_at: lastMessageAt ?? '0001-01-01T00:00:00Z',
                    traffic: {
                      window_seconds: 300,
                      message_count: messageCount,
                      message_rate_per_minute: messageCount / 5,
                      rate_points: [],
                      top_topics: [],
                      top_clients: [],
                      top_clients_available: false,
                      top_clients_note: 'Unavailable in tests.',
                      persistence: 'In-memory',
                    },
                  },
                },
              }),
        'GET /api/v1/mqtt-users': () => envelope('users', []),
      }
    }

    function renderHome(token: string, options: Parameters<typeof homeRoutes>[0] = {}) {
      window.localStorage.setItem('mcm_admin_token', token)
      vi.stubGlobal('fetch', installFetchMock(homeRoutes(options)))
      render(<App />)
    }

    it('tells a connected broker without traffic apart from a disconnection and offers operators to connect an application', async () => {
      renderHome('idle-token')

      const health = await screen.findByRole('region', { name: 'Broker health' })
      expect(await within(health).findByText('Connected · no traffic')).toBeInTheDocument()
      expect(within(health).getByText(/No traffic is not a disconnection/)).toBeInTheDocument()
      expect(within(health).getByText('mosquitto:1883')).toBeInTheDocument()
      expect(within(health).getByText('Status reported')).toBeInTheDocument()
      expect(within(health).getByText('2 min ago')).toBeInTheDocument()
      expect(within(health).getByText('None observed yet')).toBeInTheDocument()
      expect(within(health).queryByText('Disconnected')).not.toBeInTheDocument()
      expect(window.location.pathname).toBe('/')

      await userEvent.click(screen.getByRole('button', { name: 'Connect an application' }))

      expect(await screen.findByRole('region', { name: 'MQTT user onboarding' })).toBeInTheDocument()
      expect(window.location.pathname).toBe('/users')
      expect(screen.getByRole('link', { name: /^MQTT users/ })).toHaveAttribute('aria-current', 'page')
    })

    it('reports a disconnected broker as a disconnection, not as missing traffic', async () => {
      renderHome('down-token', { broker: 'disconnected' })

      const health = await screen.findByRole('region', { name: 'Broker health' })
      expect(await within(health).findByText('Disconnected')).toBeInTheDocument()
      expect(within(health).getByText(/MCM cannot reach the broker at mosquitto:1883/)).toBeInTheDocument()
      expect(within(health).queryByText('Connected · no traffic')).not.toBeInTheDocument()
    })

    it('does not claim a disconnection when the broker status cannot be read', async () => {
      renderHome('unknown-token', { statusFails: true })

      const health = await screen.findByRole('region', { name: 'Broker health' })
      expect(await within(health).findByText('Status unavailable')).toBeInTheDocument()
      expect(within(health).getByText(/does not mean the broker is down/)).toBeInTheDocument()
      expect(within(health).queryByText('Disconnected')).not.toBeInTheDocument()
    })

    it('reports recent traffic and the time of the last message when the broker is active', async () => {
      renderHome('active-token', { messageCount: 6, lastMessageAt: new Date(Date.now() - 30_000).toISOString() })

      const health = await screen.findByRole('region', { name: 'Broker health' })
      expect(await within(health).findByText('Connected · receiving traffic')).toBeInTheDocument()
      expect(within(health).getByText('The broker is reachable and clients are publishing: 6 messages observed in the last 5 minutes.')).toBeInTheDocument()
      expect(within(health).getByText('Less than a minute ago')).toBeInTheDocument()
    })

    it('explains to a viewer who can connect an application instead of offering an action they cannot take', async () => {
      renderHome('viewer-token', { role: 'viewer' })

      const connect = await screen.findByRole('region', { name: 'Connect an application' })
      expect(within(connect).getByText('Connecting an application needs the operator role or higher.')).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Connect an application' })).not.toBeInTheDocument()
    })

    it('shows pending changes with the apply action to an admin', async () => {
      window.localStorage.setItem('mcm_deploy_pending', '1')
      renderHome('admin-token', { role: 'admin' })

      const pending = await screen.findByRole('region', { name: 'Pending changes' })
      expect(within(pending).getByText('Saved, not applied')).toBeInTheDocument()
      await userEvent.click(within(pending).getByRole('button', { name: 'Review and apply' }))

      expect(await screen.findByRole('link', { name: /^Deploy/ })).toHaveAttribute('aria-current', 'page')
      expect(window.location.pathname).toBe('/deploy')
    })

    it('shows pending changes with the preview action to an operator and says an admin applies them', async () => {
      window.localStorage.setItem('mcm_deploy_pending', '1')
      renderHome('operator-token', { role: 'operator' })

      const pending = await screen.findByRole('region', { name: 'Pending changes' })
      expect(within(pending).getByText('Saved, not applied')).toBeInTheDocument()
      expect(within(pending).getByRole('button', { name: 'Preview changes' })).toBeInTheDocument()
      expect(within(pending).getByText('Applying them needs the admin role and MFA.')).toBeInTheDocument()
      expect(within(pending).queryByRole('button', { name: 'Review and apply' })).not.toBeInTheDocument()
    })

    it('shows pending changes to a viewer without an action they cannot take', async () => {
      window.localStorage.setItem('mcm_deploy_pending', '1')
      renderHome('viewer-pending-token', { role: 'viewer' })

      const pending = await screen.findByRole('region', { name: 'Pending changes' })
      expect(within(pending).getByText('Saved, not applied')).toBeInTheDocument()
      expect(within(pending).getByText('Previewing them needs the operator role or higher; applying them needs the admin role and MFA.')).toBeInTheDocument()
      expect(within(pending).queryByRole('button')).not.toBeInTheDocument()
    })

    it('says no pending changes are recorded in this browser when the flag is clear', async () => {
      renderHome('clear-token', { role: 'admin' })

      const pending = await screen.findByRole('region', { name: 'Pending changes' })
      expect(within(pending).getByText('Nothing pending in this browser')).toBeInTheDocument()
    })

    it('places broker health, pending changes and the connect action before recent traffic', async () => {
      renderHome('order-token')

      const health = await screen.findByRole('region', { name: 'Broker health' })
      const pending = screen.getByRole('region', { name: 'Pending changes' })
      const connect = screen.getByRole('region', { name: 'Connect an application' })
      const traffic = screen.getByText('Message rate')
      const follows = (a: Element, b: Element) => Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)
      expect(follows(health, pending)).toBe(true)
      expect(follows(pending, connect)).toBe(true)
      expect(follows(connect, traffic)).toBe(true)
    })

    it.each([
      ['/logs', /^Logs/],
      ['/users', /^MQTT users/],
      ['/deploy', /^Deploy/],
    ])('still opens %s on a direct browser load without redirecting to the dashboard', async (path, linkName) => {
      window.history.replaceState(null, '', path)
      renderHome('deep-link-token')

      expect(await screen.findByRole('link', { name: linkName })).toHaveAttribute('aria-current', 'page')
      expect(window.location.pathname).toBe(path)
      expect(screen.queryByRole('region', { name: 'Broker health' })).not.toBeInTheDocument()
    })

    it('does not reopen the onboarding when the user returns to Users from the sidebar', async () => {
      renderHome('reopen-token')

      await userEvent.click(await screen.findByRole('button', { name: 'Connect an application' }))
      expect(await screen.findByRole('region', { name: 'MQTT user onboarding' })).toBeInTheDocument()

      await userEvent.click(screen.getByRole('link', { name: /^Dashboard/ }))
      await screen.findByRole('region', { name: 'Broker health' })
      await userEvent.click(screen.getByRole('link', { name: /^MQTT users/ }))

      expect(await screen.findByRole('button', { name: 'Add User' })).toBeInTheDocument()
      expect(screen.queryByRole('region', { name: 'MQTT user onboarding' })).not.toBeInTheDocument()
    })

    describe('warnings that open filtered Logs (#354 T6)', () => {
      async function streamLogs(entries: Array<['mcm' | 'mosquitto_log', string, string]>) {
        await waitFor(() => expect(MockWebSocket.instances.length).toBeGreaterThan(0))
        // act: the views have rendered every streamed entry when this returns.
        act(() => {
          for (const [source, severity, message] of entries) {
            MockWebSocket.instances[0]?.emit('message', new MessageEvent('message', { data: JSON.stringify({ type: source === 'mcm' ? 'broker_log' : 'broker_log_entry', source, severity, message, observed_at: '2026-01-01T00:00:00Z' }) }))
          }
        })
      }

      function expectAppliedFilter(source: string, severities: string[]) {
        expect(screen.getByRole('combobox', { name: 'Source' })).toHaveValue(source)
        const group = screen.getByRole('group', { name: 'Severity' })
        for (const severity of ['debug', 'info', 'warning', 'error']) {
          expect(within(group).getByRole('button', { name: severity })).toHaveAttribute('aria-pressed', String(severities.includes(severity)))
        }
      }

      it('shows no warning link when there are no associated diagnostic events', async () => {
        renderHome('quiet-down-token', { broker: 'disconnected' })
        const health = await screen.findByRole('region', { name: 'Broker health' })
        expect(await within(health).findByText('Disconnected')).toBeInTheDocument()
        await streamLogs([['mcm', 'info', 'Routine sync'], ['mosquitto_log', 'info', 'New connection'], ['mosquitto_log', 'debug', 'PINGREQ']])

        expect(screen.queryByRole('link', { name: /related MCM event/ })).not.toBeInTheDocument()
        expect(screen.queryByRole('link', { name: /Mosquitto log entr/ })).not.toBeInTheDocument()
        expect(screen.queryByRole('region', { name: 'Diagnostic warnings' })).not.toBeInTheDocument()

        // The Logs view lists the three events, so they had arrived when the
        // home was checked above (nothing was streamed since).
        await userEvent.click(screen.getByRole('link', { name: /^Logs/ }))
        expect(await screen.findByText('Routine sync')).toBeInTheDocument()
        expect(screen.getByText('New connection')).toBeInTheDocument()
        expect(screen.getByText('PINGREQ')).toBeInTheDocument()
      })

      it('does not point at MCM events while the broker is connected', async () => {
        renderHome('healthy-token')
        const health = await screen.findByRole('region', { name: 'Broker health' })
        expect(await within(health).findByText('Connected · no traffic')).toBeInTheDocument()
        await streamLogs([['mcm', 'warning', 'Slow status poll'], ['mosquitto_log', 'error', 'Socket error on sensor-2']])
        // The Mosquitto warning proves the stream was processed.
        expect(await screen.findByRole('link', { name: 'View 1 Mosquitto log entry' })).toBeInTheDocument()

        expect(screen.queryByRole('link', { name: /related MCM event/ })).not.toBeInTheDocument()
      })

      it('opens the MCM warnings and errors from a disconnected broker warning, with the filter visible', async () => {
        renderHome('mcm-warning-token', { broker: 'disconnected' })
        const health = await screen.findByRole('region', { name: 'Broker health' })
        expect(await within(health).findByText('Disconnected')).toBeInTheDocument()
        await streamLogs([['mcm', 'info', 'Routine sync'], ['mcm', 'warning', 'Broker poll timed out'], ['mcm', 'error', 'Broker connection refused'], ['mosquitto_log', 'info', 'Broker info line']])

        const link = await screen.findByRole('link', { name: 'View 2 related MCM events' })
        expect(link).toHaveAttribute('href', '/logs?source=mcm&severity=warning,error')
        await userEvent.click(link)

        expect(await screen.findByRole('link', { name: /^Logs/ })).toHaveAttribute('aria-current', 'page')
        expect(window.location.pathname).toBe('/logs')
        expect(window.location.search).toBe('?source=mcm&severity=warning,error')
        expectAppliedFilter('mcm', ['warning', 'error'])
        expect(screen.getByText('Broker poll timed out')).toBeInTheDocument()
        expect(screen.getByText('Broker connection refused')).toBeInTheDocument()
        expect(screen.queryByText('Routine sync')).not.toBeInTheDocument()
        expect(screen.queryByText('Broker info line')).not.toBeInTheDocument()
      })

      it('also points at MCM events when the broker status cannot be read', async () => {
        renderHome('mcm-unknown-token', { statusFails: true })
        const health = await screen.findByRole('region', { name: 'Broker health' })
        expect(await within(health).findByText('Status unavailable')).toBeInTheDocument()
        await streamLogs([['mcm', 'error', 'Status request failed']])

        expect(await screen.findByRole('link', { name: 'View 1 related MCM event' })).toHaveAttribute('href', '/logs?source=mcm&severity=warning,error')
      })

      it('opens Mosquitto warnings and errors from a broker log warning with its count', async () => {
        renderHome('mosquitto-warning-token')
        await screen.findByRole('region', { name: 'Broker health' })
        await streamLogs([['mosquitto_log', 'warning', 'Client sensor-1 disconnected'], ['mosquitto_log', 'error', 'Socket error on sensor-2'], ['mosquitto_log', 'warning', 'Client sensor-3 timed out'], ['mosquitto_log', 'info', 'New connection from 10.0.0.4'], ['mcm', 'warning', 'MCM warning line']])

        const warnings = await screen.findByRole('region', { name: 'Diagnostic warnings' })
        expect(warnings).toHaveTextContent('Mosquitto logged 3 warnings or errors in the live window.')
        const link = within(warnings).getByRole('link', { name: 'View 3 Mosquitto log entries' })
        expect(link).toHaveAttribute('href', '/logs?source=mosquitto_log&severity=warning,error')
        await userEvent.click(link)

        expect(await screen.findByRole('link', { name: /^Logs/ })).toHaveAttribute('aria-current', 'page')
        expect(window.location.search).toBe('?source=mosquitto_log&severity=warning,error')
        expectAppliedFilter('mosquitto_log', ['warning', 'error'])
        expect(screen.getByText('Socket error on sensor-2')).toBeInTheDocument()
        expect(screen.queryByText('New connection from 10.0.0.4')).not.toBeInTheDocument()
        expect(screen.queryByText('MCM warning line')).not.toBeInTheDocument()
      })

      it('leaves a modified click on a warning link to the browser', async () => {
        renderHome('modified-click-token')
        await screen.findByRole('region', { name: 'Broker health' })
        await streamLogs([['mosquitto_log', 'error', 'Socket error on sensor-2']])
        const link = await screen.findByRole('link', { name: 'View 1 Mosquitto log entry' })
        const user = userEvent.setup()
        await user.keyboard('{Control>}')
        await user.click(link)
        await user.keyboard('{/Control}')
        expect(window.location.pathname).toBe('/')
        expect(screen.getByRole('region', { name: 'Broker health' })).toBeInTheDocument()
      })

      it('opens the sidebar Logs link unfiltered and restores the filtered view on back', async () => {
        renderHome('sidebar-logs-token')
        await screen.findByRole('region', { name: 'Broker health' })
        await streamLogs([['mosquitto_log', 'error', 'Socket error on sensor-2'], ['mcm', 'info', 'Routine sync']])
        await userEvent.click(await screen.findByRole('link', { name: 'View 1 Mosquitto log entry' }))
        expectAppliedFilter('mosquitto_log', ['warning', 'error'])

        await userEvent.click(screen.getByRole('link', { name: /^Logs/ }))
        expect(screen.getByRole('link', { name: /^Logs/ })).toHaveAttribute('href', '/logs')
        expect(window.location.pathname).toBe('/logs')
        expect(window.location.search).toBe('')
        expectAppliedFilter('all', [])
        expect(screen.getByText('Routine sync')).toBeInTheDocument()

        window.history.back()
        await waitFor(() => expect(window.location.search).toBe('?source=mosquitto_log&severity=warning,error'))
        await waitFor(() => expectAppliedFilter('mosquitto_log', ['warning', 'error']))
        expect(screen.queryByText('Routine sync')).not.toBeInTheDocument()

        window.history.back()
        expect(await screen.findByRole('region', { name: 'Broker health' })).toBeInTheDocument()
        expect(window.location.pathname).toBe('/')
      })

      it('applies the filter of a direct load of /logs?severity=error', async () => {
        window.history.replaceState(null, '', '/logs?severity=error')
        renderHome('direct-filter-token')

        expect(await screen.findByRole('link', { name: /^Logs/ })).toHaveAttribute('aria-current', 'page')
        expectAppliedFilter('all', ['error'])
        await streamLogs([['mcm', 'info', 'Routine sync'], ['mcm', 'error', 'Disk full']])
        expect(await screen.findByText('Disk full')).toBeInTheDocument()
        expect(screen.queryByText('Routine sync')).not.toBeInTheDocument()
        expect(window.location.pathname).toBe('/logs')
        expect(window.location.search).toBe('?severity=error')
      })

      it('keeps the URL in step with filter changes without adding history entries', async () => {
        window.history.replaceState(null, '', '/logs')
        renderHome('sync-filter-token')
        expect(await screen.findByRole('link', { name: /^Logs/ })).toHaveAttribute('aria-current', 'page')
        const entries = window.history.length
        const user = userEvent.setup()

        await user.click(within(screen.getByRole('group', { name: 'Severity' })).getByRole('button', { name: 'error' }))
        await user.click(within(screen.getByRole('group', { name: 'Severity' })).getByRole('button', { name: 'warning' }))
        await user.selectOptions(screen.getByRole('combobox', { name: 'Source' }), 'mcm')
        await user.type(screen.getByRole('searchbox', { name: 'Search messages' }), 'disk full')
        expect(window.location.pathname).toBe('/logs')
        expect(window.location.search).toBe('?source=mcm&severity=warning,error&q=disk%20full')

        await user.click(screen.getByRole('button', { name: 'Clear filters' }))
        expect(window.location.search).toBe('')
        expect(window.history.length).toBe(entries)
      })
    })
  })
})
