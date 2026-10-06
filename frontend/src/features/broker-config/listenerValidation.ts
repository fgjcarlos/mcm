import type { ListenerIssue, ListenerSpec } from './types'

const allowedProtocols = new Set(['mqtt', 'mqtts', 'ws', 'wss'])

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
  if (!bind.includes(':') || !/^[\da-f:]+$/i.test(bind)) return false
  try {
    return new URL(`http://[${bind}]/`).hostname.toLowerCase() === `[${bind}]`.toLowerCase()
  } catch {
    return false
  }
}

function isIPAddress(bind: string): boolean {
  return isIPv4(bind) || isIPv6(bind)
}

export function validateListeners(specs: ListenerSpec[]): ListenerValidationResult {
  const issues: ListenerIssue[] = []
  const warnings: ListenerIssue[] = []
  const seen = new Set<string>()

  for (const spec of specs) {
    if (!isIPAddress(spec.bind)) {
      issues.push({ kind: 'bind', listener_id: spec.id, message: 'bind must be an IP address' })
    }
    if (!Number.isInteger(spec.port) || spec.port < 1 || spec.port > 65535) {
      issues.push({ kind: 'port_range', listener_id: spec.id, message: 'port must be between 1 and 65535' })
    }
    if (spec.protocols.length === 0) {
      issues.push({ kind: 'protocols_required', listener_id: spec.id, message: 'at least one protocol is required' })
    }
    for (const protocol of spec.protocols) {
      if (!allowedProtocols.has(protocol)) {
        issues.push({ kind: 'protocols_unknown', listener_id: spec.id, message: `unknown protocol "${protocol}"` })
        continue
      }
      const key = `${spec.port}\0${spec.bind}\0${protocol}`
      if (seen.has(key)) {
        issues.push({
          kind: 'duplicate',
          listener_id: spec.id,
          message: `duplicate listener for port ${spec.port}, bind ${spec.bind}, protocol ${protocol}`,
        })
        continue
      }
      seen.add(key)
      if ((protocol === 'ws' || protocol === 'wss') && spec.port === 1883) {
        warnings.push({
          kind: 'websocket_default_port',
          listener_id: spec.id,
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
    if (composePorts.has(spec.port)) {
      conflicts[spec.id] ??= []
      if (!conflicts[spec.id].includes(spec.port)) conflicts[spec.id].push(spec.port)
    }
  }
  for (const ports of Object.values(conflicts)) ports.sort((a, b) => a - b)
  return conflicts
}
