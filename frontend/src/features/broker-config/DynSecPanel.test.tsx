import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import DynSecPanel from './DynSecPanel'

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

const document = {
  clients: [
    {
      username: 'alice',
      textname: 'Alice',
      textdescription: '',
      clientid: '',
      password: '',
      password_set: true,
      roles: [{ rolename: 'reader', priority: 0 }],
      groups: [],
      disabled: false,
    },
  ],
  groups: [{ groupname: 'team', textname: '', textdescription: '', roles: [], clients: [{ username: 'alice', priority: 0 }] }],
  roles: [
    {
      rolename: 'reader',
      textname: 'Reader',
      textdescription: '',
      acls: [{ acltype: 'subscribePattern', topic: 'sensors/#', allow: true, priority: 0 }],
    },
  ],
  default_access: { publish_client_send: false, publish_client_receive: true, subscribe: false, unsubscribe: true },
  anonymous_group: 'anonymous',
}

function dynSecState(overrides: Record<string, unknown> = {}) {
  return {
    backend: 'dynamic-security',
    enabled: true,
    management_client: 'mcm',
    document,
    guard: { ok: true, management_client: 'mcm', allowed: ['mcm'], violations: [], reasons: [] },
    warnings: [],
    ...overrides,
  }
}

describe('DynSecPanel', () => {
  const originalFetch = globalThis.fetch

  beforeEach(() => {
    vi.restoreAllMocks()
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
  })

  it('shows the clients, the groups and the roles', async () => {
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/dynsec': () => jsonResponse(dynSecState()),
    }) as unknown as typeof fetch

    render(<DynSecPanel token="token" onLogout={() => {}} role="auditor" />)

    await waitFor(() => expect(screen.getByLabelText('Username client 1')).toHaveValue('alice'))
    expect(screen.getByLabelText('Name group 1')).toHaveValue('team')
    expect(screen.getByLabelText('Name role 1')).toHaveValue('reader')
    expect(screen.getByLabelText('Topic role 1 1')).toHaveValue('sensors/#')
    expect(screen.getByLabelText('Access type role 1 1')).toHaveValue('subscribePattern')
    expect(screen.getByLabelText('Allow role 1 1')).toHaveValue('true')
  })

  it('never shows a stored password', async () => {
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/dynsec': () => jsonResponse(dynSecState()),
    }) as unknown as typeof fetch

    render(<DynSecPanel token="token" onLogout={() => {}} role="auditor" />)

    await waitFor(() => expect(screen.getByLabelText('Password client 1')).toHaveValue(''))
    expect(screen.getByText(/A password is stored/)).toBeInTheDocument()
  })

  it('shows a violation of the management channel as an error', async () => {
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/dynsec': () =>
        jsonResponse(
          dynSecState({
            guard: {
              ok: false,
              management_client: 'mcm',
              allowed: ['mcm'],
              violations: ['intruder'],
              reasons: ['the client "intruder" may reach the control channel of the plugin'],
            },
          }),
        ),
    }) as unknown as typeof fetch

    render(<DynSecPanel token="token" onLogout={() => {}} role="auditor" />)

    expect(await screen.findByText('Management channel is open')).toBeInTheDocument()
    expect(screen.getByText('intruder')).toBeInTheDocument()
    expect(screen.getByText(/may reach the control channel of the plugin/)).toBeInTheDocument()
  })

  it('refuses the preview to an auditor', async () => {
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/dynsec': () => jsonResponse(dynSecState()),
    }) as unknown as typeof fetch

    render(<DynSecPanel token="token" onLogout={() => {}} role="auditor" />)

    await waitFor(() => expect(screen.getByLabelText('Username client 1')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Preview' })).toBeDisabled()
  })

  it('previews the desired document and shows the plan', async () => {
    let previewRequest: { document?: { clients?: Array<{ username: string }> } } | null = null
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/dynsec': () => jsonResponse(dynSecState()),
      'POST /api/v1/broker/dynsec/preview': (init) => {
        previewRequest = JSON.parse(String(init?.body ?? '{}'))
        return jsonResponse({
          plan_id: 'plan-1',
          kind: 'update',
          plan: {
            changes: [
              { kind: 'client', name: 'bob', action: 'create', summary: 'create the client "bob"', commands: [{ command: 'createClient', username: 'bob', password: '********' }] },
            ],
          },
          guard: { ok: true, management_client: 'mcm', allowed: ['mcm'], violations: [], reasons: [] },
          backend: 'dynamic-security',
          has_changes: true,
        })
      },
    }) as unknown as typeof fetch

    render(<DynSecPanel token="token" onLogout={() => {}} role="operator" />)

    await waitFor(() => expect(screen.getByLabelText('Username client 1')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Add client' }))
    await userEvent.type(screen.getByLabelText('Username client 2'), 'bob')
    await userEvent.click(screen.getByRole('button', { name: 'Preview' }))

    expect(await screen.findByText(/plan plan-1/)).toBeInTheDocument()
    expect(screen.getByText('create the client "bob"')).toBeInTheDocument()
    expect(screen.getByText('createClient')).toBeInTheDocument()
    expect(previewRequest).not.toBeNull()
    const sent = (previewRequest as unknown as { document: { clients: Array<{ username: string }> } }).document
    expect(sent.clients[1].username).toBe('bob')
  })

  it('applies the reviewed plan with the same document', async () => {
    let applyRequest: { plan_id?: string; document?: { clients?: Array<{ username: string }> } } | null = null
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/dynsec': () => jsonResponse(dynSecState()),
      'POST /api/v1/broker/dynsec/preview': () =>
        jsonResponse({
          plan_id: 'plan-1',
          kind: 'update',
          plan: { changes: [{ kind: 'client', name: 'alice', action: 'update', summary: 'update the client "alice"', commands: [] }] },
          guard: { ok: true, management_client: 'mcm', allowed: ['mcm'], violations: [], reasons: [] },
          backend: 'dynamic-security',
          has_changes: true,
        }),
      'POST /api/v1/broker/dynsec/apply': (init) => {
        applyRequest = JSON.parse(String(init?.body ?? '{}'))
        return jsonResponse({
          plan_id: 'plan-1',
          document,
          guard: { ok: true, management_client: 'mcm', allowed: ['mcm'], violations: [], reasons: [] },
          deployment: { id: 4, status: 'active_verified' },
        })
      },
    }) as unknown as typeof fetch

    render(<DynSecPanel token="token" onLogout={() => {}} role="admin" />)

    await waitFor(() => expect(screen.getByLabelText('Username client 1')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Preview' }))

    const applyButton = await screen.findByRole('button', { name: 'Apply' })
    await userEvent.click(applyButton)

    await waitFor(() => expect(applyRequest).not.toBeNull())
    expect((applyRequest as unknown as { plan_id: string }).plan_id).toBe('plan-1')
    expect((applyRequest as unknown as { document: { clients: Array<{ username: string }> } }).document.clients[0].username).toBe('alice')
    expect(await screen.findByText(/The security database was updated/)).toBeInTheDocument()
  })

  it('shows the adoption path when the broker does not use the plugin', async () => {
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/dynsec': () =>
        jsonResponse(
          dynSecState({
            backend: 'file',
            enabled: false,
            guard: { ok: false },
            warnings: ['the dynamic security plugin is not loaded by the broker'],
          }),
        ),
    }) as unknown as typeof fetch

    render(<DynSecPanel token="token" onLogout={() => {}} role="admin" />)

    expect(await screen.findByRole('button', { name: 'Adopt the dynamic security backend' })).toBeInTheDocument()
    expect(screen.getByText('password and ACL files')).toBeInTheDocument()
    expect(screen.getByText(/the password file and the ACL file are dropped/)).toBeInTheDocument()
  })

  it('adopts the backend, shows the credentials once and applies the plan', async () => {
    let applyRequest: { plan_id?: string; document?: unknown } | null = null
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/dynsec': () =>
        jsonResponse(dynSecState({ backend: 'file', enabled: false, guard: { ok: false } })),
      'POST /api/v1/broker/dynsec/adopt': () =>
        jsonResponse({
          plan_id: 'plan-adopt',
          kind: 'adopt',
          plan: { changes: [] },
          guard: { ok: true, management_client: 'mcm', allowed: ['mcm'], violations: [], reasons: [] },
          backend: 'file',
          has_changes: true,
          credentials: [{ username: 'bob', password: 'generated_password', reason: 'the password of the client was changed' }],
          config_revision_id: 'revision-9',
          config_diff: '+plugin /usr/lib/mosquitto_dynamic_security.so',
        }),
      'POST /api/v1/broker/dynsec/apply': (init) => {
        applyRequest = JSON.parse(String(init?.body ?? '{}'))
        return jsonResponse({
          plan_id: 'plan-adopt',
          document,
          guard: { ok: true, management_client: 'mcm', allowed: ['mcm'], violations: [], reasons: [] },
          deployment: { id: 5, status: 'active_verified' },
        })
      },
    }) as unknown as typeof fetch

    render(<DynSecPanel token="token" onLogout={() => {}} role="admin" />)

    await userEvent.click(await screen.findByRole('button', { name: 'Adopt the dynamic security backend' }))

    expect(await screen.findByText(/Copy these now, they are shown once/)).toBeInTheDocument()
    expect(screen.getByText(/bob · generated_password/)).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Apply' }))

    await waitFor(() => expect(applyRequest).not.toBeNull())
    expect((applyRequest as unknown as { plan_id: string }).plan_id).toBe('plan-adopt')
  })

  it('refuses a change that would open the management channel', async () => {
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/dynsec': () => jsonResponse(dynSecState()),
      'POST /api/v1/broker/dynsec/preview': () =>
        jsonResponse(
          {
            error: 'the management channel of the dynamic security plugin would be open',
            guard: {
              ok: false,
              management_client: 'mcm',
              allowed: ['mcm'],
              violations: ['intruder'],
              reasons: ['the client "intruder" may reach the control channel of the plugin'],
            },
          },
          409,
        ),
    }) as unknown as typeof fetch

    render(<DynSecPanel token="token" onLogout={() => {}} role="operator" />)

    await waitFor(() => expect(screen.getByLabelText('Username client 1')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Preview' }))

    expect(await screen.findByText(/the management channel of the dynamic security plugin would be open/)).toBeInTheDocument()
    expect(screen.getByText(/may reach the control channel of the plugin/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Apply' })).not.toBeInTheDocument()
  })
})
