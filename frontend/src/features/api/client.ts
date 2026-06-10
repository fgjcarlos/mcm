export class UnauthorizedResponseError extends Error {
  constructor() {
    super('Session expired.')
    this.name = 'UnauthorizedResponseError'
  }
}

export function isUnauthorizedResponseError(error: unknown): boolean {
  return error instanceof UnauthorizedResponseError
}

export async function authenticatedFetch(
  input: RequestInfo | URL,
  { token, onUnauthorized, headers, ...init }: RequestInit & { token: string; onUnauthorized: () => void },
) {
  const response = await fetch(input, {
    ...init,
    headers: withAuthHeader(headers, token),
  })

  if (response.status === 401) {
    onUnauthorized()
    throw new UnauthorizedResponseError()
  }

  return response
}

function withAuthHeader(headers: HeadersInit | undefined, token: string): Record<string, string> {
  if (!headers) return { Authorization: `Bearer ${token}` }

  if (headers instanceof Headers) {
    const result: Record<string, string> = {}
    headers.forEach((value, key) => {
      result[key] = value
    })
    result.Authorization = `Bearer ${token}`
    return result
  }

  if (Array.isArray(headers)) {
    return { ...Object.fromEntries(headers), Authorization: `Bearer ${token}` }
  }

  return { ...headers, Authorization: `Bearer ${token}` }
}
