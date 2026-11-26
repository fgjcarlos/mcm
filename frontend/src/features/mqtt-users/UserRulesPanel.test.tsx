import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ACLRule } from '../acl/types'
import MQTTUsersPanel from './MQTTUsersPanel'

type RouteHandler = (init?: RequestInit, url?: string) => Response | Promise<Response>

function jsonResponse(body: unknown, init?: ResponseInit) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' }, ...init })
}

function installFetchMock(routes: Record<string, RouteHandler>) {
  return vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const requestUrl = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
    const key = `${(init?.method ?? 'GET').toUpperCase()} ${requestUrl}`
    const handler = routes[key] ?? routes[key.split('?')[0]]
    if (!handler) throw new Error(`Unhandled fetch: ${key}`)
    return await handler(init, requestUrl)
  })
}

const users = [
  { id: 1, username: 'sensor', disabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
]

// Answers GET /api/v1/acls, including exact principal filtering and limit/offset pagination.
function pagedRules(all: ACLRule[]): RouteHandler {
  return (_init, url) => {
    const params = new URL(url ?? '', 'http://localhost').searchParams
    const principal = params.get('principal')
    const matches = all.filter((rule) => principal ? rule.selector === 'user' && rule.principal === principal : rule.principal.toLowerCase().includes((params.get('user') ?? '').toLowerCase()))
    const limit = Number(params.get('limit') ?? 50)
    const offset = Number(params.get('offset') ?? 0)
    return jsonResponse({ rules: matches.slice(offset, offset + limit), total: matches.length, limit, offset })
  }
}

function rule(id: string, principal: string, topic: string, extra: Partial<ACLRule> = {}): ACLRule {
  return { id, principal, topic_filter: topic, permission: 'read', selector: 'user', scope: 'global', ...extra }
}

async function openRules(role: string, routes: Record<string, RouteHandler>, props: Partial<Parameters<typeof MQTTUsersPanel>[0]> = {}) {
  const fetchMock = installFetchMock({ 'GET /api/v1/mqtt-users': () => jsonResponse({ users, total: 1, limit: 25, offset: 0 }), ...routes })
  vi.stubGlobal('fetch', fetchMock)
  const user = userEvent.setup()
  render(<MQTTUsersPanel token="tok" onLogout={() => {}} role={role} {...props} />)
  await user.click(await screen.findByRole('button', { name: 'Rules for sensor' }))
  const panel = await screen.findByRole('region', { name: 'Direct ACL rules of sensor' })
  return { fetchMock, user, panel }
}

function bodyOf(fetchMock: ReturnType<typeof installFetchMock>, method: string) {
  const call = fetchMock.mock.calls.find(([, init]) => init?.method === method)
  return call ? JSON.parse(String(call[1]?.body)) : undefined
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('direct ACL rules from the account of an existing MQTT user (#360)', () => {
  it('lists only the direct rules of that exact username, across pages, and says they are not its effective permissions', async () => {
    const others = Array.from({ length: 120 }, (_, i) => rule(String(i + 10), `sensor-${i}`, `other/${i}`))
    const all = [
      rule('1', 'sensor', 'plant/temp'),
      rule('2', 'SENSOR', 'case/differs'),
      rule('3', '%u', 'devices/%u/#', { selector: 'pattern', principal: '' }),
      ...others,
      rule('200', 'sensor', 'plant/alarms', { permission: 'deny', scope: 'listener', listener_id: 'tls' }),
    ]
    const { panel, fetchMock } = await openRules('auditor', { 'GET /api/v1/acls': pagedRules(all) })

    expect(await within(panel).findByText('plant/alarms')).toBeInTheDocument()
    expect(within(panel).getByText('plant/temp')).toBeInTheDocument()
    expect(within(panel).queryByText('case/differs')).not.toBeInTheDocument()
    expect(within(panel).queryByText('other/0')).not.toBeInTheDocument()
    expect(within(panel).queryByText('devices/%u/#')).not.toBeInTheDocument()
    expect(within(panel).getByText('tls')).toBeInTheDocument()
    expect(within(panel).getByText(/not its effective permissions/i)).toBeInTheDocument()
    const urls = fetchMock.mock.calls.map(([input]) => String(input))
    expect(urls).toContain('/api/v1/acls?principal=sensor&limit=100&offset=0')
    expect(urls.filter((url) => url.includes('/api/v1/acls?principal=sensor'))).toHaveLength(1)
  })

  it('keeps reading further pages when the user itself has more than one page of direct rules', async () => {
    const own = Array.from({ length: 150 }, (_, i) => rule(String(i + 1), 'sensor', `plant/${i}`))
    const { panel, fetchMock } = await openRules('auditor', { 'GET /api/v1/acls': pagedRules(own) })

    expect(await within(panel).findByText('plant/149')).toBeInTheDocument()
    const urls = fetchMock.mock.calls.map(([input]) => String(input)).filter((url) => url.includes('/api/v1/acls?principal=sensor'))
    expect(urls).toEqual(['/api/v1/acls?principal=sensor&limit=100&offset=0', '/api/v1/acls?principal=sensor&limit=100&offset=100'])
  })

  it('shows an empty state when the account has no direct rule', async () => {
    const { panel } = await openRules('auditor', { 'GET /api/v1/acls': pagedRules([rule('9', 'sensor-2', 'x/y')]) })
    expect(await within(panel).findByText('sensor has no direct ACL rule.')).toBeInTheDocument()
  })

  it('lets an operator add, change and remove a rule of that user without typing the username, leaving the deploy pending', async () => {
    let stored = [rule('1', 'sensor', 'plant/temp'), rule('2', 'sensor-2', 'plant/other')]
    const onMutationStart = vi.fn(() => 7)
    const onMutation = vi.fn()
    const onReviewChanges = vi.fn()
    const { panel, user, fetchMock } = await openRules('operator', {
      'GET /api/v1/acls': (init, url) => pagedRules(stored)(init, url),
      'POST /api/v1/acls': (init) => {
        const created = { ...JSON.parse(String(init?.body)), id: '3' }
        stored = [...stored, created]
        return jsonResponse(created, { status: 201 })
      },
      'PUT /api/v1/acls/1': (init) => {
        stored = stored.map((r) => (r.id === '1' ? { ...JSON.parse(String(init?.body)), id: '1' } : r))
        return jsonResponse(stored[0])
      },
      'DELETE /api/v1/acls/3': () => {
        stored = stored.filter((r) => r.id !== '3')
        return new Response(null, { status: 204 })
      },
    }, { onMutationStart, onMutation, onReviewChanges })

    await within(panel).findByText('plant/temp')
    expect(within(panel).queryByRole('textbox', { name: /principal|username/i })).not.toBeInTheDocument()

    await user.click(within(panel).getByRole('button', { name: 'Add rule' }))
    await user.type(within(panel).getByRole('textbox', { name: /^Topic filter/ }), 'plant/cmd/#')
    await user.selectOptions(within(panel).getByRole('combobox', { name: 'Permission' }), 'write')
    await user.click(within(panel).getByRole('button', { name: 'Save rule' }))
    expect(await within(panel).findByText('plant/cmd/#')).toBeInTheDocument()
    expect(bodyOf(fetchMock, 'POST')).toEqual({ principal: 'sensor', selector: 'user', topic_filter: 'plant/cmd/#', permission: 'write', scope: 'global', listener_id: '', description: '' })

    const status = within(panel).getByRole('status')
    expect(status).toHaveTextContent('Saved in MCM, not applied to Mosquitto yet.')
    expect(status).toHaveTextContent('The review covers every pending change, not only this user')
    await user.click(within(status).getByRole('button', { name: 'Preview changes' }))
    expect(onReviewChanges).toHaveBeenCalledTimes(1)

    await user.click(within(panel).getByRole('button', { name: 'Edit rule plant/temp' }))
    const topic = within(panel).getByRole('textbox', { name: /^Topic filter/ })
    await user.clear(topic)
    await user.type(topic, 'plant/temp/#')
    await user.click(within(panel).getByRole('button', { name: 'Save rule' }))
    expect(await within(panel).findByText('plant/temp/#')).toBeInTheDocument()
    expect(bodyOf(fetchMock, 'PUT')).toMatchObject({ principal: 'sensor', selector: 'user', topic_filter: 'plant/temp/#' })

    await user.click(within(panel).getByRole('button', { name: 'Remove rule plant/cmd/#' }))
    await user.click(within(panel).getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(within(panel).queryByText('plant/cmd/#')).not.toBeInTheDocument())

    expect(onMutationStart).toHaveBeenCalledTimes(3)
    expect(onMutation).toHaveBeenCalledTimes(3)
    expect(onMutation).toHaveBeenCalledWith(7)
    // The other account keeps its rule, and nothing is deployed on the way.
    expect(stored.find((r) => r.id === '2')).toEqual(rule('2', 'sensor-2', 'plant/other'))
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/deploy'))).toBe(false)
  })

  it('keeps a read-only role from changing the rules', async () => {
    const { panel } = await openRules('auditor', { 'GET /api/v1/acls': pagedRules([rule('1', 'sensor', 'plant/temp')]) })
    await within(panel).findByText('plant/temp')
    expect(within(panel).getByRole('button', { name: 'Add rule' })).toBeDisabled()
    expect(within(panel).getByRole('button', { name: 'Edit rule plant/temp' })).toBeDisabled()
    expect(within(panel).getByRole('button', { name: 'Remove rule plant/temp' })).toBeDisabled()
    expect(within(panel).getByText('Changing ACL rules needs the operator role or higher.')).toBeInTheDocument()
  })
})
