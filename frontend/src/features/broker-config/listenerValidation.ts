import type { ListenerIssue, ListenerSpec } from './types'

const allowedProtocols = new Set(['mqtt', 'websockets'])

export interface ListenerValidationResult {
  issues: ListenerIssue[]
  warnings: ListenerIssue[]
}

function isIPv4(bind: string): boolean {
  const octets = bind.split('.')
  return octets.length === 4 && octets.every((octet) =>
    /^(0|[1-9]\d{0,2})$/.test(octet) && Number(octet) <= 255,
  )
}

function isIPv6(bind: string): boolean {
  if (!bind.includes(':') || bind.includes('%')) return false

  let address = bind
  if (address.includes('.')) {
    const separator = address.lastIndexOf(':')
    if (separator < 0 || !isIPv4(address.slice(separator + 1))) return false
    const octets = bind.slice(separator + 1).split('.').map(Number)
    address = `${bind.slice(0, separator)}:${((octets[0] << 8) | octets[1]).toString(16)}:${((octets[2] << 8) | octets[3]).toString(16)}`
  }

  if (address.includes(':::')) return false
  const compression = address.indexOf('::')
  if (compression !== -1 && address.indexOf('::', compression + 2) !== -1) return false
  const groups = address.split(':')
  if (compression === -1) {
    return groups.length === 8 && groups.every((group) => /^[\da-f]{1,4}$/i.test(group))
  }

  const left = address.slice(0, compression).split(':').filter(Boolean)
  const right = address.slice(compression + 2).split(':').filter(Boolean)
  if (left.length + right.length >= 8) return false
  return [...left, ...right].every((group) => /^[\da-f]{1,4}$/i.test(group))
}

function isIPAddress(bind: string): boolean {
  return isIPv4(bind) || isIPv6(bind)
}

export function validateListeners(specs: ListenerSpec[]): ListenerValidationResult {
  const issues: ListenerIssue[] = []
  const warnings: ListenerIssue[] = []
  const seen = new Set<string>()

  for (const spec of specs) {
    if (!isIPAddress(spec.Bind)) {
      issues.push({ kind: 'bind', listener_id: spec.ID, message: 'bind must be an IP address' })
    }
    if (!Number.isInteger(spec.Port) || spec.Port < 1 || spec.Port > 65535) {
      issues.push({ kind: 'port_range', listener_id: spec.ID, message: 'port must be between 1 and 65535' })
    }
    if (spec.Protocols.length === 0) {
      issues.push({ kind: 'protocols_required', listener_id: spec.ID, message: 'at least one protocol is required' })
    }
    for (const protocol of spec.Protocols) {
      if (!allowedProtocols.has(protocol)) {
        issues.push({ kind: 'protocols_unknown', listener_id: spec.ID, message: `unknown protocol "${protocol}"` })
        continue
      }
      const key = `${spec.Port}\0${spec.Bind}\0${protocol}`
      if (seen.has(key)) {
        issues.push({
          kind: 'duplicate',
          listener_id: spec.ID,
          message: `duplicate listener for port ${spec.Port}, bind ${spec.Bind}, protocol ${protocol}`,
        })
        continue
      }
      seen.add(key)
      if (protocol === 'websockets' && spec.Port === 1883) {
        warnings.push({
          kind: 'websocket_default_port',
          listener_id: spec.ID,
          message: 'websocket protocols on port 1883 may conflict with the default MQTT listener',
        })
      }
    }
  }

  issues.sort((a, b) => (a.listener_id ?? '').localeCompare(b.listener_id ?? '') || a.kind.localeCompare(b.kind) || a.message.localeCompare(b.message))
  warnings.sort((a, b) => (a.listener_id ?? '').localeCompare(b.listener_id ?? '') || a.message.localeCompare(b.message))
  return { issues, warnings }
}

export function composePortConflicts(specs: ListenerSpec[], hostPorts: number[]): Record<string, number[]> {
  const composePorts = new Set(hostPorts)
  const conflicts: Record<string, number[]> = {}
  for (const spec of specs) {
    if (composePorts.has(spec.Port)) {
      conflicts[spec.ID] ??= []
      if (!conflicts[spec.ID].includes(spec.Port)) conflicts[spec.ID].push(spec.Port)
    }
  }
  for (const ports of Object.values(conflicts)) ports.sort((a, b) => a - b)
  return conflicts
}
