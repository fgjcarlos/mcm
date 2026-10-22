import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ListenersPanel from './ListenersPanel'

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
    const handler = routes[`${method.toUpperCase()} ${requestUrl}`]
    if (!handler) throw new Error(`Unhandled fetch: ${method} ${requestUrl}`)
    return await handler(init)
  })
}

const initialSpecs = [{ id: 'mqtt', port: 1883, bind: '0.0.0.0', protocols: ['mqtt'] }]
const preview = {
  revision_id: 'revision-123',
  needs_restart: true,
  diff: '--- current\n+++ rendered\n+listener 1883',
  rendered: 'listener 1883 0.0.0.0\nprotocol mqtt\n',
  issues: [],
  warnings: [],
  compose_status: { mapped_ports: [1883], unmapped_ports: [], disabled: false },
}

function baseRoutes(): Record<string, RouteHandler> {
  return {
    'GET /api/v1/listeners': () => jsonResponse({ specs: initialSpecs }),
  }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}

describe('ListenersPanel', () => {
  it.each(['older-first', 'newer-first'] as const)('accepts only the latest preview request (%s completion)', async (order) => {
    const requests: Array<ReturnType<typeof deferred<Response>>> = []
    vi.stubGlobal('fetch', installFetchMock({
      ...baseRoutes(),
      'POST /api/v1/listeners/preview': () => {
        const request = deferred<Response>()
        requests.push(request)
        return request.promise
      },
    }))

    const user = userEvent.setup()
    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)
    const port = await screen.findByLabelText('Port mqtt')
    await user.clear(port)
    await user.type(port, '1884')
    await user.click(screen.getByRole('button', { name: 'Preview' }))
    await user.clear(port)
    await user.type(port, '1885')
    await user.click(screen.getByRole('button', { name: 'Preview' }))

    const older = jsonResponse({ ...preview, needs_restart: false, revision_id: 'revision-older' })
    const newer = jsonResponse({ ...preview, needs_restart: false, revision_id: 'revision-newer' })
    if (order === 'older-first') {
      requests[0].resolve(older)
      await Promise.resolve()
      expect(screen.queryByText('revision-older')).not.toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Apply (restart required)' })).toBeDisabled()
      requests[1].resolve(newer)
    } else {
      requests[1].resolve(newer)
      await screen.findByText('revision-newer')
      requests[0].resolve(older)
    }

    expect(await screen.findByText('revision-newer')).toBeInTheDocument()
    expect(screen.queryByText('revision-older')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Apply (restart required)' })).toBeEnabled()
  })

  it('ignores errors from superseded preview requests', async () => {
    const requests: Array<ReturnType<typeof deferred<Response>>> = []
    vi.stubGlobal('fetch', installFetchMock({
      ...baseRoutes(),
      'POST /api/v1/listeners/preview': () => {
        const request = deferred<Response>()
        requests.push(request)
        return request.promise
      },
    }))

    const user = userEvent.setup()
    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)
    const port = await screen.findByLabelText('Port mqtt')
    await user.click(screen.getByRole('button', { name: 'Preview' }))
    await user.clear(port)
    await user.type(port, '1884')
    await user.click(screen.getByRole('button', { name: 'Preview' }))
    requests[1].resolve(jsonResponse({ ...preview, needs_restart: false, revision_id: 'revision-newer' }))
    await screen.findByText('revision-newer')
    requests[0].reject(new Error('superseded preview failed'))

    await waitFor(() => expect(requests[0].promise).rejects.toThrow('superseded preview failed'))
    expect(screen.queryByText('superseded preview failed')).not.toBeInTheDocument()
    expect(screen.getByText('revision-newer')).toBeInTheDocument()
  })

  it('renders listeners and compose port hints from initial fetch', async () => {
    vi.stubGlobal('fetch', installFetchMock({
      ...baseRoutes(),
      'GET /api/v1/listeners': () => jsonResponse({ specs: initialSpecs, compose_host_ports: [1883] }),
    }))

    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)

    expect(await screen.findByText('1883')).toBeInTheDocument()
    expect(screen.getByText('0.0.0.0')).toBeInTheDocument()
    expect(screen.getByText('mqtt')).toBeInTheDocument()
    expect(screen.getByText('Port 1883 conflicts with a Compose host port')).toBeInTheDocument()
  })

  it('submitting a preview shows the diff and stores a revision id', async () => {
    vi.stubGlobal('fetch', installFetchMock({
      ...baseRoutes(),
      'POST /api/v1/listeners/preview': () => jsonResponse(preview),
    }))

    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)
    await screen.findByText('1883')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Preview' }))

    expect(await screen.findByText('+++ rendered')).toBeInTheDocument()
    expect(screen.getByText('listener 1883 0.0.0.0')).toBeInTheDocument()
    expect(screen.getByText('revision-123')).toBeInTheDocument()
  })

  it('apply button disabled until preview captured and confirm checked', async () => {
    vi.stubGlobal('fetch', installFetchMock({
      ...baseRoutes(),
      'POST /api/v1/listeners/preview': () => jsonResponse(preview),
    }))

    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)
    const apply = screen.getByRole('button', { name: 'Apply (restart required)' })
    expect(apply).toBeDisabled()

    await screen.findByText('1883')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Preview' }))
    await screen.findByText('revision-123')
    expect(apply).toBeDisabled()

    await userEvent.setup().click(screen.getByRole('checkbox', { name: 'Confirm restart' }))
    expect(apply).toBeEnabled()
  })

  it('apply with confirm succeeds, calls listeners list refresh', async () => {
    const fetchMock = installFetchMock({
      ...baseRoutes(),
      'POST /api/v1/listeners/preview': () => jsonResponse(preview),
      'POST /api/v1/listeners/apply': (init) => {
        expect(init?.body).toBe(JSON.stringify({ revision_id: 'revision-123', confirm: true }))
        return jsonResponse({ revision_id: 'revision-123', applied: true })
      },
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)
    await screen.findByText('1883')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Preview' }))
    await screen.findByText('revision-123')
    await userEvent.setup().click(screen.getByRole('checkbox', { name: 'Confirm restart' }))
    await userEvent.setup().click(screen.getByRole('button', { name: 'Apply (restart required)' }))

    expect(await screen.findByText('Listener configuration applied.')).toBeInTheDocument()
    await waitFor(() => {
      expect(fetchMock.mock.calls.filter(([input]) => input === '/api/v1/listeners')).toHaveLength(2)
    })
  })

  it('refreshes applied edits, returns to clean state, and reports the mutation watermark', async () => {
    const onApplyStart = vi.fn(() => 42)
    const onApplySuccess = vi.fn()
    let listenerFetches = 0
    vi.stubGlobal('fetch', installFetchMock({
      'GET /api/v1/listeners': () => {
        listenerFetches += 1
        return jsonResponse({ specs: listenerFetches === 1
          ? initialSpecs
          : [{ ...initialSpecs[0], port: 1884 }] })
      },
      'POST /api/v1/listeners/preview': () => jsonResponse({ ...preview, needs_restart: false }),
      'POST /api/v1/listeners/apply': () => jsonResponse({ revision_id: 'revision-123', applied: true }),
    }))

    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" onApplyStart={onApplyStart} onApplySuccess={onApplySuccess} />)
    const port = await screen.findByLabelText('Port mqtt')
    await userEvent.setup().clear(port)
    await userEvent.setup().type(port, '1884')
    expect(screen.getByText('Unsaved changes')).toBeInTheDocument()

    await userEvent.setup().click(screen.getByRole('button', { name: 'Preview' }))
    await screen.findByText('revision-123')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Apply (restart required)' }))

    expect(await screen.findByText('Listener configuration applied.')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText('Saved')).toBeInTheDocument())
    expect(screen.getByLabelText('Port mqtt')).toHaveValue('1884')
    expect(onApplyStart).toHaveBeenCalledOnce()
    expect(onApplySuccess).toHaveBeenCalledExactlyOnceWith(42)
    expect(listenerFetches).toBe(2)
  })

  it('disables editing through apply refresh and recovers after refresh failure', async () => {
    const refresh = deferred<Response>()
    let listenerFetches = 0
    vi.stubGlobal('fetch', installFetchMock({
      ...baseRoutes(),
      'GET /api/v1/listeners': () => {
        listenerFetches += 1
        return listenerFetches === 1 ? jsonResponse({ specs: initialSpecs }) : refresh.promise
      },
      'POST /api/v1/listeners/preview': () => jsonResponse({ ...preview, needs_restart: false }),
      'POST /api/v1/listeners/apply': () => jsonResponse({ revision_id: 'revision-123', applied: true }),
    }))

    const user = userEvent.setup()
    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)
    const port = await screen.findByLabelText('Port mqtt')
    await user.clear(port)
    await user.type(port, '1884')
    await user.click(screen.getByRole('button', { name: 'Preview' }))
    await screen.findByText('revision-123')
    await user.click(screen.getByRole('button', { name: 'Apply (restart required)' }))

    await waitFor(() => expect(listenerFetches).toBe(2))
    expect(port).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Add listener' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Preview' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Apply (restart required)' })).toBeDisabled()
    refresh.resolve(jsonResponse({ error: 'refresh failed' }, { status: 500 }))
    await screen.findByText('refresh failed')
    expect(screen.getByLabelText('Port mqtt')).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Add listener' })).toBeEnabled()
  })

  it('shows compose_unmapped (409) as an error', async () => {
    vi.stubGlobal('fetch', installFetchMock({
      ...baseRoutes(),
      'POST /api/v1/listeners/preview': () => jsonResponse({ error: 'listener ports are not mapped by compose' }, { status: 409 }),
    }))

    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)
    await screen.findByText('1883')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Preview' }))

    expect(await screen.findByText('listener ports are not mapped by compose')).toBeInTheDocument()
  })

  it('shows preview validation 400 in the Issues panel', async () => {
    vi.stubGlobal('fetch', installFetchMock({
      ...baseRoutes(),
      'POST /api/v1/listeners/preview': () => jsonResponse({ issues: [{ kind: 'port', listener_id: 'mqtt', message: 'port must be between 1 and 65535' }] }, { status: 400 }),
    }))

    render(<ListenersPanel token="tok" onLogout={() => {}} role="admin" />)
    await screen.findByText('1883')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Preview' }))

    expect(await screen.findByText('Issues')).toBeInTheDocument()
    expect(screen.getByText('port must be between 1 and 65535')).toBeInTheDocument()
  })
})
