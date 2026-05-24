import type {
  ACLRule,
  AdminUser,
  AuditEvent,
  Deployment,
  DeployPreview,
  LoginResponse,
  MQTTUser,
  SecurityEvent,
  StatusResponse,
} from '../types'

type FetchOptions = {
  method?: string
  body?: string
  token?: string
}

async function apiFetch<T>(path: string, options: FetchOptions = {}): Promise<T> {
  const headers: Record<string, string> = {}

  if (options.token) {
    headers['Authorization'] = `Bearer ${options.token}`
  }

  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
  }

  const response = await fetch(path, {
    method: options.method ?? 'GET',
    headers,
    body: options.body,
  })

  if (!response.ok) {
    const status = response.status
    let message = `Request failed with status ${status}`
    try {
      const errBody = (await response.json()) as { error?: string; details?: string[] }
      const details = errBody.details?.join('; ')
      message = details ?? errBody.error ?? message
    } catch {
      // ignore JSON parse errors — keep default message
    }
    const error = new Error(message) as Error & { status: number }
    error.status = status
    throw error
  }

  return response.json() as Promise<T>
}

// Auth

export function login(username: string, password: string): Promise<LoginResponse> {
  return apiFetch<LoginResponse>('/api/v1/auth/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  })
}

export function loginMFA(mfa_challenge: string, code: string): Promise<LoginResponse> {
  return apiFetch<LoginResponse>('/api/v1/auth/login/mfa', {
    method: 'POST',
    body: JSON.stringify({ mfa_challenge, code }),
  })
}

export function getMe(token: string): Promise<AdminUser> {
  return apiFetch<AdminUser>('/api/v1/auth/me', { token })
}

// Status

export function getStatus(): Promise<StatusResponse> {
  return apiFetch<StatusResponse>('/api/v1/status')
}

// ACLs

export function listACLs(token: string): Promise<{ rules: ACLRule[] }> {
  return apiFetch<{ rules: ACLRule[] }>('/api/v1/acls', { token })
}

export function createACL(
  token: string,
  data: { principal: string; topic_filter: string; permission: ACLRule['permission']; description: string },
): Promise<ACLRule> {
  return apiFetch<ACLRule>('/api/v1/acls', {
    method: 'POST',
    token,
    body: JSON.stringify(data),
  })
}

export function updateACL(
  token: string,
  id: string,
  data: { principal: string; topic_filter: string; permission: ACLRule['permission']; description: string },
): Promise<ACLRule> {
  return apiFetch<ACLRule>(`/api/v1/acls/${id}`, {
    method: 'PUT',
    token,
    body: JSON.stringify(data),
  })
}

export function deleteACL(token: string, id: string): Promise<void> {
  return apiFetch<void>(`/api/v1/acls/${id}`, { method: 'DELETE', token })
}

// MQTT Users

export function listMQTTUsers(token: string): Promise<MQTTUser[]> {
  return apiFetch<MQTTUser[]>('/api/v1/mqtt-users', { token })
}

export function createMQTTUser(token: string, username: string): Promise<MQTTUser & { password: string }> {
  return apiFetch<MQTTUser & { password: string }>('/api/v1/mqtt-users', {
    method: 'POST',
    token,
    body: JSON.stringify({ username }),
  })
}

export function updateMQTTUser(token: string, id: number, data: { disabled: boolean }): Promise<MQTTUser> {
  return apiFetch<MQTTUser>(`/api/v1/mqtt-users/${id}`, {
    method: 'PUT',
    token,
    body: JSON.stringify(data),
  })
}

export function deleteMQTTUser(token: string, id: number): Promise<void> {
  return apiFetch<void>(`/api/v1/mqtt-users/${id}`, { method: 'DELETE', token })
}

export function resetPassword(token: string, userId: number): Promise<MQTTUser & { password: string }> {
  return apiFetch<MQTTUser & { password: string }>(`/api/v1/mqtt-users/${userId}/reset-password`, {
    method: 'POST',
    token,
  })
}

// Deployments

export function listDeployments(token: string): Promise<{ deployments: Deployment[] }> {
  return apiFetch<{ deployments: Deployment[] }>('/api/v1/deployments', { token })
}

export function deployPreview(token: string): Promise<DeployPreview> {
  return apiFetch<DeployPreview>('/api/v1/deployments/preview', { method: 'POST', token })
}

export function deployApply(token: string): Promise<Deployment> {
  return apiFetch<Deployment>('/api/v1/deployments/apply', { method: 'POST', token })
}

// Audit & Security Events

export function listAuditEvents(token: string, limit = 25): Promise<{ events?: AuditEvent[] }> {
  return apiFetch<{ events?: AuditEvent[] }>(`/api/v1/audit-events?limit=${limit}`, { token })
}

export function listSecurityEvents(token: string, limit = 50): Promise<{ events?: SecurityEvent[] }> {
  return apiFetch<{ events?: SecurityEvent[] }>(`/api/v1/security/events?limit=${limit}`, { token })
}

// Settings (placeholder — no response type needed yet)

export function getSettings(token: string): Promise<unknown> {
  return apiFetch<unknown>('/api/v1/settings', { token })
}
