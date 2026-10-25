import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import BridgesPanel from './BridgesPanel'

type RouteHandler = (init?: RequestInit) => Response | Promise<Response>

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
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

const storedBridge = {
  name: 'upstream',
  address: 'remote.example.org:1884',
  remote_client_id: 'mcm-upstream',
  username: 'mcm',
  password_set: true,
  try_private: true,
  topics: [{ topic: 'both/#', direction: 'both', qos: 1 }],
}

describe('BridgesPanel', () => {
  const originalFetch = globalThis.fetch

  beforeEach(() => {
    vi.restoreAllMocks()
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
  })

  it('shows the bridges of the managed configuration', async () => {
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/bridges': () => jsonResponse({ bridges: [storedBridge] }),
    }) as unknown as typeof fetch

    render(<BridgesPanel token="token" onLogout={() => {}} role="auditor" />)

    await waitFor(() => expect(screen.getByLabelText('Address upstream')).toHaveValue('remote.example.org:1884'))
    expect(screen.getByLabelText('Name upstream')).toHaveValue('upstream')
    expect(screen.getByLabelText('Topic upstream 1')).toHaveValue('both/#')
  })

  it('never shows a stored password', async () => {
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/bridges': () => jsonResponse({ bridges: [storedBridge] }),
    }) as unknown as typeof fetch

    render(<BridgesPanel token="token" onLogout={() => {}} role="auditor" />)

    await waitFor(() => expect(screen.getByLabelText('Password upstream')).toHaveValue(''))
    expect(screen.getByText(/A password is stored/)).toBeInTheDocument()
  })

  it('refuses the preview to an auditor', async () => {
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/bridges': () => jsonResponse({ bridges: [storedBridge] }),
    }) as unknown as typeof fetch

    render(<BridgesPanel token="token" onLogout={() => {}} role="auditor" />)

    await waitFor(() => expect(screen.getByLabelText('Address upstream')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Preview' })).toBeDisabled()
  })

  it('shows the validation findings before the operator previews', async () => {
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/bridges': () => jsonResponse({ bridges: [storedBridge] }),
    }) as unknown as typeof fetch

    render(<BridgesPanel token="token" onLogout={() => {}} role="operator" />)

    await waitFor(() => expect(screen.getByLabelText('Address upstream')).toBeInTheDocument())
    const address = screen.getByLabelText('Address upstream')
    await userEvent.clear(address)
    await userEvent.type(address, 'not a host')

    // The finding is shown in the summary list and on the field itself.
    expect(await screen.findAllByText(/the remote broker address is not a valid host or host:port/)).not.toHaveLength(0)
    expect(screen.getByRole('button', { name: 'Preview' })).toBeDisabled()
  })

  it('previews the edited bridges and shows the diff', async () => {
    const previewBody = {
      revision_id: 'revision-1',
      kind: 'bridges',
      reload_kind: 'restart',
      requires_restart: true,
      conf_diff: '+connection upstream\n+    address other.example.org:1884',
      changed_directives: ['address'],
    }
    let previewRequest: { bridges?: unknown[] } | null = null
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/bridges': () => jsonResponse({ bridges: [storedBridge] }),
      'POST /api/v1/broker/bridges/preview': (init) => {
        previewRequest = JSON.parse(String(init?.body ?? '{}'))
        return jsonResponse(previewBody)
      },
    }) as unknown as typeof fetch

    render(<BridgesPanel token="token" onLogout={() => {}} role="operator" />)

    await waitFor(() => expect(screen.getByLabelText('Address upstream')).toBeInTheDocument())
    const address = screen.getByLabelText('Address upstream')
    await userEvent.clear(address)
    await userEvent.type(address, 'other.example.org:1884')
    await userEvent.click(screen.getByRole('button', { name: 'Preview' }))

    expect(await screen.findByText(/revision revision-1/)).toBeInTheDocument()
    expect(screen.getByText(/Mosquitto reads a bridge at start-up/)).toBeInTheDocument()
    expect(previewRequest).not.toBeNull()
    const sent = (previewRequest as unknown as { bridges: Array<{ address: string }> }).bridges
    expect(sent[0].address).toBe('other.example.org:1884')
  })

  it('applies the reviewed revision with a restart confirmation', async () => {
    let applyRequest: { revision_id?: string; force?: boolean } | null = null
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/bridges': () => jsonResponse({ bridges: [storedBridge] }),
      'POST /api/v1/broker/bridges/preview': () =>
        jsonResponse({
          revision_id: 'revision-1',
          kind: 'bridges',
          reload_kind: 'restart',
          requires_restart: true,
          conf_diff: '+connection upstream',
        }),
      'POST /api/v1/broker/bridges/apply': (init) => {
        applyRequest = JSON.parse(String(init?.body ?? '{}'))
        return jsonResponse({ id: 7, status: 'active_verified', kind: 'bridges' })
      },
    }) as unknown as typeof fetch

    render(<BridgesPanel token="token" onLogout={() => {}} role="admin" />)

    await waitFor(() => expect(screen.getByLabelText('Address upstream')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Preview' }))

    const applyButton = await screen.findByRole('button', { name: 'Apply' })
    expect(applyButton).toBeDisabled()

    await userEvent.click(screen.getByLabelText('Confirm broker restart'))
    expect(applyButton).toBeEnabled()
    await userEvent.click(applyButton)

    await waitFor(() => expect(applyRequest).not.toBeNull())
    expect((applyRequest as unknown as { revision_id: string }).revision_id).toBe('revision-1')
    expect((applyRequest as unknown as { force: boolean }).force).toBe(true)
    expect(await screen.findByText(/Bridges applied/)).toBeInTheDocument()
  })

  it('adds a bridge to the draft', async () => {
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/bridges': () => jsonResponse({ bridges: [] }),
    }) as unknown as typeof fetch

    render(<BridgesPanel token="token" onLogout={() => {}} role="operator" />)

    await waitFor(() => expect(screen.getByText(/No bridge is configured/)).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Add bridge' }))
    expect(await screen.findByLabelText('Name bridge-1')).toHaveValue('bridge-1')
    expect(screen.getByText(/Unsaved changes/)).toBeInTheDocument()
  })
})
