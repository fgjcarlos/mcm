import { describe, expect, it, vi } from 'vitest'
import {
  ForbiddenResponseError,
  isForbiddenResponseError,
  isUnauthorizedResponseError,
  authenticatedFetch,
} from './client'

describe('ForbiddenResponseError', () => {
  it('is an instance of Error', () => {
    const err = new ForbiddenResponseError()
    expect(err).toBeInstanceOf(Error)
  })

  it('has a friendly default message', () => {
    const err = new ForbiddenResponseError()
    expect(err.message).toBe("You don't have permission to perform this action.")
  })

  it('accepts a custom message', () => {
    const err = new ForbiddenResponseError('Custom error')
    expect(err.message).toBe('Custom error')
  })
})

describe('isForbiddenResponseError()', () => {
  it('returns true for a ForbiddenResponseError instance', () => {
    expect(isForbiddenResponseError(new ForbiddenResponseError())).toBe(true)
  })

  it('returns false for a plain Error', () => {
    expect(isForbiddenResponseError(new Error('nope'))).toBe(false)
  })

  it('returns false for a non-error value', () => {
    expect(isForbiddenResponseError('string')).toBe(false)
    expect(isForbiddenResponseError(null)).toBe(false)
  })
})

describe('authenticatedFetch 403 handling', () => {
  it('throws ForbiddenResponseError on a 403 response', async () => {
    const onUnauthorized = vi.fn()
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: 'insufficient role' }), { status: 403 })),
    )

    await expect(
      authenticatedFetch('/api/v1/acls', { token: 'tok', onUnauthorized }),
    ).rejects.toBeInstanceOf(ForbiddenResponseError)

    vi.unstubAllGlobals()
  })

  it('does NOT call onUnauthorized on a 403 response', async () => {
    const onUnauthorized = vi.fn()
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: 'insufficient role' }), { status: 403 })),
    )

    await expect(
      authenticatedFetch('/api/v1/acls', { token: 'tok', onUnauthorized }),
    ).rejects.toBeInstanceOf(ForbiddenResponseError)

    expect(onUnauthorized).not.toHaveBeenCalled()
    vi.unstubAllGlobals()
  })

  it('uses the friendly default message when the body error is "insufficient role"', async () => {
    const onUnauthorized = vi.fn()
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: 'insufficient role' }), { status: 403 })),
    )

    const err = await authenticatedFetch('/api/v1/acls', { token: 'tok', onUnauthorized }).catch((e: unknown) => e)
    expect((err as ForbiddenResponseError).message).toBe("You don't have permission to perform this action.")
    vi.unstubAllGlobals()
  })

  it('still calls onUnauthorized and throws UnauthorizedResponseError on a 401 response', async () => {
    const onUnauthorized = vi.fn()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 401 })))

    await expect(
      authenticatedFetch('/api/v1/acls', { token: 'tok', onUnauthorized }),
    ).rejects.toSatisfy(isUnauthorizedResponseError)

    expect(onUnauthorized).toHaveBeenCalledOnce()
    vi.unstubAllGlobals()
  })

  // FIX 3: empty-string error body falls back to friendly default (not empty message)
  it('uses the friendly default when the body error is an empty string', async () => {
    const onUnauthorized = vi.fn()
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: '' }), { status: 403 })),
    )

    const err = await authenticatedFetch('/api/v1/acls', { token: 'tok', onUnauthorized }).catch((e: unknown) => e)
    expect((err as ForbiddenResponseError).message).toBe("You don't have permission to perform this action.")
    vi.unstubAllGlobals()
  })

  // FIX 6: non-"insufficient role" 403 body — server message is passed through
  it('passes through a non-"insufficient role" 403 server message', async () => {
    const onUnauthorized = vi.fn()
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: 'feature disabled' }), { status: 403 })),
    )

    const err = await authenticatedFetch('/api/v1/acls', { token: 'tok', onUnauthorized }).catch((e: unknown) => e)
    expect((err as ForbiddenResponseError).message).toBe('feature disabled')
    vi.unstubAllGlobals()
  })

  // FIX 6: non-JSON 403 body falls back to friendly default
  it('uses the friendly default when the 403 body is not valid JSON', async () => {
    const onUnauthorized = vi.fn()
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response('not json at all', { status: 403 })),
    )

    const err = await authenticatedFetch('/api/v1/acls', { token: 'tok', onUnauthorized }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ForbiddenResponseError)
    expect((err as ForbiddenResponseError).message).toBe("You don't have permission to perform this action.")
    vi.unstubAllGlobals()
  })
})
