import type { BrokerLog } from '../broker/useBrokerStream'

// Filter for the live Logs view (issue #357). Plain data so a later change
// can carry it in a link (issue #354).

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
