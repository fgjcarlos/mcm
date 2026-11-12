import { describe, expect, it } from 'vitest'
import type { BrokerLog } from '../broker/useBrokerStream'
import { emptyLogFilter, isLogFilterActive, matchesLogFilter } from './logFilter'

function log(source: BrokerLog['source'], severity: BrokerLog['severity'], message: string): BrokerLog {
  return { type: 'broker_log', source, severity, message, observed_at: '2026-01-01T00:00:00Z' }
}

describe('log filter', () => {
  it('matches everything when empty', () => {
    expect(isLogFilterActive(emptyLogFilter)).toBe(false)
    expect(matchesLogFilter(log('mcm', 'debug', 'x'), emptyLogFilter)).toBe(true)
    expect(matchesLogFilter(log('mosquitto_log', 'error', 'y'), emptyLogFilter)).toBe(true)
  })

  it('filters by source', () => {
    const filter = { ...emptyLogFilter, source: 'mosquitto_log' as const }
    expect(isLogFilterActive(filter)).toBe(true)
    expect(matchesLogFilter(log('mosquitto_log', 'info', 'x'), filter)).toBe(true)
    expect(matchesLogFilter(log('mcm', 'info', 'x'), filter)).toBe(false)
  })

  it('filters by any of the selected severities', () => {
    const filter = { ...emptyLogFilter, severities: ['warning', 'error'] as BrokerLog['severity'][] }
    expect(matchesLogFilter(log('mcm', 'warning', 'x'), filter)).toBe(true)
    expect(matchesLogFilter(log('mcm', 'error', 'x'), filter)).toBe(true)
    expect(matchesLogFilter(log('mcm', 'info', 'x'), filter)).toBe(false)
  })

  it('searches the message without regard to case and ignores surrounding spaces', () => {
    const filter = { ...emptyLogFilter, text: '  Client-7 ' }
    expect(isLogFilterActive(filter)).toBe(true)
    expect(matchesLogFilter(log('mcm', 'info', 'Denied client-7 on a/b'), filter)).toBe(true)
    expect(matchesLogFilter(log('mcm', 'info', 'Denied client-8'), filter)).toBe(false)
    expect(isLogFilterActive({ ...emptyLogFilter, text: '   ' })).toBe(false)
  })

  it('requires every active criterion to match', () => {
    const filter = { source: 'mcm' as const, severities: ['error'] as BrokerLog['severity'][], text: 'disk' }
    expect(matchesLogFilter(log('mcm', 'error', 'disk full'), filter)).toBe(true)
    expect(matchesLogFilter(log('mcm', 'warning', 'disk full'), filter)).toBe(false)
    expect(matchesLogFilter(log('mosquitto_log', 'error', 'disk full'), filter)).toBe(false)
    expect(matchesLogFilter(log('mcm', 'error', 'memory'), filter)).toBe(false)
  })
})
