import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ListenersPanel from './ListenersPanel'

type RouteHandler = (init?: RequestInit) => Response | Promise<Response>

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
    const handler = routes[`${method.toUpperCase()} ${requestUrl}`]
    if (!handler) throw new Error(`Unhandled fetch: ${method} ${requestUrl}`)
    return await handler(init)
  })
}

const initialSpecs = [{ id: 'mqtt', port: 1883, bind: '0.0.0.0', protocols: ['mqtt'] }]
const preview = {
  revision_id: 'revision-123',
  needs_restart: true,
  diff: '--- current\n+++ rendered\n+listener 1883',
  rendered: 'listener 1883 0.0.0.0\nprotocol mqtt\n',
  issues: [],
  warnings: [],
  compose_status: { mapped_ports: [1883], unmapped_ports: [], disabled: false },
}

function baseRoutes(): Record<string, RouteHandler> {
  return {
    'GET /api/v1/listeners': () => jsonResponse({ specs: initialSpecs }),
  }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ListenersPanel', () => {
  it('renders listeners list from initial fetch', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes()))

    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)

    expect(await screen.findByText('1883')).toBeInTheDocument()
    expect(screen.getByText('0.0.0.0')).toBeInTheDocument()
    expect(screen.getByText('mqtt')).toBeInTheDocument()
  })

  it('submitting a preview shows the diff and stores a revision id', async () => {
    vi.stubGlobal('fetch', installFetchMock({
      ...baseRoutes(),
      'POST /api/v1/listeners/preview': () => jsonResponse(preview),
    }))

    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)
    await screen.findByText('1883')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Preview' }))

    expect(await screen.findByText('+++ rendered')).toBeInTheDocument()
    expect(screen.getByText('listener 1883 0.0.0.0')).toBeInTheDocument()
    expect(screen.getByText('revision-123')).toBeInTheDocument()
  })

  it('apply button disabled until preview captured and confirm checked', async () => {
    vi.stubGlobal('fetch', installFetchMock({
      ...baseRoutes(),
      'POST /api/v1/listeners/preview': () => jsonResponse(preview),
    }))

    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)
    const apply = screen.getByRole('button', { name: 'Apply (restart required)' })
    expect(apply).toBeDisabled()

    await screen.findByText('1883')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Preview' }))
    await screen.findByText('revision-123')
    expect(apply).toBeDisabled()

    await userEvent.setup().click(screen.getByRole('checkbox', { name: 'Confirm restart' }))
    expect(apply).toBeEnabled()
  })

  it('apply with confirm succeeds, calls listeners list refresh', async () => {
    const fetchMock = installFetchMock({
      ...baseRoutes(),
      'POST /api/v1/listeners/preview': () => jsonResponse(preview),
      'POST /api/v1/listeners/apply': (init) => {
        expect(init?.body).toBe(JSON.stringify({ revision_id: 'revision-123', confirm: true }))
        return jsonResponse({ revision_id: 'revision-123', applied: true })
      },
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)
    await screen.findByText('1883')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Preview' }))
    await screen.findByText('revision-123')
    await userEvent.setup().click(screen.getByRole('checkbox', { name: 'Confirm restart' }))
    await userEvent.setup().click(screen.getByRole('button', { name: 'Apply (restart required)' }))

    expect(await screen.findByText('Listener configuration applied.')).toBeInTheDocument()
    await waitFor(() => {
      expect(fetchMock.mock.calls.filter(([input]) => input === '/api/v1/listeners')).toHaveLength(2)
    })
  })

  it('shows compose_unmapped (409) as an error', async () => {
    vi.stubGlobal('fetch', installFetchMock({
      ...baseRoutes(),
      'POST /api/v1/listeners/preview': () => jsonResponse({ error: 'listener ports are not mapped by compose' }, { status: 409 }),
    }))

    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)
    await screen.findByText('1883')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Preview' }))

    expect(await screen.findByText('listener ports are not mapped by compose')).toBeInTheDocument()
  })

  it('shows preview validation 400 in the Issues panel', async () => {
    vi.stubGlobal('fetch', installFetchMock({
      ...baseRoutes(),
      'POST /api/v1/listeners/preview': () => jsonResponse({ issues: [{ kind: 'port', listener_id: 'mqtt', message: 'port must be between 1 and 65535' }] }, { status: 400 }),
    }))

    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)
    await screen.findByText('1883')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Preview' }))

    expect(await screen.findByText('Issues')).toBeInTheDocument()
    expect(screen.getByText('port must be between 1 and 65535')).toBeInTheDocument()
  })
})
