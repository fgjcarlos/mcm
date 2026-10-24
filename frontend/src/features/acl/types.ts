// Types of the native Mosquitto ACL surface.
//
// The vocabulary here is MQTT access control, not MCM administration:
// a "permission" is what Mosquitto grants on a topic (read, write,
// readwrite or deny), a "selector" is the native scope of the ACL entry
// (user, anonymous or pattern) and a "scope" is the ACL file the entry is
// rendered into (the broker-wide file or the file of one listener).
//
// The MCM administrative roles (viewer, auditor, operator, admin) are a
// different concept and live in the admin panel. They are never modelled
// here so the MQTT permissions panel cannot be read as a role panel.

export type ACLSelector = 'user' | 'anonymous' | 'pattern'
export type ACLScope = 'global' | 'listener'
export type ACLPermission = 'read' | 'write' | 'readwrite' | 'deny'

export type ACLRule = {
  id: string
  selector?: ACLSelector
  scope?: ACLScope
  listener_id?: string
  principal: string
  topic_filter: string
  permission: ACLPermission
  description?: string
}

export type ACLScopeDescriptor = {
  scope: ACLScope
  listener_id?: string
  path: string
  rule_count: number
  wired: boolean
  wiring_issue?: string
}

export type EffectiveGrant = {
  topic_filter: string
  permission: ACLPermission
  selector: ACLSelector
  principal?: string
  denied: boolean
  denied_by?: string[]
}

export type EffectiveIdentity = {
  identity: string
  kind: 'user' | 'anonymous'
  grants: EffectiveGrant[]
}

export type EffectiveScope = {
  scope: ACLScope
  listener_id?: string
  path: string
  identities: EffectiveIdentity[]
}

export type ACLSource = {
  path: string
  adopted: boolean
  body?: string
  content_hash?: string
}

export type SimulationDecision = {
  allowed: boolean
  reason: string
  matching_rules?: ACLRule[]
}

export const SELECTOR_LABELS: Record<ACLSelector, string> = {
  user: 'User',
  anonymous: 'Anonymous',
  pattern: 'Pattern',
}

export const PERMISSION_LABELS: Record<ACLPermission, string> = {
  read: 'read',
  write: 'write',
  readwrite: 'readwrite',
  deny: 'deny',
}
