import type { BrokerLog } from '../broker/useBrokerStream'

// Filter for the live Logs view (issue #357). Plain data so it can travel in
// the URL of the Logs view (issue #354):
// /logs?source=mcm|mosquitto_log&severity=warning,error&q=<text>

export type LogSourceFilter = 'all' | BrokerLog['source']

export type LogFilter = {
  source: LogSourceFilter
  // Empty means every severity.
  severities: BrokerLog['severity'][]
  text: string
}

export const emptyLogFilter: LogFilter = { source: 'all', severities: [], text: '' }

export const logSeverities: BrokerLog['severity'][] = ['debug', 'info', 'warning', 'error']

export function isLogFilterActive(filter: LogFilter) {
  return filter.source !== 'all' || filter.severities.length > 0 || filter.text.trim() !== ''
}

export function matchesLogFilter(log: BrokerLog, filter: LogFilter) {
  if (filter.source !== 'all' && log.source !== filter.source) return false
  if (filter.severities.length > 0 && !filter.severities.includes(log.severity)) return false
  const text = filter.text.trim().toLowerCase()
  return text === '' || log.message.toLowerCase().includes(text)
}

const logSources: BrokerLog['source'][] = ['mcm', 'mosquitto_log']

// Reads the filter from a URL query. Unknown values are ignored, so a stale
// or hand-edited link still opens the view with whatever it can apply.
export function parseLogFilterQuery(search: string): LogFilter {
  const params = new URLSearchParams(search)
  const source = params.get('source') ?? ''
  const requested = params.getAll('severity').flatMap((value) => value.split(','))
  return {
    source: logSources.find((candidate) => candidate === source) ?? 'all',
    severities: logSeverities.filter((severity) => requested.includes(severity)),
    text: params.get('q') ?? '',
  }
}

// Writes only the active criteria; severities in their usual order. Returns
// '' for no filter, otherwise the query including its leading '?'.
export function serializeLogFilterQuery(filter: LogFilter) {
  const params: string[] = []
  if (filter.source !== 'all') params.push(`source=${filter.source}`)
  const severities = logSeverities.filter((severity) => filter.severities.includes(severity))
  if (severities.length > 0) params.push(`severity=${severities.join(',')}`)
  if (filter.text.trim() !== '') params.push(`q=${encodeURIComponent(filter.text)}`)
  return params.length > 0 ? `?${params.join('&')}` : ''
}

export function logsPath(filter: LogFilter) {
  return `/logs${serializeLogFilterQuery(filter)}`
}
