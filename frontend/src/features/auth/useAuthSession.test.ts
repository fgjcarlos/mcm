import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAuthSession, type AdminUser } from './useAuthSession'

type RouteHandler = (init?: RequestInit) => Response | Promise<Response>

class LocalStorageMock implements Storage {
  private store = new Map<string, string>()

  get length() {
    return this.store.size
  }

  clear() {
    this.store.clear()
  }

  getItem(key: string) {
    return this.store.get(key) ?? null
  }

  key(index: number) {
    return Array.from(this.store.keys())[index] ?? null
  }

  removeItem(key: string) {
    this.store.delete(key)
  }

  setItem(key: string, value: string) {
    this.store.set(key, String(value))
  }
}

function jsonResponse(body: unknown, init?: ResponseInit) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
}

function installFetchMock(routes: Record<string, RouteHandler>) {
  return vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const requestUrl =
      typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
    const key = `${method.toUpperCase()} ${requestUrl}`
    const handler = routes[key]

    if (!handler) {
      throw new Error(`Unhandled fetch: ${key}`)
    }

    return await handler(init)
  })
}

const sampleUser: AdminUser = {
  id: 7,
  username: 'operator',
  disabled: false,
  role: 'operator',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

describe('useAuthSession', () => {
  const localStorageMock = new LocalStorageMock()

  beforeEach(() => {
    localStorageMock.clear()
    Object.defineProperty(window, 'localStorage', {
      value: localStorageMock,
      configurable: true,
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('init reads token from localStorage when key is present', () => {
    localStorageMock.setItem('mcm_admin_token', 'stored-token-abc')

    // No fetch needed — token read happens synchronously from localStorage
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {}))) // never resolves in this test

    const { result } = renderHook(() => useAuthSession())

    expect(result.current.token).toBe('stored-token-abc')
  })

  it('init has null token and isRestoring false when localStorage is empty', () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)

    const { result } = renderHook(() => useAuthSession())

    expect(result.current.token).toBeNull()
    expect(result.current.isRestoring).toBe(false)
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('login persists token to localStorage and updates state', async () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {}))) // no restore triggered after login in this test

    const { result } = renderHook(() => useAuthSession())

    await act(async () => {
      result.current.handleLogin('tok-123', sampleUser)
    })

    expect(window.localStorage.getItem('mcm_admin_token')).toBe('tok-123')
    expect(result.current.token).toBe('tok-123')
    expect(result.current.currentUser).toEqual(sampleUser)
    expect(result.current.isRestoring).toBe(false)
  })

  it('logout clears localStorage and resets state', async () => {
    localStorageMock.setItem('mcm_admin_token', 'active-token')

    const fetchMock = installFetchMock({
      'GET /api/v1/auth/me': () =>
        jsonResponse(sampleUser),
    })
    vi.stubGlobal('fetch', fetchMock)

    const { result } = renderHook(() => useAuthSession())

    // Wait for restore to complete so currentUser is set
    await waitFor(() => expect(result.current.currentUser).not.toBeNull())

    await act(async () => {
      result.current.handleLogout()
    })

    expect(window.localStorage.getItem('mcm_admin_token')).toBeNull()
    expect(result.current.token).toBeNull()
    expect(result.current.currentUser).toBeNull()
  })

  it('restore success — GET /api/v1/auth/me 200 sets currentUser and transitions isRestoring to false', async () => {
    localStorageMock.setItem('mcm_admin_token', 'restore-token')

    const fetchMock = installFetchMock({
      'GET /api/v1/auth/me': () => jsonResponse(sampleUser),
    })
    vi.stubGlobal('fetch', fetchMock)

    const { result } = renderHook(() => useAuthSession())

    // While in-flight, isRestoring should be true
    expect(result.current.isRestoring).toBe(true)

    await waitFor(() => expect(result.current.currentUser).toEqual(sampleUser))

    expect(result.current.isRestoring).toBe(false)
    expect(result.current.token).toBe('restore-token')
  })

  it('restore 401 — clears token and localStorage (logs out)', async () => {
    localStorageMock.setItem('mcm_admin_token', 'expired-token')

    const fetchMock = installFetchMock({
      'GET /api/v1/auth/me': () => new Response(null, { status: 401 }),
    })
    vi.stubGlobal('fetch', fetchMock)

    const { result } = renderHook(() => useAuthSession())

    await waitFor(() => expect(result.current.token).toBeNull())

    expect(window.localStorage.getItem('mcm_admin_token')).toBeNull()
    expect(result.current.currentUser).toBeNull()
  })

  it('isRestoring transitions true → false after restore completes', async () => {
    localStorageMock.setItem('mcm_admin_token', 'in-flight-token')

    let resolveResponse!: (value: Response) => void
    const pendingFetch = new Promise<Response>((resolve) => {
      resolveResponse = resolve
    })

    vi.stubGlobal('fetch', vi.fn(() => pendingFetch))

    const { result } = renderHook(() => useAuthSession())

    // Token present, user not yet set → isRestoring should be true
    expect(result.current.token).toBe('in-flight-token')
    expect(result.current.currentUser).toBeNull()
    expect(result.current.isRestoring).toBe(true)

    // Resolve the fetch
    await act(async () => {
      resolveResponse(jsonResponse(sampleUser))
      await pendingFetch
    })

    await waitFor(() => expect(result.current.isRestoring).toBe(false))
    expect(result.current.currentUser).toEqual(sampleUser)
  })

  it('restore network error — triggers logout (token cleared, localStorage cleared)', async () => {
    localStorageMock.setItem('mcm_admin_token', 'network-error-token')

    // Stub fetch to reject with a network error
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new Error('Network failure'))),
    )

    const { result } = renderHook(() => useAuthSession())

    // Restore is in-flight: token set, user null
    expect(result.current.token).toBe('network-error-token')
    expect(result.current.isRestoring).toBe(true)

    // catch path → handleLogout() → token and localStorage cleared
    await waitFor(() => expect(result.current.token).toBeNull())

    expect(window.localStorage.getItem('mcm_admin_token')).toBeNull()
    expect(result.current.currentUser).toBeNull()
  })

  it('restore effect is cancelled on unmount — setCurrentUser not called after unmount', async () => {
    localStorageMock.setItem('mcm_admin_token', 'cancel-token')

    let resolveResponse!: (value: Response) => void
    const pendingFetch = new Promise<Response>((resolve) => {
      resolveResponse = resolve
    })

    vi.stubGlobal('fetch', vi.fn(() => pendingFetch))

    const { result, unmount } = renderHook(() => useAuthSession())

    // Restore is in-flight
    expect(result.current.isRestoring).toBe(true)

    // Unmount before the fetch resolves — this sets cancelled = true in cleanup
    unmount()

    // Resolve the fetch AFTER unmount; the cancelled guard should prevent any setState
    await act(async () => {
      resolveResponse(jsonResponse(sampleUser))
      await pendingFetch
    })

    // currentUser was never set because the hook was unmounted before the fetch resolved
    expect(result.current.currentUser).toBeNull()
  })

  it('handleLogout reference is stable across re-renders', async () => {
    // Start with no token so no fetch is triggered
    vi.stubGlobal('fetch', vi.fn())

    const { result, rerender } = renderHook(() => useAuthSession())

    const logoutBefore = result.current.handleLogout

    // Force a re-render without changing any state
    rerender()

    const logoutAfter = result.current.handleLogout

    // useCallback([], []) guarantees the same reference across renders
    expect(logoutAfter).toBe(logoutBefore)
  })
})
