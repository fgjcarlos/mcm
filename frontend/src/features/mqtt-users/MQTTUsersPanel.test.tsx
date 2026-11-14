import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import MQTTUsersPanel from './MQTTUsersPanel'

type RouteHandler = (init?: RequestInit, url?: string) => Response | Promise<Response>

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
    const handler = routes[key] ?? routes[key.split('?')[0]]
    if (!handler) throw new Error(`Unhandled fetch: ${key}`)
    return await handler(init, requestUrl)
  })
}

type UserRow = { id: number; username: string; disabled: boolean; created_at: string; updated_at: string }

function userRow(id: number, username: string): UserRow {
  return { id, username, disabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }
}

// Answers like the paged endpoint of S10/S10b: filters by `q` (case-insensitive
// substring of the username), echoes limit/offset and counts the matches.
function pagedUsers(all: UserRow[]): RouteHandler {
  return (_init, url) => {
    const params = new URL(url ?? '', 'http://localhost').searchParams
    const q = (params.get('q') ?? '').toLowerCase()
    const matches = all.filter((user) => user.username.toLowerCase().includes(q))
    const limit = Number(params.get('limit') ?? 50)
    const offset = Number(params.get('offset') ?? 0)
    return jsonResponse({ users: matches.slice(offset, offset + limit), total: matches.length, limit, offset })
  }
}

function requestedUrls(fetchMock: ReturnType<typeof installFetchMock>) {
  return fetchMock.mock.calls.map(([input]) => String(input))
}

beforeEach(() => {
  window.history.replaceState(null, '', '/users')
})

afterEach(() => {
  vi.unstubAllGlobals()
})

const baseRoutes: Record<string, RouteHandler> = {
  'GET /api/v1/mqtt-users': pagedUsers([userRow(1, 'sensor-01')]),
}

describe('MQTTUsersPanel search, paging and empty states', () => {
  it('finds a user by name through the server search instead of paging (S6)', async () => {
    const all = Array.from({ length: 200 }, (_, i) => userRow(i + 1, `device-${String(i + 1).padStart(3, '0')}`))
    const fetchMock = installFetchMock({ 'GET /api/v1/mqtt-users': pagedUsers(all) })
    vi.stubGlobal('fetch', fetchMock)

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="viewer" />)

    expect(await screen.findByText('device-001')).toBeInTheDocument()
    expect(screen.getByText('200 users')).toBeInTheDocument()
    expect(screen.queryByText('device-187')).not.toBeInTheDocument()

    await userEvent.setup().type(screen.getByRole('searchbox', { name: 'Search users' }), 'DEVICE-187')

    expect(await screen.findByText('device-187')).toBeInTheDocument()
    expect(screen.queryByText('device-001')).not.toBeInTheDocument()
    expect(screen.getByText('1 user')).toBeInTheDocument()
    expect(requestedUrls(fetchMock)).toContain('/api/v1/mqtt-users?q=DEVICE-187&limit=25&offset=0')
    // No request downloads the full listing.
    expect(requestedUrls(fetchMock).every((url) => url.includes('limit=25'))).toBe(true)
  })

  it('shows the page position and total, and moves between pages by offset (S8)', async () => {
    const all = Array.from({ length: 60 }, (_, i) => userRow(i + 1, `user-${String(i + 1).padStart(2, '0')}`))
    const fetchMock = installFetchMock({ 'GET /api/v1/mqtt-users': pagedUsers(all) })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="viewer" />)

    expect(await screen.findByText('user-01')).toBeInTheDocument()
    expect(screen.getByText('Page 1 of 3')).toBeInTheDocument()
    expect(screen.getByText('60 users')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Next page' })).toBeEnabled()

    await user.click(screen.getByRole('button', { name: 'Next page' }))
    expect(await screen.findByText('user-26')).toBeInTheDocument()
    expect(screen.queryByText('user-25')).not.toBeInTheDocument()
    expect(screen.getByText('Page 2 of 3')).toBeInTheDocument()
    expect(requestedUrls(fetchMock)).toContain('/api/v1/mqtt-users?limit=25&offset=25')
    expect(screen.getByRole('button', { name: 'Previous page' })).toBeEnabled()

    await user.click(screen.getByRole('button', { name: 'Next page' }))
    expect(await screen.findByText('user-60')).toBeInTheDocument()
    expect(screen.getByText('Page 3 of 3')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Next page' })).toBeDisabled()

    await user.click(screen.getByRole('button', { name: 'Previous page' }))
    expect(await screen.findByText('user-26')).toBeInTheDocument()
    expect(screen.getByText('Page 2 of 3')).toBeInTheDocument()
  })

  it('tells an empty installation apart from a search without matches (S16)', async () => {
    vi.stubGlobal('fetch', installFetchMock({ 'GET /api/v1/mqtt-users': pagedUsers([]) }))
    const first = render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="viewer" />)
    expect(await screen.findByText('No MQTT users configured yet.')).toBeInTheDocument()
    expect(screen.queryByText('No MQTT users match the search.')).not.toBeInTheDocument()
    first.unmount()

    vi.stubGlobal('fetch', installFetchMock({ 'GET /api/v1/mqtt-users': pagedUsers([userRow(1, 'sensor-01')]) }))
    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="viewer" />)
    await screen.findByText('sensor-01')
    await userEvent.setup().type(screen.getByRole('searchbox', { name: 'Search users' }), 'zzz')
    expect(await screen.findByText('No MQTT users match the search.')).toBeInTheDocument()
    expect(screen.queryByText('No MQTT users configured yet.')).not.toBeInTheDocument()
  })

  it('opens with the search and page kept in the URL (S14)', async () => {
    window.history.replaceState(null, '', '/users?users.q=user&users.page=2')
    const all = Array.from({ length: 30 }, (_, i) => userRow(i + 1, `user-${String(i + 1).padStart(2, '0')}`))
    const fetchMock = installFetchMock({ 'GET /api/v1/mqtt-users': pagedUsers(all) })
    vi.stubGlobal('fetch', fetchMock)

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="viewer" />)

    expect(await screen.findByText('user-26')).toBeInTheDocument()
    expect(screen.getByRole('searchbox', { name: 'Search users' })).toHaveValue('user')
    expect(screen.getByText('Page 2 of 2')).toBeInTheDocument()
    expect(requestedUrls(fetchMock)).toEqual(['/api/v1/mqtt-users?q=user&limit=25&offset=25'])
  })

  it('shows the load error of the listing', async () => {
    vi.stubGlobal('fetch', installFetchMock({ 'GET /api/v1/mqtt-users': () => jsonResponse({ error: 'storage unavailable' }, { status: 500 }) }))
    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="viewer" />)
    expect(await screen.findByRole('alert')).toHaveTextContent('storage unavailable')
    expect(screen.queryByText('No MQTT users configured yet.')).not.toBeInTheDocument()
  })
})

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

  it('notifies the dashboard after a successful user mutation', async () => {
    const onMutation = vi.fn()
    const routesWithSuccessfulToggle: Record<string, RouteHandler> = {
      ...baseRoutes,
      'PUT /api/v1/mqtt-users/1': () => jsonResponse({}),
    }
    vi.stubGlobal('fetch', installFetchMock(routesWithSuccessfulToggle))

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="operator" onMutation={onMutation} />)

    await screen.findByText('sensor-01')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Disable' }))

    await waitFor(() => {
      expect(onMutation).toHaveBeenCalledTimes(1)
    })
    vi.unstubAllGlobals()
  })

  it('opens the onboarding journey from Add User', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="operator" />)

    await userEvent.setup().click(await screen.findByRole('button', { name: 'Add User' }))

    // The journey is one place for the user, its access rule, the preview and the apply.
    expect(screen.getByRole('region', { name: 'MQTT user onboarding' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create user' })).toBeInTheDocument()

    vi.unstubAllGlobals()
  })

  it('opens the onboarding journey at the access rule step for an existing user', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="operator" />)

    await screen.findByText('sensor-01')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Complete setup' }))

    expect(screen.getByRole('region', { name: 'MQTT user onboarding' })).toBeInTheDocument()
    // The rule step starts from the existing identity.
    expect(within(screen.getByRole('region', { name: 'MQTT user onboarding' })).getByText('sensor-01')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Create user' })).not.toBeInTheDocument()

    vi.unstubAllGlobals()
  })

  it('keeps the complete setup control disabled for a viewer', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<MQTTUsersPanel token="tok" onLogout={() => {}} role="viewer" />)

    await screen.findByText('sensor-01')
    const setupBtn = screen.getByRole('button', { name: 'Complete setup' })
    expect(setupBtn).toBeDisabled()
    expect(setupBtn).toHaveAttribute('title', 'Requires operator role or higher')

    vi.unstubAllGlobals()
  })
})
