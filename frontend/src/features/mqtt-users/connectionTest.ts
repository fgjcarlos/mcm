import type { ACLPermission } from '../acl/types'

// The plan of the manual connection test of the onboarding journey (#353).
//
// The test is run by the operator's own MQTT client, never by MCM: MCM only
// proposes a concrete topic and a unique payload, then watches what the
// broker reports. An operation is "observable" when the MCM monitor (which
// reads `#`) would see its message on the broker; a subscription leaves no
// trace MCM can read.

export type PlannedOperation = {
  operation: 'publish' | 'subscribe' | 'subscribe and publish'
  topic: string
  // The unique payload MCM watches for. Empty for a subscription.
  payload: string
  observable: boolean
}

export type ConnectionTestPlan = {
  allowed: PlannedOperation | null
  denied: PlannedOperation | null
}

const TEST_LEVEL = 'mcm-test'

// A concrete topic inside the filter: every wildcard level becomes a
// concrete level, so any client can publish to it.
export function concreteTestTopic(filter: string): string {
  return filter
    .split('/')
    .map((level) => (level === '+' || level === '#' ? TEST_LEVEL : level))
    .join('/')
}

export function topicMatchesFilter(filter: string, topic: string): boolean {
  const filterLevels = filter.split('/')
  const topicLevels = topic.split('/')
  for (let index = 0; index < filterLevels.length; index += 1) {
    const level = filterLevels[index]
    if (level === '#') return true
    if (index >= topicLevels.length) return false
    if (level !== '+' && level !== topicLevels[index]) return false
  }
  return filterLevels.length === topicLevels.length
}

export function newTestNonce(): string {
  const bytes = new Uint8Array(4)
  globalThis.crypto.getRandomValues(bytes)
  return Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('')
}

export function planConnectionTest(filter: string, permission: ACLPermission, nonce: string): ConnectionTestPlan {
  const topic = concreteTestTopic(filter)
  const allowedPayload = `mcm-test-${nonce}`
  const deniedPayload = `mcm-denied-${nonce}`
  switch (permission) {
    case 'readwrite': {
      const deniedTopic = `mcm-denied-${nonce}/probe`
      return {
        allowed: { operation: 'subscribe and publish', topic, payload: allowedPayload, observable: true },
        denied: topicMatchesFilter(filter, deniedTopic)
          ? null
          : { operation: 'publish', topic: deniedTopic, payload: deniedPayload, observable: true },
      }
    }
    case 'write':
      return {
        allowed: { operation: 'publish', topic, payload: allowedPayload, observable: true },
        denied: { operation: 'subscribe', topic, payload: '', observable: false },
      }
    case 'read':
      return {
        allowed: { operation: 'subscribe', topic, payload: '', observable: false },
        denied: { operation: 'publish', topic, payload: deniedPayload, observable: true },
      }
    case 'deny':
      return {
        allowed: null,
        denied: { operation: 'publish', topic, payload: deniedPayload, observable: true },
      }
  }
}
