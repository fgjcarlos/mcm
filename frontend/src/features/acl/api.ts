import { authenticatedFetch } from '../api/client'
import type {
  ACLScope,
  ACLScopeDescriptor,
  ACLSource,
  EffectiveScope,
  SimulationDecision,
} from './types'

// The scopes the ACL rules live in.
export async function fetchACLScopes(token: string, onUnauthorized: () => void): Promise<ACLScopeDescriptor[]> {
  const response = await authenticatedFetch('/api/v1/acls/scopes', { token, onUnauthorized })
  if (!response.ok) throw new Error('Failed to load the ACL scopes.')
  const body = (await response.json()) as { scopes?: ACLScopeDescriptor[] }
  return body.scopes ?? []
}

// The effective permissions of every identity, per scope.
export async function fetchEffectivePermissions(token: string, onUnauthorized: () => void): Promise<EffectiveScope[]> {
  const response = await authenticatedFetch('/api/v1/acls/effective', { token, onUnauthorized })
  if (!response.ok) throw new Error('Failed to load the effective permissions.')
  const body = (await response.json()) as { scopes?: EffectiveScope[] }
  return body.scopes ?? []
}

// The answer of the access simulator.
export async function simulateAccess(
  token: string,
  onUnauthorized: () => void,
  query: { scope: ACLScope; listener_id: string; username: string; client_id: string; topic: string; access: string },
): Promise<SimulationDecision> {
  const response = await authenticatedFetch('/api/v1/acls/effective/simulate', {
    token,
    onUnauthorized,
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(query),
  })
  if (!response.ok) {
    const body = (await response.json().catch(() => null)) as { error?: string; details?: string[] } | null
    throw new Error(body?.details?.join('; ') ?? body?.error ?? 'The simulation failed.')
  }
  return (await response.json()) as SimulationDecision
}

// The adopted ACL source of the broker-wide ACL file.
export async function fetchACLSource(token: string, onUnauthorized: () => void): Promise<ACLSource> {
  const response = await authenticatedFetch('/api/v1/acls/source', { token, onUnauthorized })
  if (!response.ok) throw new Error('Failed to load the adopted ACL source.')
  return (await response.json()) as ACLSource
}

// Adopts an existing ACL file so the deploy composes the managed rules
// into it instead of replacing it.
export async function adoptACLSource(
  token: string,
  onUnauthorized: () => void,
  payload: { path?: string; body?: string },
): Promise<ACLSource> {
  const response = await authenticatedFetch('/api/v1/acls/source/adopt', {
    token,
    onUnauthorized,
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!response.ok) {
    const body = (await response.json().catch(() => null)) as { error?: string; details?: string[] } | null
    throw new Error(body?.details?.join('; ') ?? body?.error ?? 'The adoption failed.')
  }
  return (await response.json()) as ACLSource
}
