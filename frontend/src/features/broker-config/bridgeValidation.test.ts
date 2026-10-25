import { describe, expect, it } from 'vitest'
import {
  isValidHost,
  splitAddress,
  topicsOverlap,
  validateBridges,
  validateTopicPattern,
} from './bridgeValidation'
import type { Bridge } from './bridgeTypes'

function bridge(overrides: Partial<Bridge> = {}): Bridge {
  return {
    name: 'upstream',
    address: 'remote.example.org:1884',
    topics: [{ topic: 'both/#', direction: 'both', qos: 1 }],
    ...overrides,
  }
}

describe('splitAddress', () => {
  it('defaults the port to 1883', () => {
    expect(splitAddress('broker')).toEqual({ host: 'broker', port: 1883 })
  })

  it('reads an explicit port', () => {
    expect(splitAddress('broker:1884')).toEqual({ host: 'broker', port: 1884 })
  })

  it('reads an IPv6 literal', () => {
    expect(splitAddress('[::1]:1883')).toEqual({ host: '::1', port: 1883 })
    expect(splitAddress('::1')).toEqual({ host: '::1', port: 1883 })
  })

  it('refuses a scheme, a path, a bad port and an empty address', () => {
    expect(splitAddress('mqtt://broker')).toBeNull()
    expect(splitAddress('broker:1883/path')).toBeNull()
    expect(splitAddress('broker:0')).toBeNull()
    expect(splitAddress('broker:70000')).toBeNull()
    expect(splitAddress('broker:abc')).toBeNull()
    expect(splitAddress('')).toBeNull()
  })
})

describe('isValidHost', () => {
  it('accepts hostnames and IP addresses', () => {
    expect(isValidHost('broker.example.org')).toBe(true)
    expect(isValidHost('127.0.0.1')).toBe(true)
    expect(isValidHost('::1')).toBe(true)
  })

  it('refuses a malformed host', () => {
    expect(isValidHost('not a host')).toBe(false)
    expect(isValidHost('-leading-dash.org')).toBe(false)
  })
})

describe('validateTopicPattern', () => {
  it('accepts the MQTT wildcards', () => {
    expect(validateTopicPattern('both/#')).toBeNull()
    expect(validateTopicPattern('sensors/+/temp')).toBeNull()
    expect(validateTopicPattern('$SYS/broker/#')).toBeNull()
  })

  it('refuses a misplaced wildcard', () => {
    expect(validateTopicPattern('both/#/more')).not.toBeNull()
    expect(validateTopicPattern('both/sen+')).not.toBeNull()
    expect(validateTopicPattern('')).not.toBeNull()
  })
})

describe('topicsOverlap', () => {
  it('detects the filters that can match a common topic', () => {
    expect(topicsOverlap('a/b', 'a/b')).toBe(true)
    expect(topicsOverlap('a/#', 'a/b')).toBe(true)
    expect(topicsOverlap('a/+/c', 'a/b/c')).toBe(true)
    expect(topicsOverlap('a/b', 'a/c')).toBe(false)
    expect(topicsOverlap('a/b/c', 'a/b')).toBe(false)
  })
})

describe('validateBridges', () => {
  it('accepts a well formed bridge', () => {
    const result = validateBridges([bridge()])
    expect(result.issues).toEqual([])
    expect(result.warnings).toEqual([])
  })

  it('refuses a missing name', () => {
    const result = validateBridges([bridge({ name: '' })])
    expect(result.issues.some((issue) => issue.kind === 'name')).toBe(true)
  })

  it('refuses a name with spaces', () => {
    const result = validateBridges([bridge({ name: 'up stream' })])
    expect(result.issues.some((issue) => issue.kind === 'name')).toBe(true)
  })

  it('refuses a duplicate name', () => {
    const result = validateBridges([bridge(), bridge()])
    expect(result.issues.some((issue) => issue.kind === 'duplicate' && issue.field === 'name')).toBe(true)
  })

  it('refuses a duplicate remote client id', () => {
    const result = validateBridges([
      bridge({ name: 'one', remote_client_id: 'same' }),
      bridge({ name: 'two', remote_client_id: 'same' }),
    ])
    expect(result.issues.some((issue) => issue.kind === 'duplicate' && issue.field === 'remote_client_id')).toBe(true)
  })

  it('refuses a missing address', () => {
    const result = validateBridges([bridge({ address: '' })])
    expect(result.issues.some((issue) => issue.kind === 'address')).toBe(true)
  })

  it('refuses a malformed address', () => {
    const result = validateBridges([bridge({ address: 'mqtt://broker:1883' })])
    expect(result.issues.some((issue) => issue.kind === 'address')).toBe(true)
  })

  it('refuses a bridge without a topic rule', () => {
    const result = validateBridges([bridge({ topics: [] })])
    expect(result.issues.some((issue) => issue.kind === 'dependency' && issue.field === 'topics')).toBe(true)
  })

  it('refuses a duplicate topic rule', () => {
    const result = validateBridges([
      bridge({
        topics: [
          { topic: 'both/#', direction: 'both', qos: 1 },
          { topic: 'both/#', direction: 'both', qos: 0 },
        ],
      }),
    ])
    expect(result.issues.some((issue) => issue.kind === 'duplicate' && issue.field === 'topics')).toBe(true)
  })

  it('refuses an out of range quality of service', () => {
    const result = validateBridges([bridge({ topics: [{ topic: 'both/#', direction: 'both', qos: 5 }] })])
    expect(result.issues.some((issue) => issue.kind === 'value')).toBe(true)
  })

  it('refuses a client certificate without a private key', () => {
    const result = validateBridges([bridge({ tls: { cert_file: '/client.pem' } })])
    expect(result.issues.some((issue) => issue.kind === 'dependency' && issue.field === 'tls.key_file')).toBe(true)
  })

  it('refuses a private key without a client certificate', () => {
    const result = validateBridges([bridge({ tls: { key_file: '/client.key' } })])
    expect(result.issues.some((issue) => issue.kind === 'dependency' && issue.field === 'tls.cert_file')).toBe(true)
  })

  it('refuses a notification topic when the notifications are disabled', () => {
    const result = validateBridges([bridge({ notifications: false, notification_topic: '$SYS/broker/connection/upstream/state' })])
    expect(result.issues.some((issue) => issue.kind === 'dependency' && issue.field === 'notification_topic')).toBe(true)
  })

  it('warns about a user name without a password without blocking the change', () => {
    const result = validateBridges([bridge({ username: 'mcm' })])
    expect(result.issues).toEqual([])
    expect(result.warnings.some((warning) => warning.field === 'password')).toBe(true)
  })

  it('warns about an unverified remote certificate', () => {
    const result = validateBridges([bridge({ tls: { insecure: true } })])
    expect(result.issues).toEqual([])
    expect(result.warnings.some((warning) => warning.field === 'tls.insecure')).toBe(true)
  })

  it('refuses two rules of the same bridge that bounce the traffic back', () => {
    const result = validateBridges([
      bridge({
        topics: [
          { topic: 'data/#', direction: 'out', qos: 1 },
          { topic: 'data/#', direction: 'in', qos: 1 },
        ],
      }),
    ])
    expect(result.issues.some((issue) => issue.kind === 'loop' && issue.field === 'topics')).toBe(true)
  })

  it('refuses two bridges that share a remote broker and overlap', () => {
    const result = validateBridges([
      bridge({ name: 'one', address: 'remote.example.org:1884', topics: [{ topic: 'both/#', direction: 'both', qos: 1 }] }),
      bridge({ name: 'two', address: 'remote.example.org:1884', topics: [{ topic: 'both/+/x', direction: 'both', qos: 1 }] }),
    ])
    expect(result.issues.some((issue) => issue.kind === 'loop' && issue.field === 'address')).toBe(true)
  })

  it('accepts two bridges that share a remote broker but send in one direction only', () => {
    const result = validateBridges([
      bridge({ name: 'one', address: 'remote.example.org:1884', topics: [{ topic: 'out/#', direction: 'out', qos: 1 }] }),
      bridge({ name: 'two', address: 'remote.example.org:1884', topics: [{ topic: 'out/+/x', direction: 'out', qos: 1 }] }),
    ])
    expect(result.issues).toEqual([])
  })

  it('accepts two bridges that point at different remotes', () => {
    const result = validateBridges([
      bridge({ name: 'one', address: 'one.example.org:1884' }),
      bridge({ name: 'two', address: 'two.example.org:1884' }),
    ])
    expect(result.issues).toEqual([])
  })
})
