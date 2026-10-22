import { render, screen, waitFor } from '@testing-library/react'
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
    await waitFor(() => expect(screen.getByRole('button', { name: 'Apply (restart required)' })).toBeEnabled())
  })
})
