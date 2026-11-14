import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { usePagedList } from './usePagedList'

type Row = { id: number }

function jsonResponse(body: unknown, init?: ResponseInit) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' }, ...init })
}

// Answers like the backend envelope of S10: echoes limit/offset (limit
// clamped to 100) and reports `total` matching rows.
function envelopeFetch(total: number, rowsKey = 'users') {
  return vi.fn(async (input: string | URL | Request, _init?: RequestInit) => {
    void _init
    const url = new URL(String(input), 'http://localhost')
    const limit = Math.min(Number(url.searchParams.get('limit') ?? 50), 100)
    const offset = Number(url.searchParams.get('offset') ?? 0)
    const count = Math.max(0, Math.min(limit, total - offset))
    const rows = Array.from({ length: count }, (_, i) => ({ id: offset + i + 1 }))
    return jsonResponse({ [rowsKey]: rows, total, limit, offset })
  })
}

function requestedUrls(fetchMock: ReturnType<typeof vi.fn>) {
  return fetchMock.mock.calls.map(([input]) => String(input))
}

const usersOptions = { path: '/api/v1/mqtt-users', rowsKey: 'users', token: 'tok', namespace: 'users' }
const aclOptions = { path: '/api/v1/acl', rowsKey: 'rules', token: 'tok', namespace: 'acl', filterParams: { user: 'user', topic: 'topic' } }

beforeEach(() => {
  window.history.replaceState(null, '', '/')
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('usePagedList', () => {
  it('loads the first page with the expected query and exposes the envelope', async () => {
    const fetchMock = envelopeFetch(3)
    vi.stubGlobal('fetch', fetchMock)

    const { result } = renderHook(() => usePagedList<Row>(usersOptions))

    expect(result.current.loading).toBe(true)
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(requestedUrls(fetchMock)).toEqual(['/api/v1/mqtt-users?limit=25&offset=0'])
    expect(fetchMock.mock.calls[0][1]?.headers).toEqual({ Authorization: 'Bearer tok' })
    expect(result.current.rows).toEqual([{ id: 1 }, { id: 2 }, { id: 3 }])
    expect(result.current).toMatchObject({ total: 3, limit: 25, offset: 0, page: 1, pageCount: 1, error: null, hasFilters: false, search: '', filters: {} })
  })

  it('reports pageCount 0 for an empty listing and pageCount > 1 when total exceeds the page size', async () => {
    vi.stubGlobal('fetch', envelopeFetch(0))
    const empty = renderHook(() => usePagedList<Row>(usersOptions))
    await waitFor(() => expect(empty.result.current.loading).toBe(false))
    expect(empty.result.current).toMatchObject({ rows: [], total: 0, pageCount: 0, hasFilters: false })
    empty.unmount()

    vi.stubGlobal('fetch', envelopeFetch(200))
    const { result } = renderHook(() => usePagedList<Row>(usersOptions))
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current).toMatchObject({ total: 200, limit: 25, pageCount: 8 })
    expect(result.current.rows).toHaveLength(25)
  })

  it('uses the limit echoed by the server, not the requested page size', async () => {
    const fetchMock = envelopeFetch(250)
    vi.stubGlobal('fetch', fetchMock)
    const { result } = renderHook(() => usePagedList<Row>({ ...usersOptions, pageSize: 500 }))
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current).toMatchObject({ limit: 100, pageCount: 3 })

    act(() => result.current.goToPage(2))
    await waitFor(() => expect(result.current.offset).toBe(100))
    expect(requestedUrls(fetchMock).at(-1)).toBe('/api/v1/mqtt-users?limit=500&offset=100')
  })

  it('debounces setSearch, writes it to the URL with replaceState and resets to page 1', async () => {
    window.history.replaceState(null, '', '/users?users.page=3&logs=keep')
    const fetchMock = envelopeFetch(200)
    vi.stubGlobal('fetch', fetchMock)
    const pushState = vi.spyOn(window.history, 'pushState')
    const { result } = renderHook(() => usePagedList<Row>(usersOptions))
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(requestedUrls(fetchMock)).toEqual(['/api/v1/mqtt-users?limit=25&offset=50'])

    act(() => result.current.setSearch('sens'))
    act(() => result.current.setSearch('sensor 01'))
    expect(result.current.search).toBe('sensor 01')
    expect(fetchMock).toHaveBeenCalledTimes(1)

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(requestedUrls(fetchMock)[1]).toBe('/api/v1/mqtt-users?q=sensor+01&limit=25&offset=0')
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current).toMatchObject({ page: 1, hasFilters: true })
    expect(window.location.pathname).toBe('/users')
    expect(new URLSearchParams(window.location.search).get('users.q')).toBe('sensor 01')
    expect(new URLSearchParams(window.location.search).get('users.page')).toBeNull()
    expect(new URLSearchParams(window.location.search).get('logs')).toBe('keep')
    expect(pushState).not.toHaveBeenCalled()
  })

  it('setFilter sends the filter param immediately and resets to page 1', async () => {
    window.history.replaceState(null, '', '/acl?acl.page=2')
    const fetchMock = envelopeFetch(80, 'rules')
    vi.stubGlobal('fetch', fetchMock)
    const { result } = renderHook(() => usePagedList<Row>(aclOptions))
    await waitFor(() => expect(result.current.loading).toBe(false))

    act(() => result.current.setFilter('topic', 'sensors/#'))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(requestedUrls(fetchMock)[1]).toBe('/api/v1/acl?topic=sensors%2F%23&limit=25&offset=0')
    expect(result.current).toMatchObject({ page: 1, hasFilters: true, filters: { topic: 'sensors/#' } })
    expect(window.location.search).toBe('?acl.topic=sensors%2F%23')

    act(() => result.current.setFilter('topic', ''))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3))
    expect(requestedUrls(fetchMock)[2]).toBe('/api/v1/acl?limit=25&offset=0')
    expect(result.current.hasFilters).toBe(false)
    expect(window.location.search).toBe('')
  })

  it('goToPage moves the offset by the page size and clamps out-of-range pages', async () => {
    const fetchMock = envelopeFetch(60)
    vi.stubGlobal('fetch', fetchMock)
    const { result } = renderHook(() => usePagedList<Row>(usersOptions))
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.pageCount).toBe(3)

    act(() => result.current.goToPage(2))
    await waitFor(() => expect(result.current.offset).toBe(25))
    expect(result.current.page).toBe(2)
    expect(window.location.search).toBe('?users.page=2')

    act(() => result.current.goToPage(99))
    await waitFor(() => expect(result.current.offset).toBe(50))
    expect(result.current.page).toBe(3)
    expect(result.current.rows).toHaveLength(10)

    act(() => result.current.goToPage(-4))
    await waitFor(() => expect(result.current.offset).toBe(0))
    expect(result.current.page).toBe(1)
    expect(requestedUrls(fetchMock)).toEqual([
      '/api/v1/mqtt-users?limit=25&offset=0',
      '/api/v1/mqtt-users?limit=25&offset=25',
      '/api/v1/mqtt-users?limit=25&offset=50',
      '/api/v1/mqtt-users?limit=25&offset=0',
    ])
  })

  it('restores search, filters and page from location.search on mount, without debounce', async () => {
    window.history.replaceState(null, '', '/acl?acl.user=alice&acl.topic=a%2Fb&acl.page=2&users.q=other')
    const fetchMock = envelopeFetch(60, 'rules')
    vi.stubGlobal('fetch', fetchMock)
    const { result } = renderHook(() => usePagedList<Row>(aclOptions))
    expect(result.current).toMatchObject({ page: 2, filters: { user: 'alice', topic: 'a/b' }, hasFilters: true })
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(requestedUrls(fetchMock)).toEqual(['/api/v1/acl?user=alice&topic=a%2Fb&limit=25&offset=25'])
    expect(new URLSearchParams(window.location.search).get('users.q')).toBe('other')
  })

  it('falls back to the last page when the restored page is past the end', async () => {
    window.history.replaceState(null, '', '/users?users.page=9')
    const fetchMock = envelopeFetch(30)
    vi.stubGlobal('fetch', fetchMock)
    const { result } = renderHook(() => usePagedList<Row>(usersOptions))
    await waitFor(() => expect(result.current.offset).toBe(25))
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current).toMatchObject({ page: 2, pageCount: 2 })
    expect(result.current.rows).toHaveLength(5)
  })

  it('sets error on a failed request, keeps the previous rows and recovers on reload', async () => {
    let fail = false
    const ok = envelopeFetch(2)
    const fetchMock = vi.fn(async (input: string | URL | Request) => {
      if (fail) return jsonResponse({ error: 'database is locked' }, { status: 500 })
      return ok(input)
    })
    vi.stubGlobal('fetch', fetchMock)
    const { result } = renderHook(() => usePagedList<Row>(usersOptions))
    await waitFor(() => expect(result.current.loading).toBe(false))

    fail = true
    act(() => result.current.reload())
    await waitFor(() => expect(result.current.error).toBe('database is locked'))
    expect(result.current.loading).toBe(false)
    expect(result.current.rows).toEqual([{ id: 1 }, { id: 2 }])

    fail = false
    act(() => result.current.reload())
    await waitFor(() => expect(result.current.error).toBeNull())
    expect(fetchMock).toHaveBeenCalledTimes(3)
  })

  it('reports a network failure or a malformed body as error without throwing', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('Failed to fetch') }))
    const network = renderHook(() => usePagedList<Row>(usersOptions))
    await waitFor(() => expect(network.result.current.error).toBe('Failed to load the list.'))
    expect(network.result.current.rows).toEqual([])
    network.unmount()

    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse([{ id: 1 }])))
    const malformed = renderHook(() => usePagedList<Row>(usersOptions))
    await waitFor(() => expect(malformed.result.current.error).toBe('Unexpected response from the server.'))
  })

  it('calls onUnauthorized on 401 without surfacing an error', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ error: 'unauthorized' }, { status: 401 })))
    const onUnauthorized = vi.fn()
    const { result } = renderHook(() => usePagedList<Row>({ ...usersOptions, onUnauthorized }))
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1))
    expect(result.current.error).toBeNull()
  })
})
