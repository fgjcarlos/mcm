import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import EffectivePermissionsPanel from './EffectivePermissionsPanel'

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
  'GET /api/v1/acls/scopes': () =>
    jsonResponse({
      scopes: [
        { scope: 'global', path: '/mosquitto/config/acl', rule_count: 2, wired: true },
        {
          scope: 'listener',
          listener_id: 'tls',
          path: '/mosquitto/config/acl.tls',
          rule_count: 1,
          wired: false,
          wiring_issue: 'listener "tls" does not declare acl_file "/mosquitto/config/acl.tls"',
        },
      ],
    }),
  'GET /api/v1/acls/effective': () =>
    jsonResponse({
      scopes: [
        {
          scope: 'global',
          path: '/mosquitto/config/acl',
          identities: [
            {
              identity: 'alice',
              kind: 'user',
              grants: [
                {
                  topic_filter: 'sensors/#',
                  permission: 'readwrite',
                  selector: 'user',
                  principal: 'alice',
                  denied: false,
                },
              ],
            },
            {
              identity: 'anonymous',
              kind: 'anonymous',
              grants: [
                {
                  topic_filter: 'public/#',
                  permission: 'read',
                  selector: 'anonymous',
                  denied: true,
                  denied_by: ['public/secret/#'],
                },
              ],
            },
          ],
        },
      ],
    }),
  'POST /api/v1/acls/effective/simulate': () =>
    jsonResponse({
      allowed: false,
      reason: 'a deny rule covers the topic',
      matching_rules: [],
    }),
}

describe('EffectivePermissionsPanel', () => {
  it('shows the effective permissions of every identity', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<EffectivePermissionsPanel token="tok" onLogout={() => {}} />)

    await screen.findByText('sensors/#')
    expect(screen.getByText('alice')).toBeInTheDocument()
    expect(screen.getByText('anonymous')).toBeInTheDocument()
    // The grant that a deny rule covers is reported as denied.
    expect(screen.getByText(/denied by public\/secret\/#/)).toBeInTheDocument()

    vi.unstubAllGlobals()
  })

  it('keeps the MCM roles out of the MQTT permissions view', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<EffectivePermissionsPanel token="tok" onLogout={() => {}} />)

    await screen.findByText('sensors/#')
    // The panel states that the MCM roles live in the administration panel
    // instead of presenting them as MQTT permissions.
    expect(screen.getByText(/administrative roles \(viewer, auditor, operator, admin\)/)).toBeInTheDocument()
    expect(screen.queryByText('auditor')).not.toBeInTheDocument()

    vi.unstubAllGlobals()
  })

  it('warns when a listener ACL file is not applied by the broker', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<EffectivePermissionsPanel token="tok" onLogout={() => {}} />)

    await screen.findByText('The ACL file of tls is not applied')
    expect(screen.getByText(/does not declare acl_file/)).toBeInTheDocument()

    vi.unstubAllGlobals()
  })

  it('answers the access simulator with the verdict of the broker', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<EffectivePermissionsPanel token="tok" onLogout={() => {}} />)

    await screen.findByText('sensors/#')
    await userEvent.type(screen.getByPlaceholderText('device-sensor-01'), 'alice')
    await userEvent.type(screen.getByPlaceholderText('sensors/1/temperature'), 'sensors/secret/1')
    await userEvent.click(screen.getByRole('button', { name: 'Simulate' }))

    await waitFor(() => expect(screen.getByText('Denied')).toBeInTheDocument())
    expect(screen.getByText('a deny rule covers the topic')).toBeInTheDocument()

    vi.unstubAllGlobals()
  })
})
