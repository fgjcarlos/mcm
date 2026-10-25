import type { Bridge, BridgeTopicRule } from './bridgeTypes'

export interface BridgeValidationResult {
  issues: BridgeValidationIssue[]
  warnings: BridgeValidationIssue[]
}

export type BridgeValidationIssue = {
  kind: string
  bridge?: string
  field?: string
  topic?: string
  message: string
}

// namePattern mirrors the rule the server enforces: a connection name is an
// identifier in the broker log and in the $SYS topics it publishes.
const namePattern = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/

const hostnamePattern = /^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$/

export const bridgeDirections = ['both', 'in', 'out'] as const
export const bridgeProtocolVersions = ['mqttv311', 'mqttv50', 'mqttv31'] as const
export const bridgeStartTypes = ['automatic', 'lazy', 'once'] as const
export const bridgeTLSVersions = ['tlsv1.3', 'tlsv1.2'] as const

// splitAddress splits an address into its host and port. It mirrors the
// server rule: the port defaults to the standard MQTT port.
export function splitAddress(address: string): { host: string; port: number } | null {
  const trimmed = address.trim()
  if (!trimmed) return null
  if (trimmed.includes('://') || trimmed.includes('/') || /\s/.test(trimmed)) return null

  let host = trimmed
  let port = 1883
  if (trimmed.startsWith('[')) {
    const end = trimmed.indexOf(']')
    if (end < 0) return null
    host = trimmed.slice(1, end)
    const rest = trimmed.slice(end + 1)
    if (rest) {
      if (!rest.startsWith(':')) return null
      const parsed = Number(rest.slice(1))
      if (!Number.isInteger(parsed) || parsed < 1 || parsed > 65535) return null
      port = parsed
    }
    return host ? { host, port } : null
  }
  const colons = trimmed.split(':').length - 1
  if (colons === 1) {
    const [rawHost, rawPort] = trimmed.split(':')
    if (!/^\d+$/.test(rawPort)) return null
    const parsed = Number(rawPort)
    if (parsed < 1 || parsed > 65535) return null
    host = rawHost
    port = parsed
  }
  if (!host) return null
  return { host, port }
}

function isIPv4(host: string): boolean {
  return /^\d{1,3}(\.\d{1,3}){3}$/.test(host)
}

function isIPv6(host: string): boolean {
  return host.includes(':') && /^[0-9A-Fa-f:]+$/.test(host)
}

export function isValidHost(host: string): boolean {
  if (!host) return false
  if (isIPv4(host) || isIPv6(host)) return true
  if (host.length > 253) return false
  return hostnamePattern.test(host)
}

// validateTopicPattern checks an MQTT topic filter the way the MQTT
// specification allows it: "#" as the last level and "+" as a whole level.
export function validateTopicPattern(pattern: string): string | null {
  if (!pattern) return 'the topic pattern is empty'
  if (/\s/.test(pattern)) return 'the topic pattern must not contain spaces'
  if (pattern.startsWith('$')) return null
  const levels = pattern.split('/')
  for (let i = 0; i < levels.length; i += 1) {
    const level = levels[i]
    if (level.includes('#')) {
      if (level !== '#') return 'the multi-level wildcard must occupy a whole level'
      if (i !== levels.length - 1) return 'the multi-level wildcard must be the last level'
    }
    if (level.includes('+') && level !== '+') return 'the single-level wildcard must occupy a whole level'
  }
  return null
}

// topicsOverlap reports whether two MQTT topic filters can match a common
// topic. The loop detection uses it.
export function topicsOverlap(a: string, b: string): boolean {
  if (a === b) return true
  const levelsA = a.split('/')
  const levelsB = b.split('/')
  for (let i = 0; i < levelsA.length && i < levelsB.length; i += 1) {
    const la = levelsA[i]
    const lb = levelsB[i]
    if (la === '#' || lb === '#') return true
    if (la === '+' || lb === '+') continue
    if (la !== lb) return false
  }
  if (levelsA.length > levelsB.length) return levelsA[levelsB.length] === '#'
  if (levelsB.length > levelsA.length) return levelsB[levelsA.length] === '#'
  return true
}

function localTopic(rule: BridgeTopicRule): string {
  return joinTopic(rule.local_prefix, rule.topic)
}

function remoteTopic(rule: BridgeTopicRule): string {
  return joinTopic(rule.remote_prefix, rule.topic)
}

function joinTopic(prefix: string | undefined, topic: string): string {
  if (!prefix) return topic
  return `${prefix}/${topic.replace(/^\//, '')}`
}

function directionOf(rule: BridgeTopicRule): 'in' | 'out' | 'both' {
  return rule.direction ?? 'both'
}

function isOutbound(rule: BridgeTopicRule): boolean {
  const direction = directionOf(rule)
  return direction === 'out' || direction === 'both'
}

function isInbound(rule: BridgeTopicRule): boolean {
  const direction = directionOf(rule)
  return direction === 'in' || direction === 'both'
}

// validateBridges checks the bridge set the operator is editing. The
// server remains the authority; the checks here give the operator the
// answer without a round trip.
export function validateBridges(bridges: Bridge[]): BridgeValidationResult {
  const issues: BridgeValidationIssue[] = []
  const warnings: BridgeValidationIssue[] = []

  const seenNames = new Map<string, number>()
  const seenRemoteIds = new Map<string, string>()
  const seenLocalIds = new Map<string, string>()

  for (const bridge of bridges) {
    const name = bridge.name ?? ''
    if (!name.trim()) {
      issues.push({ kind: 'name', field: 'name', message: 'the bridge name is required' })
    } else if (!namePattern.test(name)) {
      issues.push({
        kind: 'name',
        bridge: name,
        field: 'name',
        message: 'the bridge name must start with a letter or a digit and may only contain letters, digits, dots, dashes and underscores',
      })
    }
    if (name) {
      const count = (seenNames.get(name) ?? 0) + 1
      seenNames.set(name, count)
      if (count > 1) {
        issues.push({ kind: 'duplicate', bridge: name, field: 'name', message: `bridge "${name}" is declared more than once` })
      }
    }

    if (bridge.remote_client_id) {
      const other = seenRemoteIds.get(bridge.remote_client_id)
      if (other) {
        issues.push({
          kind: 'duplicate',
          bridge: name,
          field: 'remote_client_id',
          message: `the remote client id "${bridge.remote_client_id}" is already used by the bridge "${other}"`,
        })
      } else {
        seenRemoteIds.set(bridge.remote_client_id, name)
      }
    }
    if (bridge.local_client_id) {
      const other = seenLocalIds.get(bridge.local_client_id)
      if (other) {
        issues.push({
          kind: 'duplicate',
          bridge: name,
          field: 'local_client_id',
          message: `the local client id "${bridge.local_client_id}" is already used by the bridge "${other}"`,
        })
      } else {
        seenLocalIds.set(bridge.local_client_id, name)
      }
    }

    const address = (bridge.address ?? '').trim()
    if (!address) {
      issues.push({ kind: 'address', bridge: name, field: 'address', message: 'the remote broker address is required' })
    } else {
      const parsed = splitAddress(address)
      if (!parsed) {
        issues.push({ kind: 'address', bridge: name, field: 'address', message: 'the remote broker address is not a valid host or host:port' })
      } else if (!isValidHost(parsed.host)) {
        issues.push({
          kind: 'address',
          bridge: name,
          field: 'address',
          message: `the remote broker host "${parsed.host}" is not a valid hostname or IP address`,
        })
      }
    }

    const rules = bridge.topics ?? []
    if (rules.length === 0) {
      issues.push({
        kind: 'dependency',
        bridge: name,
        field: 'topics',
        message: 'the bridge needs at least one topic rule; Mosquitto refuses a connection without topics',
      })
    }
    const seenRules = new Set<string>()
    for (const rule of rules) {
      const topic = rule.topic ?? ''
      const patternError = validateTopicPattern(topic)
      if (patternError) {
        issues.push({ kind: 'value', bridge: name, field: 'topics', topic, message: `the topic pattern "${topic}" is invalid: ${patternError}` })
      }
      if (rule.direction && !bridgeDirections.includes(rule.direction)) {
        issues.push({
          kind: 'value',
          bridge: name,
          field: 'topics',
          topic,
          message: `the topic direction "${rule.direction}" must be one of both, in, out`,
        })
      }
      if (rule.qos < 0 || rule.qos > 2) {
        issues.push({
          kind: 'value',
          bridge: name,
          field: 'topics',
          topic,
          message: `the topic quality of service ${rule.qos} must be 0, 1 or 2`,
        })
      }
      if (rule.local_prefix) {
        const prefixError = validateTopicPattern(rule.local_prefix)
        if (prefixError) {
          issues.push({
            kind: 'value',
            bridge: name,
            field: 'topics',
            topic,
            message: `the local prefix "${rule.local_prefix}" is invalid: ${prefixError}`,
          })
        }
      }
      if (rule.remote_prefix) {
        const prefixError = validateTopicPattern(rule.remote_prefix)
        if (prefixError) {
          issues.push({
            kind: 'value',
            bridge: name,
            field: 'topics',
            topic,
            message: `the remote prefix "${rule.remote_prefix}" is invalid: ${prefixError}`,
          })
        }
      }
      const key = `${topic}|${directionOf(rule)}`
      if (seenRules.has(key)) {
        issues.push({
          kind: 'duplicate',
          bridge: name,
          field: 'topics',
          topic,
          message: `the topic rule "${topic}" is declared more than once in this bridge`,
        })
      }
      seenRules.add(key)
    }

    // Dependencies between the TLS options.
    const tls = bridge.tls ?? {}
    if (tls.cert_file && !tls.key_file) {
      issues.push({
        kind: 'dependency',
        bridge: name,
        field: 'tls.key_file',
        message: 'the bridge presents a client certificate but no private key; set the key file or remove the certificate',
      })
    }
    if (tls.key_file && !tls.cert_file) {
      issues.push({
        kind: 'dependency',
        bridge: name,
        field: 'tls.cert_file',
        message: 'the bridge sets a private key but no client certificate; set the certificate file or remove the key',
      })
    }
    if (tls.psk && tls.cert_file) {
      issues.push({
        kind: 'dependency',
        bridge: name,
        field: 'tls.psk',
        message: 'the bridge uses a pre-shared key and a client certificate at the same time; Mosquitto accepts only one',
      })
    }
    if (tls.psk && !tls.identity) {
      issues.push({
        kind: 'dependency',
        bridge: name,
        field: 'tls.identity',
        message: 'the bridge uses a pre-shared key but no identity; Mosquitto refuses the handshake without one',
      })
    }
    if (bridge.notification_topic && bridge.notifications === false) {
      issues.push({
        kind: 'dependency',
        bridge: name,
        field: 'notification_topic',
        message: 'the bridge sets a notification topic but the notifications are disabled',
      })
    }

    // Credentials are advisory: a password the operator cannot see is
    // never a reason to refuse the change.
    if (bridge.username && !bridge.password && !bridge.password_set) {
      warnings.push({
        kind: 'secret',
        bridge: name,
        field: 'password',
        message: 'the bridge sets a user name but no password; the remote broker will most likely refuse the connection',
      })
    }
    if (tls.insecure && !tls.ca_file && !tls.ca_path) {
      warnings.push({
        kind: 'secret',
        bridge: name,
        field: 'tls.insecure',
        message: 'the bridge does not verify the certificate of the remote broker; anyone able to intercept the connection can read the traffic',
      })
    }
  }

  issues.push(...loopIssues(bridges))
  issues.sort(compareIssues)
  warnings.sort(compareIssues)
  return { issues, warnings }
}

// loopIssues finds the configurations in which the broker would feed its
// own traffic back into itself.
function loopIssues(bridges: Bridge[]): BridgeValidationIssue[] {
  const issues: BridgeValidationIssue[] = []

  // Two rules of the same bridge that bounce the same traffic between the
  // local and the remote side.
  for (const bridge of bridges) {
    const rules = bridge.topics ?? []
    for (let i = 0; i < rules.length; i += 1) {
      for (let j = 0; j < rules.length; j += 1) {
        if (i === j) continue
        const outRule = rules[i]
        const inRule = rules[j]
        if (!isOutbound(outRule) || !isInbound(inRule)) continue
        if (!topicsOverlap(remoteTopic(outRule), remoteTopic(inRule))) continue
        if (!topicsOverlap(localTopic(inRule), localTopic(outRule))) continue
        issues.push({
          kind: 'loop',
          bridge: bridge.name,
          field: 'topics',
          topic: inRule.topic,
          message: `the topic rules "${outRule.topic}" and "${inRule.topic}" send the same traffic back and forth between this broker and the remote one`,
        })
      }
    }
  }

  // Two bridges that share the same remote broker and bridge overlapping
  // topics: the traffic the first one sends comes back through the second.
  for (let i = 0; i < bridges.length; i += 1) {
    for (let j = i + 1; j < bridges.length; j += 1) {
      const a = bridges[i]
      const b = bridges[j]
      if (!a.address || !b.address || a.address !== b.address) continue
      for (const ra of a.topics ?? []) {
        for (const rb of b.topics ?? []) {
          if (!topicsOverlap(localTopic(ra), localTopic(rb))) continue
          const outbound = isOutbound(ra) || isOutbound(rb)
          const inbound = isInbound(ra) || isInbound(rb)
          if (!outbound || !inbound) continue
          issues.push({
            kind: 'loop',
            bridge: b.name,
            field: 'address',
            topic: rb.topic,
            message: `the bridge "${a.name}" and the bridge "${b.name}" bridge overlapping topics through the same remote broker ${a.address}; the traffic they send comes back and is forwarded again`,
          })
        }
      }
    }
  }

  return issues
}

function compareIssues(a: BridgeValidationIssue, b: BridgeValidationIssue): number {
  if (a.bridge !== b.bridge) return (a.bridge ?? '').localeCompare(b.bridge ?? '')
  if (a.field !== b.field) return (a.field ?? '').localeCompare(b.field ?? '')
  if (a.topic !== b.topic) return (a.topic ?? '').localeCompare(b.topic ?? '')
  if (a.kind !== b.kind) return a.kind.localeCompare(b.kind)
  return a.message.localeCompare(b.message)
}

// bridgeIssues filters the findings that belong to one bridge.
export function issuesForBridge(issues: BridgeValidationIssue[], name: string): BridgeValidationIssue[] {
  return issues.filter((issue) => issue.bridge === name)
}
