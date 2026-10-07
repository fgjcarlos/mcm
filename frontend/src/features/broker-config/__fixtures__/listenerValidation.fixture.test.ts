import { describe, expect, it } from 'vitest'
import { composePortConflicts, validateListeners } from '../listenerValidation'
import type { ListenerSpec } from '../types'

const listener = (overrides: Partial<ListenerSpec> = {}): ListenerSpec => ({
  id: 'mqtt',
  port: 1883,
  bind: '0.0.0.0',
  protocols: ['mqtt'],
  ...overrides,
})

describe('production listener panel-data hooks', () => {
  it('accepts a well-formed listener', () => {
    expect(validateListeners([listener()]).issues).toEqual([])
  })

  it('reports duplicate listener tuples', () => {
    const issues = validateListeners([
      listener(),
      listener({ id: 'duplicate' }),
    ]).issues

    expect(issues).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'duplicate', listener_id: 'duplicate' }),
    ]))
  })

  it('maps compose host-port conflicts to listener IDs', () => {
    expect(composePortConflicts([
      listener(),
      listener({ id: 'secure', port: 8883, protocols: ['mqtt'] }),
      listener({ id: 'web', port: 8083, protocols: ['websockets'] }),
    ], [1883, 8883])).toEqual({ mqtt: [1883], secure: [8883] })
  })
})
