import { describe, expect, it } from 'vitest'
import { composePortConflicts, validateListeners } from '../listenerValidation'
import type { ListenerSpec } from '../types'

const listener = (overrides: Partial<ListenerSpec> = {}): ListenerSpec => ({
  ID: 'mqtt',
  Port: 1883,
  Bind: '0.0.0.0',
  Protocols: ['mqtt'],
  ...overrides,
})

describe('production listener panel-data hooks', () => {
  it('accepts a well-formed listener', () => {
    expect(validateListeners([listener()]).issues).toEqual([])
  })

  it('reports duplicate listener tuples', () => {
    const issues = validateListeners([
      listener(),
      listener({ ID: 'duplicate' }),
    ]).issues

    expect(issues).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'duplicate', listener_id: 'duplicate' }),
    ]))
  })

  it('maps compose host-port conflicts to listener IDs', () => {
    expect(composePortConflicts([
      listener(),
      listener({ ID: 'secure', Port: 8883, Protocols: ['mqtt'] }),
      listener({ ID: 'web', Port: 8083, Protocols: ['websockets'] }),
    ], [1883, 8883])).toEqual({ mqtt: [1883], secure: [8883] })
  })
})
