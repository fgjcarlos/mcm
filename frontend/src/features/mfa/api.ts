import { authenticatedFetch, isUnauthorizedResponseError, UnauthorizedResponseError } from '../api/client'

export type MFASetupResponse = {
  otpauth_url: string
  secret: string
  recovery_codes: string[]
}

export async function setupMFA(token: string, onUnauthorized: () => void): Promise<MFASetupResponse> {
  const response = await authenticatedFetch('/api/v1/auth/mfa/setup', {
    token,
    onUnauthorized,
    method: 'POST',
  })
  if (!response.ok) {
    const errBody = (await response.json().catch(() => null)) as { error?: string } | null
    throw new Error(errBody?.error ?? 'Failed to set up MFA.')
  }
  return response.json() as Promise<MFASetupResponse>
}

// verifyMFA and disableMFA use raw fetch because the backend returns 401 for
// wrong code / wrong password (not session expiry). authenticatedFetch would
// treat any 401 as a session expiry and call onUnauthorized(), which is wrong
// here. We add the Authorization header manually and only call onUnauthorized
// when the JWT itself is the problem (no user claims in context).
export async function verifyMFA(token: string, code: string, onUnauthorized: () => void): Promise<void> {
  const response = await fetch('/api/v1/auth/mfa/verify', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ code }),
  })
  if (response.status === 204) return
  const errBody = (await response.json().catch(() => null)) as { error?: string } | null
  if (response.status === 401) {
    const msg = errBody?.error ?? ''
    // If it's an MFA code rejection (not a session problem), surface as an error
    if (msg === 'invalid mfa code') {
      throw new Error(msg || 'Invalid MFA code.')
    }
    // Otherwise it is a real session expiry — log out
    onUnauthorized()
    throw new UnauthorizedResponseError()
  }
  throw new Error(errBody?.error ?? 'Failed to verify MFA code.')
}

export async function disableMFA(token: string, password: string, onUnauthorized: () => void): Promise<void> {
  const response = await fetch('/api/v1/auth/mfa', {
    method: 'DELETE',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ password }),
  })
  if (response.status === 204) return
  const errBody = (await response.json().catch(() => null)) as { error?: string } | null
  if (response.status === 401) {
    const msg = errBody?.error ?? ''
    // If it's a wrong-password rejection, surface as an error to the user
    if (msg === 'invalid credentials') {
      throw new Error(msg || 'Invalid password.')
    }
    // Otherwise it is a real session expiry — log out
    onUnauthorized()
    throw new UnauthorizedResponseError()
  }
  throw new Error(errBody?.error ?? 'Failed to disable MFA.')
}

export { isUnauthorizedResponseError }
