import { describe, expect, it } from 'vitest'
import type { BrokerLog } from '../broker/useBrokerStream'
import { emptyLogFilter, isLogFilterActive, logsPath, matchesLogFilter, parseLogFilterQuery, serializeLogFilterQuery } from './logFilter'

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

describe('log filter in the URL (issue #354)', () => {
  it('reads source, severities and text from the query', () => {
    expect(parseLogFilterQuery('?source=mcm&severity=warning,error&q=disk%20full')).toEqual({ source: 'mcm', severities: ['warning', 'error'], text: 'disk full' })
    expect(parseLogFilterQuery('source=mosquitto_log')).toEqual({ ...emptyLogFilter, source: 'mosquitto_log' })
    expect(parseLogFilterQuery('?severity=warning%2Cerror')).toEqual({ ...emptyLogFilter, severities: ['warning', 'error'] })
  })

  it('reads an empty or missing query as no filter', () => {
    expect(parseLogFilterQuery('')).toEqual(emptyLogFilter)
    expect(parseLogFilterQuery('?')).toEqual(emptyLogFilter)
    expect(parseLogFilterQuery('?source=&severity=&q=')).toEqual(emptyLogFilter)
  })

  it('ignores unknown values and repeated severities', () => {
    expect(parseLogFilterQuery('?source=kernel&severity=fatal,error,,error&other=1')).toEqual({ ...emptyLogFilter, severities: ['error'] })
    expect(parseLogFilterQuery('?source=all')).toEqual(emptyLogFilter)
  })

  it('writes only the active criteria, with severities in a stable order', () => {
    expect(serializeLogFilterQuery(emptyLogFilter)).toBe('')
    expect(serializeLogFilterQuery({ ...emptyLogFilter, text: '   ' })).toBe('')
    expect(serializeLogFilterQuery({ source: 'mcm', severities: ['error', 'warning'], text: '' })).toBe('?source=mcm&severity=warning,error')
    expect(serializeLogFilterQuery({ source: 'all', severities: [], text: 'a&b c' })).toBe('?q=a%26b%20c')
  })

  it('round-trips a filter through the query', () => {
    const filter = { source: 'mosquitto_log' as const, severities: ['debug', 'error'] as BrokerLog['severity'][], text: 'client-7 / a,b' }
    expect(parseLogFilterQuery(serializeLogFilterQuery(filter))).toEqual(filter)
  })

  it('builds the Logs link for a filter', () => {
    expect(logsPath(emptyLogFilter)).toBe('/logs')
    expect(logsPath({ source: 'mcm', severities: ['warning', 'error'], text: '' })).toBe('/logs?source=mcm&severity=warning,error')
  })
})
