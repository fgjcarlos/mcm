import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import UserOnboardingFlow from './UserOnboardingFlow'

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

const mfaProof = {
  proof: 'mfa-proof-token',
  expires_at: new Date(Date.now() + 5 * 60 * 1000).toISOString(),
}

const baseRoutes: Record<string, RouteHandler> = {
  'POST /api/v1/mqtt-users': () =>
    jsonResponse({ id: 7, username: 'probe', password: 'generated-secret', disabled: false }),
  'POST /api/v1/acls': () => jsonResponse({ id: 'rule-1', principal: 'probe' }),
  'POST /api/v1/deployments/preview': () =>
    jsonResponse({
      revision_id: 'rev-1',
      acl_diff: '+user probe\n+topic readwrite probe/allowed\n',
      passwd_diff: '+probe:$7$generated-secret\n',
      has_changes: true,
    }),
  'POST /api/v1/deployments/apply': () =>
    jsonResponse({ id: 11, status: 'active_verified', message: 'activation probe: user grant', created_at: '2026-01-01T00:00:00Z' }),
  'POST /api/v1/auth/mfa/step-up': () => jsonResponse(mfaProof),
}

async function completeUserAndRule(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByPlaceholderText('device-sensor-01'), 'probe')
  await user.click(screen.getByRole('button', { name: 'Create user' }))
  await screen.findByText('User created — save this password')
  await user.type(screen.getByPlaceholderText('sensors/# or device/+/status'), 'probe/allowed')
  await user.click(screen.getByRole('button', { name: 'Save access rule' }))
  await screen.findByRole('button', { name: 'Preview changes' })
}

async function confirmMFA(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByPlaceholderText('Authenticator code'), '123456')
  await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
  await screen.findByText(/MFA confirmed for this session/)
}

describe('UserOnboardingFlow journey', () => {
  it('creates the user, the access rule, previews and applies in one journey', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))
    const user = userEvent.setup()

    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)

    await completeUserAndRule(user)
    await user.click(screen.getByRole('button', { name: 'Preview changes' }))
    await screen.findByRole('button', { name: 'Apply and verify' })

    // The preview revision is the one the apply consumes.
    const diffs = Array.from(document.querySelectorAll('pre')).map((node) => node.textContent ?? '').join('\n')
    expect(diffs).toContain('+user probe')

    await confirmMFA(user)
    await user.click(screen.getByRole('button', { name: 'Apply and verify' }))

    expect(await screen.findByText('Applied and verified')).toBeInTheDocument()
    expect(screen.getByText('activation probe: user grant')).toBeInTheDocument()

    const applyCall = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.find(
      ([, init]) => (init as RequestInit | undefined)?.method === 'POST' && String((init as RequestInit).body).includes('rev-1'),
    )
    expect(applyCall).toBeTruthy()
    expect((applyCall![1] as RequestInit).headers).toMatchObject({ 'X-MCM-MFA': 'mfa-proof-token' })

    vi.unstubAllGlobals()
  })

  it('keeps the apply disabled until a recent MFA proof exists', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))
    const user = userEvent.setup()

    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)

    await completeUserAndRule(user)
    await user.click(screen.getByRole('button', { name: 'Preview changes' }))

    const applyButton = await screen.findByRole('button', { name: 'Apply and verify' })
    expect(applyButton).toBeDisabled()

    await confirmMFA(user)
    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Apply and verify' })).toBeEnabled()
    })

    vi.unstubAllGlobals()
  })

  it('names the failed stage and the concrete exit when the activation is rolled back', async () => {
    const rollbackRoutes: Record<string, RouteHandler> = {
      ...baseRoutes,
      'POST /api/v1/deployments/apply': () =>
        jsonResponse({
          id: 12,
          status: 'rolled_back',
          message:
            'no activation probe available (need a user readwrite grant, a pattern readwrite grant or the configured managed service account); MCM does not know the cleartext password of user "probe" because it only lives in the process memory and is lost on restart; reset that user\'s password and apply again immediately',
          created_at: '2026-01-01T00:00:00Z',
        }),
    }
    vi.stubGlobal('fetch', installFetchMock(rollbackRoutes))
    const user = userEvent.setup()

    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)

    await completeUserAndRule(user)
    await user.click(screen.getByRole('button', { name: 'Preview changes' }))
    await confirmMFA(user)
    await user.click(screen.getByRole('button', { name: 'Apply and verify' }))

    expect(await screen.findByText('Rolled back')).toBeInTheDocument()
    expect(screen.getByText(/Failed stage: activation verification \(apply\)/)).toBeInTheDocument()
    // The cause and the concrete exit of the deployment message reach the operator verbatim.
    expect(screen.getByText(/reset that user's password and apply again immediately/)).toBeInTheDocument()

    vi.unstubAllGlobals()
  })

  it('names the failed stage when the access rule cannot be saved', async () => {
    const ruleFailingRoutes: Record<string, RouteHandler> = {
      ...baseRoutes,
      'POST /api/v1/acls': () => jsonResponse({ error: 'invalid topic filter' }, { status: 422 }),
    }
    vi.stubGlobal('fetch', installFetchMock(ruleFailingRoutes))
    const user = userEvent.setup()

    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)

    await user.type(screen.getByPlaceholderText('device-sensor-01'), 'probe')
    await user.click(screen.getByRole('button', { name: 'Create user' }))
    await screen.findByText('User created — save this password')
    await user.type(screen.getByPlaceholderText('sensors/# or device/+/status'), 'probe/allowed')
    await user.click(screen.getByRole('button', { name: 'Save access rule' }))

    expect(await screen.findByText('Access rule step failed')).toBeInTheDocument()
    expect(screen.getByText('invalid topic filter')).toBeInTheDocument()

    vi.unstubAllGlobals()
  })

  it('names the failed stage when the preview cannot be generated', async () => {
    const previewFailingRoutes: Record<string, RouteHandler> = {
      ...baseRoutes,
      'POST /api/v1/deployments/preview': () => jsonResponse({ error: 'internal server error' }, { status: 500 }),
    }
    vi.stubGlobal('fetch', installFetchMock(previewFailingRoutes))
    const user = userEvent.setup()

    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)

    await completeUserAndRule(user)
    await user.click(screen.getByRole('button', { name: 'Preview changes' }))

    expect(await screen.findByText('Preview step failed')).toBeInTheDocument()
    expect(screen.getByText('internal server error')).toBeInTheDocument()

    vi.unstubAllGlobals()
  })

  it('names the failed stage when the apply is refused', async () => {
    const applyFailingRoutes: Record<string, RouteHandler> = {
      ...baseRoutes,
      'POST /api/v1/deployments/apply': () =>
        jsonResponse({ error: 'preview revision no longer matches the on-disk configuration' }, { status: 409 }),
    }
    vi.stubGlobal('fetch', installFetchMock(applyFailingRoutes))
    const user = userEvent.setup()

    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)

    await completeUserAndRule(user)
    await user.click(screen.getByRole('button', { name: 'Preview changes' }))
    await confirmMFA(user)
    await user.click(screen.getByRole('button', { name: 'Apply and verify' }))

    expect(await screen.findByText('Apply step failed')).toBeInTheDocument()
    expect(screen.getByText('preview revision no longer matches the on-disk configuration')).toBeInTheDocument()

    vi.unstubAllGlobals()
  })

  it('reports every saved change as pending deploy and clears it after a verified apply', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))
    const onMutation = vi.fn()
    const onApplyStart = vi.fn(() => 2)
    const onApplySuccess = vi.fn()
    const user = userEvent.setup()

    render(
      <UserOnboardingFlow
        token="tok"
        onLogout={() => {}}
        role="admin"
        onMutationStart={() => 1}
        onMutation={onMutation}
        onApplyStart={onApplyStart}
        onApplySuccess={onApplySuccess}
      />,
    )

    await completeUserAndRule(user)
    // The user and the access rule are saved but not applied yet.
    expect(onMutation).toHaveBeenCalledTimes(2)

    await user.click(screen.getByRole('button', { name: 'Preview changes' }))
    await confirmMFA(user)
    await user.click(screen.getByRole('button', { name: 'Apply and verify' }))

    expect(await screen.findByText('Applied and verified')).toBeInTheDocument()
    expect(onApplyStart).toHaveBeenCalledTimes(1)
    expect(onApplySuccess).toHaveBeenCalledWith(2)

    vi.unstubAllGlobals()
  })

  it('does not clear the pending deploy state when the activation is rolled back', async () => {
    const rollbackRoutes: Record<string, RouteHandler> = {
      ...baseRoutes,
      'POST /api/v1/deployments/apply': () =>
        jsonResponse({ id: 13, status: 'rolled_back', message: 'verification failed', created_at: '2026-01-01T00:00:00Z' }),
    }
    vi.stubGlobal('fetch', installFetchMock(rollbackRoutes))
    const onApplySuccess = vi.fn()
    const user = userEvent.setup()

    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" onApplySuccess={onApplySuccess} />)

    await completeUserAndRule(user)
    await user.click(screen.getByRole('button', { name: 'Preview changes' }))
    await confirmMFA(user)
    await user.click(screen.getByRole('button', { name: 'Apply and verify' }))

    expect(await screen.findByText('Rolled back')).toBeInTheDocument()
    expect(onApplySuccess).not.toHaveBeenCalled()

    vi.unstubAllGlobals()
  })

  it('does not present a pending activation as applied and verified', async () => {
    const pendingRoutes: Record<string, RouteHandler> = {
      ...baseRoutes,
      'POST /api/v1/deployments/apply': () =>
        jsonResponse({ id: 14, status: 'pending_activation', message: 'waiting for the broker', created_at: '2026-01-01T00:00:00Z' }),
    }
    vi.stubGlobal('fetch', installFetchMock(pendingRoutes))
    const onApplySuccess = vi.fn()
    const user = userEvent.setup()

    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" onApplySuccess={onApplySuccess} />)

    await completeUserAndRule(user)
    await user.click(screen.getByRole('button', { name: 'Preview changes' }))
    await confirmMFA(user)
    await user.click(screen.getByRole('button', { name: 'Apply and verify' }))

    expect(await screen.findByText(/the activation was not proven yet/)).toBeInTheDocument()
    expect(screen.queryByText('Applied and verified')).not.toBeInTheDocument()
    expect(onApplySuccess).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Test the connection' })).toBeInTheDocument()

    vi.unstubAllGlobals()
  })

  it('does not claim the previous configuration was restored when the rollback failed', async () => {
    const rollbackFailedRoutes: Record<string, RouteHandler> = {
      ...baseRoutes,
      'POST /api/v1/deployments/apply': () =>
        jsonResponse({ id: 15, status: 'rollback_failed', message: 'restore error', created_at: '2026-01-01T00:00:00Z' }),
    }
    vi.stubGlobal('fetch', installFetchMock(rollbackFailedRoutes))
    const user = userEvent.setup()

    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)

    await completeUserAndRule(user)
    await user.click(screen.getByRole('button', { name: 'Preview changes' }))
    await confirmMFA(user)
    await user.click(screen.getByRole('button', { name: 'Apply and verify' }))

    expect(await screen.findByText('Rollback failed')).toBeInTheDocument()
    expect(screen.getByText(/restore of the previous configuration failed/)).toBeInTheDocument()
    expect(screen.queryByText(/MCM restored the previous configuration/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Test the connection' })).not.toBeInTheDocument()

    vi.unstubAllGlobals()
  })

  it('does not claim a restore for a failed apply', async () => {
    const failedRoutes: Record<string, RouteHandler> = {
      ...baseRoutes,
      'POST /api/v1/deployments/apply': () =>
        jsonResponse({ id: 16, status: 'failed', message: 'apply error', created_at: '2026-01-01T00:00:00Z' }),
    }
    vi.stubGlobal('fetch', installFetchMock(failedRoutes))
    const user = userEvent.setup()

    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)

    await completeUserAndRule(user)
    await user.click(screen.getByRole('button', { name: 'Preview changes' }))
    await confirmMFA(user)
    await user.click(screen.getByRole('button', { name: 'Apply and verify' }))

    expect(await screen.findByText('Apply failed (failed)')).toBeInTheDocument()
    expect(screen.queryByText(/MCM restored the previous configuration/)).not.toBeInTheDocument()

    vi.unstubAllGlobals()
  })

  it('retries after a failed apply with a new preview without creating the user or the rule again', async () => {
    let previewCalls = 0
    let applyCalls = 0
    const userHandler = vi.fn(baseRoutes['POST /api/v1/mqtt-users'])
    const aclHandler = vi.fn(baseRoutes['POST /api/v1/acls'])
    const retryRoutes: Record<string, RouteHandler> = {
      ...baseRoutes,
      'POST /api/v1/mqtt-users': userHandler,
      'POST /api/v1/acls': aclHandler,
      'POST /api/v1/deployments/preview': () => {
        previewCalls += 1
        return jsonResponse({ revision_id: `rev-${previewCalls}`, acl_diff: '+user probe\n', passwd_diff: '', has_changes: true })
      },
      'POST /api/v1/deployments/apply': (init) => {
        applyCalls += 1
        if (applyCalls === 1) return jsonResponse({ error: 'apply failed' }, { status: 500 })
        expect(String(init?.body)).toContain('rev-2')
        return jsonResponse({ id: 17, status: 'active_verified', message: 'ok', created_at: '2026-01-01T00:00:00Z' })
      },
    }
    vi.stubGlobal('fetch', installFetchMock(retryRoutes))
    const user = userEvent.setup()

    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="admin" />)

    await completeUserAndRule(user)
    await user.click(screen.getByRole('button', { name: 'Preview changes' }))
    await confirmMFA(user)
    await user.click(screen.getByRole('button', { name: 'Apply and verify' }))
    expect(await screen.findByText('Apply step failed')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Generate a new preview' }))
    await waitFor(() => expect(previewCalls).toBe(2))
    await waitFor(() => expect(screen.queryByText('Apply step failed')).not.toBeInTheDocument())
    await waitFor(() => expect(screen.getByRole('button', { name: 'Apply and verify' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: 'Apply and verify' }))

    expect(await screen.findByText('Applied and verified')).toBeInTheDocument()
    expect(userHandler).toHaveBeenCalledTimes(1)
    expect(aclHandler).toHaveBeenCalledTimes(1)

    vi.unstubAllGlobals()
  })

  it('keeps the write controls disabled for a viewer', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes))

    render(<UserOnboardingFlow token="tok" onLogout={() => {}} role="viewer" />)

    const createButton = screen.getByRole('button', { name: 'Create user' })
    expect(createButton).toBeDisabled()
    expect(createButton).toHaveAttribute('title', 'Requires operator role or higher')

    vi.unstubAllGlobals()
  })
})
