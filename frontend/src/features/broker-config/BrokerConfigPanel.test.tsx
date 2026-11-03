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
  'GET /api/v1/broker/config/revisions': () => jsonResponse({ revisions: [] }),
  'POST /api/v1/auth/mfa/step-up': () => jsonResponse({ proof: 'recent-proof', expires_at: new Date(Date.now() + 300000).toISOString() }),
  'GET /api/v1/broker/config': () =>
    jsonResponse({ path: '/etc/mosquitto/mosquitto.conf', body: 'listener 1883\n', hash: 'abc123def4567890', capabilities: { write: true, restart: true } }),
  'GET /api/v1/broker/config/catalog': () =>
    jsonResponse({ version: '2.0', directives: [{ name: 'listener', type: 'integer', scope: 'broker', multiplicity: '0+', reload_kind: 'restart', since_version: '1.0', description: 'Listen for incoming connections' }] }),
  'POST /api/v1/broker/config/import': () =>
    jsonResponse({ revision_id: 'rev-1', base_conf_hash: 'abc123def4567890', has_changes: true, rendered_conf_hash: 'b', conf_diff: '+listener 1883\n-listener 1884\n', conf_body: 'listener 1883\n', reload_kind: 'restart', requires_restart: true, validation_issues: [] }),
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
    await user.type(screen.getByLabelText('MFA code'), '123456')
    await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
    await screen.findByText(/MFA confirmed/)
    await user.click(screen.getByLabelText(/I adopt/))
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
    await user.type(screen.getByLabelText('MFA code'), '123456')
    await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
    await screen.findByText(/MFA confirmed/)
    await user.click(screen.getByLabelText(/I adopt/))
    await user.click(screen.getByRole('button', { name: 'Apply' }))
    await user.click(screen.getByRole('button', { name: 'Force Apply' }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/apply requires broker restart/i)
    })

    vi.unstubAllGlobals()
  })
  it('validates the edited draft against its base and requires MFA before applying the exact revision', async () => {
    const draft = 'max_connections 30\n'
    const fetchMock = installFetchMock({
      ...configRoute,
      'POST /api/v1/broker/config/preview': (init) => {
        expect(JSON.parse(String(init?.body))).toEqual({ body: draft, base_hash: 'abc123def4567890' })
        return jsonResponse({ revision_id: 'edited', base_conf_hash: 'abc123def4567890', conf_body: draft, has_changes: true, conf_diff: '+max_connections 30', reload_kind: 'reload', requires_restart: false, validation_issues: [] })
      },
      'POST /api/v1/auth/mfa/step-up': () => jsonResponse({ proof: 'recent-proof', expires_at: new Date(Date.now() + 300000).toISOString() }),
      'GET /api/v1/broker/config/revisions': () => jsonResponse({ revisions: [] }),
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    render(<BrokerConfigPanel token="tok" onLogout={() => {}} role="admin" />)
    const editor = await screen.findByRole('textbox', { name: 'Draft configuration' })
    await user.clear(editor)
    await user.type(editor, draft)
    await user.click(screen.getByRole('button', { name: 'Validate draft' }))
    await screen.findByText('revision edited')
    expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled()
    await user.type(screen.getByLabelText('MFA code'), '123456')
    await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
    await screen.findByText(/MFA confirmed/)
    await user.click(screen.getByLabelText(/I adopt/))
    await user.click(screen.getByRole('button', { name: 'Apply' }))
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([url]) => url === '/api/v1/broker/config/apply')
      expect(call?.[1]?.headers).toEqual(expect.objectContaining({ 'X-MCM-MFA': 'recent-proof' }))
      expect(JSON.parse(String(call?.[1]?.body))).toEqual({ revision_id: 'edited', force: false, adopt: true })
    })
    vi.unstubAllGlobals()
  })

  it('invalidates a reviewed revision when the draft changes and keeps it after an invalid MFA code', async () => {
    const onLogout = vi.fn()
    const fetchMock = installFetchMock({
      ...configRoute,
      'POST /api/v1/auth/mfa/step-up': () => jsonResponse({ error: 'invalid or reused mfa code' }, { status: 401 }),
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    render(<BrokerConfigPanel token="tok" onLogout={onLogout} role="admin" />)
    const editor = await screen.findByRole('textbox', { name: 'Draft configuration' })
    await user.click(screen.getByRole('button', { name: 'Import / Refresh preview' }))
    await screen.findByText('revision rev-1')
    await user.type(editor, '# unsaved edit')
    expect(screen.queryByText('revision rev-1')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Apply' })).not.toBeInTheDocument()
    await user.type(screen.getByLabelText('MFA code'), '123456')
    await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
    await screen.findByText('invalid or reused mfa code')
    expect(onLogout).not.toHaveBeenCalled()
    expect(editor).toHaveValue('listener 1883\n# unsaved edit')
    expect(fetchMock.mock.calls.some(([url]) => url === '/api/v1/broker/config/apply')).toBe(false)
    vi.unstubAllGlobals()
  })

  it('prepares recovery in Changes without applying automatically', async () => {
    const fetchMock = installFetchMock({
      ...configRoute,
      'GET /api/v1/broker/config/revisions': () => jsonResponse({ revisions: [{ id: 'previous', actor: 'admin', created_at: '2026-01-01', status: 'active_verified' }] }),
      'POST /api/v1/broker/config/revisions/previous/recover': (init) => {
        expect(init?.headers).toEqual(expect.objectContaining({ 'X-MCM-MFA': 'recent-proof' }))
        expect(JSON.parse(String(init?.body))).toEqual({ base_hash: 'abc123def4567890' })
        return jsonResponse({ revision_id: 'recovery', base_conf_hash: 'abc123def4567890', conf_body: 'listener 1883\n', conf_diff: '', reload_kind: 'none', validation_issues: [] })
      },
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    render(<BrokerConfigPanel token="tok" onLogout={() => {}} role="admin" view="changes" />)
    const recover = await screen.findByRole('button', { name: 'Recover revision previous' })
    expect(recover).toBeDisabled()
    await user.type(screen.getByLabelText('MFA code'), '123456')
    await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
    await screen.findByText(/MFA confirmed/)
    await user.click(recover)
    await screen.findByText('revision recovery')
    expect(screen.getByText(/Root configuration only/)).toBeInTheDocument()
    expect(fetchMock.mock.calls.some(([url]) => url === '/api/v1/broker/config/apply')).toBe(false)
    vi.unstubAllGlobals()
  })

})
