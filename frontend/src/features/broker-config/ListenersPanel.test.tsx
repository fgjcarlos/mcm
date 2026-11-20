import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ListenersPanel from './ListenersPanel'

type RouteHandler = (init?: RequestInit) => Response | Promise<Response>

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
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

const initialSpecs = [{ ID: 'mqtt', Port: 1883, Bind: '0.0.0.0', Protocols: ['mqtt'] }]
const preview = { revision_id: 'revision-123', needs_restart: false, diff: '', rendered: '', issues: [], warnings: [] }

function renderPanel(composeHostPorts: number[] = []) {
  vi.stubGlobal('fetch', installFetchMock({
    'GET /api/v1/listeners': () => jsonResponse({ specs: initialSpecs }),
    'GET /api/v1/broker/certificates': () => jsonResponse({ profiles: [] }),
    'POST /api/v1/listeners/preview': () => jsonResponse(preview),
    'POST /api/v1/auth/mfa/step-up': () => jsonResponse({ proof: 'recent-proof', expires_at: new Date(Date.now() + 300000).toISOString() }),
  }))
  return render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" composeHostPorts={composeHostPorts} />)
}

afterEach(() => vi.unstubAllGlobals())

describe('ListenersPanel editor', () => {
  it('keeps Add disabled until the initial listener fetch resolves', async () => {
    let resolveResponse!: (response: Response) => void
    const pendingResponse = new Promise<Response>((resolve) => { resolveResponse = resolve })
    vi.stubGlobal('fetch', installFetchMock({
      'GET /api/v1/listeners': () => pendingResponse,
      'GET /api/v1/broker/certificates': () => jsonResponse({ profiles: [] }),
      'POST /api/v1/listeners/preview': () => jsonResponse(preview),
    }))
    const user = userEvent.setup()
    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)

    const addButton = screen.getByRole('button', { name: 'Add listener' })
    expect(addButton).toBeDisabled()
    await user.click(addButton)
    expect(screen.getByText('Loading listeners…')).toBeInTheDocument()

    resolveResponse(jsonResponse({ specs: initialSpecs }))
    expect(await screen.findByLabelText('Port mqtt')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add listener' })).toBeEnabled()
    expect(screen.getByText('Saved')).toBeInTheDocument()
    expect(screen.getAllByLabelText(/Port/)).toHaveLength(1)
  })

  it('adds, edits, and deletes listener rows and marks edits dirty', async () => {
    const user = userEvent.setup()
    renderPanel()
    await screen.findByLabelText('Port mqtt')

    expect(screen.getByText('Saved')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Add listener' }))
    const ports = screen.getAllByLabelText(/Port/)
    expect(ports).toHaveLength(2)
    await user.clear(ports[0])
    await user.type(ports[0], '1884')
    expect(ports[0]).toHaveValue('1884')
    expect(screen.getByText('Unsaved changes')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Remove listener mqtt' }))
    expect(screen.getAllByLabelText(/Port/)).toHaveLength(1)
  })

  it('shows inline issues for invalid ports and blocks Apply', async () => {
    const user = userEvent.setup()
    renderPanel()
    const port = await screen.findByLabelText('Port mqtt')
    await user.clear(port)
    await user.type(port, '70000')

    expect((await screen.findAllByText('port must be between 1 and 65535')).length).toBeGreaterThan(0)
    expect(screen.getByRole('button', { name: 'Apply (restart required)' })).toBeDisabled()
  })

  it('shows a warning cue when a listener port collides with a compose host port', async () => {
    renderPanel([1883])
    await screen.findByLabelText('Port mqtt')

    expect(screen.getByText('Port 1883 conflicts with a Compose host port')).toBeInTheDocument()
  })

  it('binds a certificate profile to a listener and sends it in the preview', async () => {
    const previewBodies: string[] = []
    vi.stubGlobal('fetch', installFetchMock({
      'GET /api/v1/listeners': () => jsonResponse({ specs: initialSpecs }),
      'GET /api/v1/broker/certificates': () => jsonResponse({
        profiles: [{ name: 'production', active_revision: 'rev-active', expiry_status: 'valid', revisions: [] }],
      }),
      'POST /api/v1/listeners/preview': (init) => {
        previewBodies.push(String(init?.body))
        return jsonResponse(preview)
      },
    }))
    const user = userEvent.setup()
    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)
    await screen.findByLabelText('Port mqtt')

    const select = await screen.findByLabelText('TLS profile mqtt')
    expect(screen.queryByLabelText('Require client certificate mqtt')).not.toBeInTheDocument()
    await user.selectOptions(select, 'production')

    const mutualTls = await screen.findByLabelText('Require client certificate mqtt')
    await user.click(mutualTls)
    await user.click(screen.getByRole('button', { name: 'Preview' }))
    await screen.findByText('revision-123')

    const body = JSON.parse(previewBodies[0]) as { specs: Array<{ tls_profile?: string; Options?: Record<string, string> }> }
    expect(body.specs[0].tls_profile).toBe('production')
    expect(body.specs[0].Options?.require_certificate).toBe('true')
  })

  it('keeps collision warnings non-blocking for Apply', async () => {
    const user = userEvent.setup()
    renderPanel([1883])
    await screen.findByLabelText('Port mqtt')
    await user.click(screen.getByRole('button', { name: 'Preview' }))
    await screen.findByText('revision-123')
    expect(screen.getByText('Port 1883 conflicts with a Compose host port')).toBeInTheDocument()
    await user.type(screen.getByLabelText('MFA code'), '123456')
    await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Apply (restart required)' })).toBeEnabled())
  })

  it('pages, edits, adds and filters the listener rows and keeps the issues listed', async () => {
    // l12 sits on page 2 with an invalid port: the Issues summary must still name it from page 1.
    const specs = Array.from({ length: 12 }, (_, i) => ({ ID: `l${String(i + 1).padStart(2, '0')}`, Port: i === 11 ? 70000 : 1801 + i, Bind: '0.0.0.0', Protocols: ['mqtt'] }))
    vi.stubGlobal('fetch', installFetchMock({
      'GET /api/v1/listeners': () => jsonResponse({ specs }),
      'GET /api/v1/broker/certificates': () => jsonResponse({ profiles: [] }),
    }))
    const user = userEvent.setup()
    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)

    expect(await screen.findByLabelText('Port l01')).toHaveValue('1801')
    expect(screen.getByLabelText('Port l10')).toBeInTheDocument()
    expect(screen.queryByLabelText('Port l11')).not.toBeInTheDocument()
    expect(screen.getAllByText('port must be between 1 and 65535')).toHaveLength(1)
    const pages = screen.getByRole('navigation', { name: 'listeners pages' })
    expect(pages).toHaveTextContent('Page 1 of 2')

    await user.click(within(pages).getByRole('button', { name: 'Next page' }))
    expect(screen.queryByLabelText('Port l01')).not.toBeInTheDocument()
    const bind = screen.getByLabelText('Bind l11')
    await user.clear(bind)
    await user.type(bind, '127.0.0.1')
    expect(screen.getByLabelText('Bind l11')).toHaveValue('127.0.0.1')

    await user.click(within(pages).getByRole('button', { name: 'Previous page' }))
    expect(screen.getByLabelText('Bind l01')).toHaveValue('0.0.0.0')
    await user.click(screen.getByRole('button', { name: 'Add listener' }))
    expect(screen.getByLabelText('Port listener-1')).toHaveValue('1883')
    expect(screen.getByRole('navigation', { name: 'listeners pages' })).toHaveTextContent('Page 2 of 2')

    await user.type(screen.getByLabelText('Filter listeners by port, bind or protocol'), '1805')
    expect(screen.getByLabelText('Port l05')).toBeInTheDocument()
    expect(screen.queryByLabelText('Port listener-1')).not.toBeInTheDocument()
  })
})
