import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import MQTTUsersPanel from './MQTTUsersPanel'

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

const baseRoutes: Record<string, RouteHandler> = {
  'GET /api/v1/mqtt-users': () =>
    jsonResponse([
      {
        id: 1,
        username: 'sensor-01',
        disabled: false,
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
      },
    ]),
}

describe('MQTTUsersPanel role-aware controls', () => {
  it('viewer: Add User button is disabled with a requires-role title', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="viewer" />)

    const addBtn = await screen.findByRole('button', { name: 'Add User' })
    expect(addBtn).toBeDisabled()
    expect(addBtn).toHaveAttribute('title', 'Requires operator role or higher')

    vi.unstubAllGlobals()
  })

  it('viewer: Enable/Disable toggle button is disabled for a user row', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="viewer" />)

    await screen.findByText('sensor-01')
    // The toggle button shows "Disable" for an enabled user
    const toggleBtn = screen.getByRole('button', { name: 'Disable' })
    expect(toggleBtn).toBeDisabled()

    vi.unstubAllGlobals()
  })

  it('viewer: Reset password button is disabled', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="viewer" />)

    await screen.findByText('sensor-01')
    const resetBtn = screen.getByRole('button', { name: 'Reset password' })
    expect(resetBtn).toBeDisabled()

    vi.unstubAllGlobals()
  })

  it('viewer: Delete button is disabled', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="viewer" />)

    await screen.findByText('sensor-01')
    const deleteBtn = screen.getByRole('button', { name: 'Delete' })
    expect(deleteBtn).toBeDisabled()

    vi.unstubAllGlobals()
  })

  it('operator: Add User button is enabled', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="operator" />)

    const addBtn = await screen.findByRole('button', { name: 'Add User' })
    expect(addBtn).toBeEnabled()
    expect(addBtn).not.toHaveAttribute('title')

    vi.unstubAllGlobals()
  })

  it('operator: Enable/Disable toggle is enabled', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="operator" />)

    await screen.findByText('sensor-01')
    const toggleBtn = screen.getByRole('button', { name: 'Disable' })
    expect(toggleBtn).toBeEnabled()

    vi.unstubAllGlobals()
  })

  it('operator: Reset password and Delete buttons are enabled', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="operator" />)

    await screen.findByText('sensor-01')
    expect(screen.getByRole('button', { name: 'Reset password' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Delete' })).toBeEnabled()

    vi.unstubAllGlobals()
  })

  it('admin: all mutation buttons are enabled', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="admin" />)

    const addBtn = await screen.findByRole('button', { name: 'Add User' })
    expect(addBtn).toBeEnabled()
    await screen.findByText('sensor-01')
    expect(screen.getByRole('button', { name: 'Disable' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Reset password' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Delete' })).toBeEnabled()

    vi.unstubAllGlobals()
  })

  // FIX 4: auditor tier mirrors viewer (all write buttons disabled + requires-role title)
  it('auditor: Add User button is disabled with a requires-role title', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="auditor" />)

    const addBtn = await screen.findByRole('button', { name: 'Add User' })
    expect(addBtn).toBeDisabled()
    expect(addBtn).toHaveAttribute('title', 'Requires operator role or higher')

    vi.unstubAllGlobals()
  })

  it('auditor: Enable/Disable toggle button is disabled', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="auditor" />)

    await screen.findByText('sensor-01')
    const toggleBtn = screen.getByRole('button', { name: 'Disable' })
    expect(toggleBtn).toBeDisabled()

    vi.unstubAllGlobals()
  })

  it('auditor: Reset password and Delete buttons are disabled', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="auditor" />)

    await screen.findByText('sensor-01')
    expect(screen.getByRole('button', { name: 'Reset password' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled()

    vi.unstubAllGlobals()
  })
})

describe('MQTTUsersPanel 403 forbidden handling', () => {
  // FIX 2: operator triggers a create and gets a 403 → friendly message shown, no logout
  it('shows the friendly forbidden message when create returns 403', async () => {
    const onLogout = vi.fn()
    const routesWithForbiddenCreate: Record<string, RouteHandler> = {
      ...baseRoutes,
      'POST /api/v1/mqtt-users': () =>
        jsonResponse({ error: 'insufficient role' }, { status: 403 }),
    }
    vi.stubGlobal('fetch', installFetchMock(routesWithForbiddenCreate))

    render(<MQTTUsersPanel token="tok" onLogout={onLogout} role="operator" />)

    // Open create form
    const addBtn = await screen.findByRole('button', { name: 'Add User' })
    await userEvent.setup().click(addBtn)

    // Fill in a username and submit
    const input = screen.getByPlaceholderText('device-sensor-01')
    await userEvent.setup().type(input, 'my-device')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Create user' }))

    // The panel must show the friendly 403 message
    expect(await screen.findByText("You don't have permission to perform this action.")).toBeInTheDocument()

    // And must NOT have logged the user out
    expect(onLogout).not.toHaveBeenCalled()

    vi.unstubAllGlobals()
  })

  it('shows the friendly forbidden message when toggle returns 403', async () => {
    const onLogout = vi.fn()
    const routesWithForbiddenToggle: Record<string, RouteHandler> = {
      ...baseRoutes,
      'PUT /api/v1/mqtt-users/1': () =>
        jsonResponse({ error: 'insufficient role' }, { status: 403 }),
    }
    vi.stubGlobal('fetch', installFetchMock(routesWithForbiddenToggle))

    render(<MQTTUsersPanel token="tok" onLogout={onLogout} role="operator" />)

    await screen.findByText('sensor-01')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Disable' }))

    expect(await screen.findByText("You don't have permission to perform this action.")).toBeInTheDocument()
    expect(onLogout).not.toHaveBeenCalled()

    vi.unstubAllGlobals()
  })
})
