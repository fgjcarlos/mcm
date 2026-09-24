import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import BrokerConfigPanel from './BrokerConfigPanel'

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
    const key = `${method.toUpperCase()} ${requestUrl}`
    const handler = routes[key]
    if (!handler) throw new Error(`Unhandled fetch: ${key}`)
    return await handler(init)
  })
}

const configRoute: Record<string, RouteHandler> = {
  'GET /api/v1/broker/config': () =>
    jsonResponse({ path: '/etc/mosquitto/mosquitto.conf', body: 'listener 1883\n', hash: 'abc123def4567890' }),
  'GET /api/v1/broker/config/catalog': () =>
    jsonResponse({ version: '2.0', directives: [{ name: 'listener', type: 'integer', scope: 'broker', multiplicity: '0+', reload_kind: 'restart', since_version: '1.0', description: 'Listen for incoming connections' }] }),
  'POST /api/v1/broker/config/import': () =>
    jsonResponse({ revision_id: 'rev-1', base_conf_hash: 'a', rendered_conf_hash: 'b', conf_diff: '+listener 1883\n-listener 1884\n', conf_body: 'listener 1883\n', reload_kind: 'restart', requires_restart: true, validation_issues: [] }),
  'POST /api/v1/broker/config/apply': () => jsonResponse({ id: 42, status: 'applied', created_at: '2026-01-01T00:00:00Z' }),
}

describe('BrokerConfigPanel', () => {
  it('renders the loading state initially', () => {
    vi.stubGlobal('fetch', installFetchMock({}))
    render(<BrokerConfigPanel token="tok" onLogout={() => {}} role="admin" />)
    expect(screen.getByText(/loading broker configuration/i)).toBeInTheDocument()
    vi.unstubAllGlobals()
  })

  it('renders config, catalog and preview after fetches resolve', async () => {
    vi.stubGlobal('fetch', installFetchMock(configRoute))
    render(<BrokerConfigPanel token="tok" onLogout={() => {}} role="admin" />)

    await waitFor(() => {
      expect(screen.getAllByText('/etc/mosquitto/mosquitto.conf').length).toBeGreaterThan(0)
    })
    expect(screen.getByRole('button', { name: 'Import / Refresh preview' })).toBeInTheDocument()
    vi.unstubAllGlobals()
  })

  it('calls apply with the right payload when Apply is clicked', async () => {
    const fetchMock = installFetchMock(configRoute)
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()

    render(<BrokerConfigPanel token="tok" onLogout={() => {}} role="admin" />)

    await waitFor(() => {
      expect(screen.getAllByText('/etc/mosquitto/mosquitto.conf').length).toBeGreaterThan(0)
    })
    await user.click(screen.getByRole('button', { name: 'Import / Refresh preview' }))
    await waitFor(() => {
      expect(screen.getByText('Preview')).toBeInTheDocument()
    })
    await user.click(screen.getByRole('button', { name: 'Apply' }))
    await user.click(screen.getByRole('button', { name: 'Force Apply' }))

    await waitFor(() => {
      const applyCalls = fetchMock.mock.calls.filter((call) => {
        const url = typeof call[0] === 'string' ? call[0] : (call[0] as Request).url
        return url === '/api/v1/broker/config/apply'
      })
      expect(applyCalls.length).toBeGreaterThan(0)
      const lastCall = applyCalls[applyCalls.length - 1]
      const body = JSON.parse(String(lastCall[1]?.body))
      expect(body).toEqual({ revision_id: 'rev-1', force: true, adopt: true })
    })

    vi.unstubAllGlobals()
  })

  it('surfaces 409 requires_restart errors as an inline error banner', async () => {
    const routes: Record<string, RouteHandler> = {
      ...configRoute,
      'POST /api/v1/broker/config/apply': () =>
        jsonResponse({ error: 'apply requires broker restart', directives: 'listener,persistence' }, { status: 409 }),
    }
    vi.stubGlobal('fetch', installFetchMock(routes))
    const user = userEvent.setup()

    render(<BrokerConfigPanel token="tok" onLogout={() => {}} role="admin" />)
    await waitFor(() => {
      expect(screen.getAllByText('/etc/mosquitto/mosquitto.conf').length).toBeGreaterThan(0)
    })

    await user.click(screen.getByRole('button', { name: 'Import / Refresh preview' }))
    await waitFor(() => {
      expect(screen.getByText('Preview')).toBeInTheDocument()
    })
    await user.click(screen.getByRole('button', { name: 'Apply' }))
    await user.click(screen.getByRole('button', { name: 'Force Apply' }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/apply requires broker restart/i)
    })

    vi.unstubAllGlobals()
  })
})
