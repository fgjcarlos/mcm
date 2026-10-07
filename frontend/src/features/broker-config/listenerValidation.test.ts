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
    expect(validateListeners([spec(), spec({ id: 'secure', port: 8883, bind: '2001:db8::1', protocols: ['mqtt'] })])).toEqual({
      issues: [],
      warnings: [],
    })
  })

  it.each([
    ['compressed IPv6', '2001:db8::1'],
    ['expanded IPv6', '2001:0db8:0000:0000:0000:0000:0000:0001'],
    ['leading-zero IPv6', '2001:DB8:0:0:0:0:0:0001'],
    ['mixed-case IPv6', '2001:Db8::ABCD'],
    ['IPv4-mapped IPv6', '::ffff:192.0.2.1'],
    ['IPv4-mapped hexadecimal IPv6', '::FFFF:C000:0201'],
    ['expanded IPv4-mapped IPv6', '0:0:0:0:0:ffff:192.0.2.1'],
  ])('accepts %s bind', (_name, bind) => {
    expect(validateListeners([spec({ bind })]).issues).toEqual([])
  })

  it.each([
    ['zone ID', 'fe80::1%eth0'],
    ['multiple compression markers', '2001::db8::1'],
    ['triple colon', '1:::2'],
    ['too many groups', '1:2:3:4:5:6:7:8:9'],
    ['too few uncompressed groups', '1:2:3:4:5:6:7'],
    ['invalid embedded IPv4', '::ffff:192.0.2.999'],
    ['malformed hexadecimal group', '2001:db8::gggg'],
  ])('rejects %s bind', (_name, bind) => {
    expect(validateListeners([spec({ bind })]).issues).toContainEqual(
      expect.objectContaining({ kind: 'bind', listener_id: 'mqtt' }),
    )
  })

  it.each(['mqtts', 'ws', 'wss'])('rejects unsupported protocol alias %s', (protocol) => {
    expect(validateListeners([spec({ protocols: [protocol] })]).issues).toContainEqual({
      kind: 'protocols_unknown', listener_id: 'mqtt', message: `unknown protocol "${protocol}"`,
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
    expect(validateListeners([spec({ protocols: ['websockets'] })])).toEqual({
      issues: [],
      warnings: [
        { kind: 'websocket_default_port', listener_id: 'mqtt', message: 'websocket protocols on port 1883 may conflict with the default MQTT listener' },
      ],
    })
  })
})

describe('composePortConflicts', () => {
  it('maps matching compose host ports to each listener id', () => {
    expect(composePortConflicts([
      spec(),
      spec({ id: 'web', port: 8083, protocols: ['websockets'] }),
      spec({ id: 'secure', port: 8883, protocols: ['mqtt'] }),
    ], [8883, 1883])).toEqual({ mqtt: [1883], secure: [8883] })
  })
})
