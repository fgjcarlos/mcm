import { describe, expect, it } from 'vitest'
import { composePortConflicts, validateListeners } from './listenerValidation'
import type { ListenerSpec } from './types'

const spec = (overrides: Partial<ListenerSpec> = {}): ListenerSpec => ({
  id: 'mqtt',
  port: 1883,
  bind: '0.0.0.0',
  protocols: ['mqtt'],
  ...overrides,
})

describe('validateListeners', () => {
  it('accepts valid IPv4 and IPv6 listener specifications', () => {
    expect(validateListeners([spec(), spec({ id: 'secure', port: 8883, bind: '2001:db8::1', protocols: ['mqtts'] })])).toEqual({
      issues: [],
      warnings: [],
    })
  })

  it('reports invalid ports, non-IP binds, unsupported protocols, and duplicate tuples', () => {
    const result = validateListeners([
      spec({ id: 'invalid', port: 65536, bind: 'localhost', protocols: ['http'] }),
      spec({ id: 'duplicate', port: 1883, bind: '0.0.0.0', protocols: ['mqtt'] }),
      spec({ id: 'duplicate', port: 1883, bind: '0.0.0.0', protocols: ['mqtt'] }),
    ])

    expect(result.issues).toEqual(expect.arrayContaining([
      { kind: 'port_range', listener_id: 'invalid', message: 'port must be between 1 and 65535' },
      { kind: 'bind', listener_id: 'invalid', message: 'bind must be an IP address' },
      { kind: 'protocols_unknown', listener_id: 'invalid', message: 'unknown protocol "http"' },
      { kind: 'duplicate', listener_id: 'duplicate', message: 'duplicate listener for port 1883, bind 0.0.0.0, protocol mqtt' },
    ]))
  })

  it('emits non-blocking websocket-on-default-port hints', () => {
    expect(validateListeners([spec({ protocols: ['ws', 'wss'] })])).toEqual({
      issues: [],
      warnings: [
        { kind: 'websocket_default_port', listener_id: 'mqtt', message: 'websocket protocols on port 1883 may conflict with the default MQTT listener' },
        { kind: 'websocket_default_port', listener_id: 'mqtt', message: 'websocket protocols on port 1883 may conflict with the default MQTT listener' },
      ],
    })
  })
})

describe('composePortConflicts', () => {
  it('maps matching compose host ports to each listener id', () => {
    expect(composePortConflicts([
      spec(),
      spec({ id: 'web', port: 8083, protocols: ['ws'] }),
      spec({ id: 'secure', port: 8883, protocols: ['mqtts'] }),
    ], [8883, 1883])).toEqual({ mqtt: [1883], secure: [8883] })
  })
})
