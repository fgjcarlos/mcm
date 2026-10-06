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

const initialSpecs = [{ id: 'mqtt', port: 1883, bind: '0.0.0.0', protocols: ['mqtt'] }]
const preview = { revision_id: 'revision-123', needs_restart: false, diff: '', rendered: '', issues: [], warnings: [] }

function renderPanel(composeHostPorts: number[] = []) {
  vi.stubGlobal('fetch', installFetchMock({
    'GET /api/v1/listeners': () => jsonResponse({ specs: initialSpecs }),
    'POST /api/v1/listeners/preview': () => jsonResponse(preview),
  }))
  return render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" composeHostPorts={composeHostPorts} />)
}

afterEach(() => vi.unstubAllGlobals())

describe('ListenersPanel editor', () => {
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
