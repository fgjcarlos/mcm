import { describe, expect, it } from 'vitest'
import { concreteTestTopic, planConnectionTest, topicMatchesFilter } from './connectionTest'

describe('concreteTestTopic', () => {
  it('replaces every wildcard level with a concrete level', () => {
    expect(concreteTestTopic('probe/allowed')).toBe('probe/allowed')
    expect(concreteTestTopic('sensors/#')).toBe('sensors/mcm-test')
    expect(concreteTestTopic('device/+/status')).toBe('device/mcm-test/status')
    expect(concreteTestTopic('#')).toBe('mcm-test')
    expect(concreteTestTopic('+/+')).toBe('mcm-test/mcm-test')
  })
})

describe('topicMatchesFilter', () => {
  it('follows the MQTT wildcard rules', () => {
    expect(topicMatchesFilter('sensors/#', 'sensors/a/b')).toBe(true)
    expect(topicMatchesFilter('sensors/#', 'sensors')).toBe(true)
    expect(topicMatchesFilter('device/+/status', 'device/a/status')).toBe(true)
    expect(topicMatchesFilter('device/+/status', 'device/a/b/status')).toBe(false)
    expect(topicMatchesFilter('probe/allowed', 'mcm-denied/x')).toBe(false)
    expect(topicMatchesFilter('#', 'mcm-denied/x')).toBe(true)
  })
})

describe('planConnectionTest', () => {
  const nonce = 'n0nc3'

  it('plans a publish the monitor can see and a denied publish outside the grant for readwrite', () => {
    const plan = planConnectionTest('probe/allowed', 'readwrite', nonce)
    expect(plan.allowed).toEqual({ operation: 'subscribe and publish', topic: 'probe/allowed', payload: 'mcm-test-n0nc3', observable: true })
    expect(plan.denied).toEqual({ operation: 'publish', topic: 'mcm-denied-n0nc3/probe', payload: 'mcm-denied-n0nc3', observable: true })
  })

  it('plans a publish for write and a refused subscription the monitor cannot see', () => {
    const plan = planConnectionTest('sensors/#', 'write', nonce)
    expect(plan.allowed).toEqual({ operation: 'publish', topic: 'sensors/mcm-test', payload: 'mcm-test-n0nc3', observable: true })
    expect(plan.denied).toEqual({ operation: 'subscribe', topic: 'sensors/mcm-test', payload: '', observable: false })
  })

  it('plans a subscription for read that MCM cannot verify, and a denied publish on the same topic', () => {
    const plan = planConnectionTest('device/+/status', 'read', nonce)
    expect(plan.allowed).toEqual({ operation: 'subscribe', topic: 'device/mcm-test/status', payload: '', observable: false })
    expect(plan.denied).toEqual({ operation: 'publish', topic: 'device/mcm-test/status', payload: 'mcm-denied-n0nc3', observable: true })
  })

  it('plans no allowed operation for a deny rule', () => {
    const plan = planConnectionTest('probe/allowed', 'deny', nonce)
    expect(plan.allowed).toBeNull()
    expect(plan.denied).toEqual({ operation: 'publish', topic: 'probe/allowed', payload: 'mcm-denied-n0nc3', observable: true })
  })

  it('proposes no denied topic when the readwrite grant covers every topic', () => {
    const plan = planConnectionTest('#', 'readwrite', nonce)
    expect(plan.allowed?.topic).toBe('mcm-test')
    expect(plan.denied).toBeNull()
  })
})
