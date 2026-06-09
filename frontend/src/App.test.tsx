import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'

type RouteHandler = (init?: RequestInit) => Response | Promise<Response>

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
    const handler = routes[key]

    if (!handler) {
      throw new Error(`Unhandled fetch: ${key}`)
    }

    return await handler(init)
  })
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
          topic: 'factory/line-1/temperature',
          payload_preview: '{"temperature":21.5}',
          payload_format: 'json',
          payload_bytes: 20,
          observed_at: '2026-01-01T00:02:00Z',
        }),
      }),
    )

    expect(await screen.findByText('{"temperature":21.5}')).toBeInTheDocument()
    expect(screen.getByText('json')).toBeInTheDocument()
    expect(screen.getByText('20')).toBeInTheDocument()
  })

  it('restores an operator session and loads the MQTT users view', async () => {
    window.localStorage.setItem('mcm_admin_token', 'restored-token')

    const fetchMock = installFetchMock({
      ...authenticatedRoutes('restored-token'),
      'GET /api/v1/mqtt-users': (init) => {
        expect(init?.headers).toEqual(expect.objectContaining({ Authorization: 'Bearer restored-token' }))

        return jsonResponse([
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
    await user.click(screen.getByRole('button', { name: /users/i }))

    await screen.findByText('device-sensor-01')

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/mqtt-users',
        expect.objectContaining({
          headers: expect.objectContaining({ Authorization: 'Bearer restored-token' }),
        }),
      )
    })
  })

  it('restores an operator session and loads the ACL policy workspace', async () => {
    window.localStorage.setItem('mcm_admin_token', 'acl-token')

    const fetchMock = installFetchMock({
      ...authenticatedRoutes('acl-token'),
      'GET /api/v1/acls': (init) => {
        expect(init?.headers).toEqual(expect.objectContaining({ Authorization: 'Bearer acl-token' }))

        return jsonResponse({
          rules: [
            {
              id: 'rule-1',
              principal: 'device-writer',
              topic_filter: 'factory/line-1/#',
              permission: 'write',
              description: 'Line 1 telemetry writer',
            },
          ],
        })
      },
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)

    await screen.findByText('Signed in')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /acls/i }))

    await screen.findByRole('heading', { name: 'ACL policy workspace' })
    expect(await screen.findByText('device-writer')).toBeInTheDocument()
    expect(screen.getByText('factory/line-1/#')).toBeInTheDocument()
    expect(screen.getByText('write')).toBeInTheDocument()

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/acls',
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
    await user.click(screen.getByRole('button', { name: /deploy/i }))

    await screen.findByRole('heading', { name: 'Mosquitto configuration deploy' })
    expect(await screen.findByText('Not configured')).toBeInTheDocument()
    expect(screen.getByText('Deploy functionality is not configured on this MCM instance.')).toBeInTheDocument()
  })

  it('navigates across ACL and deploy panels in an authenticated session', async () => {
    window.localStorage.setItem('mcm_admin_token', 'multi-panel-token')

    const fetchMock = installFetchMock({
      ...authenticatedRoutes('multi-panel-token'),
      'GET /api/v1/acls': () =>
        jsonResponse({
          rules: [
            {
              id: 'rule-2',
              principal: 'analytics-reader',
              topic_filter: 'analytics/+/state',
              permission: 'read',
            },
          ],
        }),
      'GET /api/v1/deployments': () =>
        jsonResponse({
          deployments: [
            {
              id: 'dep-2026-01-01',
              status: 'applied',
              message: 'Configuration applied successfully.',
              created_at: '2026-01-01T00:00:00Z',
            },
          ],
        }),
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)

    await screen.findByText('Signed in')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /acls/i }))
    expect(await screen.findByText('analytics-reader')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /deploy/i }))
    expect(await screen.findByText('Configuration preview')).toBeInTheDocument()
    expect(await screen.findByText('Configuration applied successfully.')).toBeInTheDocument()

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/acls',
        expect.objectContaining({
          headers: expect.objectContaining({ Authorization: 'Bearer multi-panel-token' }),
        }),
      )
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/deployments',
        expect.objectContaining({
          headers: expect.objectContaining({ Authorization: 'Bearer multi-panel-token' }),
        }),
      )
    })
  })
})
