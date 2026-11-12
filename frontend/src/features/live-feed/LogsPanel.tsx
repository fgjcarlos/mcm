import { useEffect, useState } from 'react'
import { liveLogWindow, type BrokerLog, type BrokerStreamState, type LiveBrokerLog } from '../broker/useBrokerStream'
import { emptyLogFilter, isLogFilterActive, logSeverities, matchesLogFilter, parseLogFilterQuery, serializeLogFilterQuery, type LogFilter, type LogSourceFilter } from './logFilter'
import { useLiveFeed } from './useLiveFeed'
import { LiveFeedChrome } from './LiveFeedChrome'
import { SeverityBadge } from './SeverityBadge'

// Live Logs view (issue #357): compact rows with an expandable detail,
// filters, a visual pause and the limit of the live window. MCM events stay
// separate from Mosquitto log entries. The filter lives in the URL query
// (issue #354): the view opens with the filter of its link and keeps the URL
// in step without adding history entries.

const sourceTitles: Record<BrokerLog['source'], string> = {
  mcm: 'MCM events and logs',
  mosquitto_log: 'Broker logs (Mosquitto)',
}

const sources: BrokerLog['source'][] = ['mcm', 'mosquitto_log']

const eventNoun = { one: 'event', many: 'events' }

export function LogsPanel({ logs, streamState }: { logs: LiveBrokerLog[]; streamState: BrokerStreamState }) {
  const feed = useLiveFeed(logs, liveLogWindow)
  const [filter, setFilter] = useState<LogFilter>(() => parseLogFilterQuery(window.location.search))
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(new Set())

  useEffect(() => {
    const search = serializeLogFilterQuery(filter)
    if (window.location.search === search) return
    window.history.replaceState(window.history.state, '', `${window.location.pathname}${search}${window.location.hash}`)
  }, [filter])

  const toggleSeverity = (severity: BrokerLog['severity']) => setFilter((current) => ({
    ...current,
    severities: current.severities.includes(severity) ? current.severities.filter((item) => item !== severity) : [...current.severities, severity],
  }))
  const toggleExpanded = (id: number) => setExpanded((current) => {
    const next = new Set(current)
    if (!next.delete(id)) next.add(id)
    return next
  })

  return <section className="mt-8 rounded-[1.75rem] border border-white/10 bg-slate-900/70 p-6">
    <LiveFeedChrome feed={feed} streamState={streamState} windowSize={liveLogWindow} noun={eventNoun} label="Log feed" />

    <div className="mt-4 flex flex-wrap items-end gap-4">
      <label className="text-sm text-slate-300">Source{' '}
        <select aria-label="Source" value={filter.source} onChange={(event) => setFilter((current) => ({ ...current, source: event.target.value as LogSourceFilter }))} className="ml-2 rounded-lg bg-slate-900 p-2">
          <option value="all">All sources</option>
          <option value="mcm">MCM events</option>
          <option value="mosquitto_log">Broker logs (Mosquitto)</option>
        </select>
      </label>
      <div role="group" aria-label="Severity" className="flex flex-wrap items-center gap-2 text-sm text-slate-300">
        <span>Severity</span>
        {logSeverities.map((severity) => <button key={severity} type="button" aria-pressed={filter.severities.includes(severity)} onClick={() => toggleSeverity(severity)} className={`rounded-full border px-3 py-1 text-xs font-semibold uppercase tracking-[0.12em] ${filter.severities.includes(severity) ? 'border-cyan-300 bg-cyan-400/20 text-cyan-50' : 'border-white/15 text-slate-300'}`}>{severity}</button>)}
      </div>
      <label className="text-sm text-slate-300">Search messages{' '}
        <input type="search" aria-label="Search messages" value={filter.text} onChange={(event) => setFilter((current) => ({ ...current, text: event.target.value }))} className="ml-2 rounded-lg bg-slate-900 p-2" />
      </label>
      {isLogFilterActive(filter) ? <button type="button" onClick={() => setFilter(emptyLogFilter)} className="text-sm font-semibold text-cyan-200 underline">Clear filters</button> : null}
    </div>

    <div className={`mt-5 grid gap-5 ${filter.source === 'all' ? 'lg:grid-cols-2' : ''}`}>
      {sources.filter((source) => filter.source === 'all' || filter.source === source).map((source) => {
        const received = feed.visible.filter((log) => log.source === source)
        const shown = received.filter((log) => matchesLogFilter(log, filter))
        return <section key={source} aria-label={sourceTitles[source]}>
          <h3 className="text-sm font-semibold text-cyan-100">{sourceTitles[source]}</h3>
          {shown.length > 0 ? <ol className="mt-3 divide-y divide-white/5 rounded-2xl border border-white/10 bg-white/[0.03]">
            {shown.map((log) => <LogRow key={log.id} log={log} expanded={expanded.has(log.id)} onToggle={() => toggleExpanded(log.id)} />)}
          </ol> : <p className="mt-3 text-sm text-slate-300">{emptyText(received.length > 0, streamState, feed.mode === 'paused' && feed.arrivedWhilePaused > 0)}</p>}
        </section>
      })}
    </div>
  </section>
}

// pausedWithArrivals: the view is paused on an empty window while new events
// keep arriving, which is neither idle nor disconnected.
function emptyText(hasEvents: boolean, streamState: BrokerStreamState, pausedWithArrivals: boolean) {
  if (hasEvents) return 'No events match the filters.'
  if (pausedWithArrivals) return 'Paused before any events arrived. Resume live to see them.'
  if (streamState !== 'connected') return `No events: the live stream is ${streamState}.`
  return 'Connected · no events received yet.'
}

function LogRow({ log, expanded, onToggle }: { log: LiveBrokerLog; expanded: boolean; onToggle: () => void }) {
  const detailId = `log-detail-${log.id}`
  return <li>
    <button type="button" aria-expanded={expanded} aria-controls={detailId} onClick={onToggle} className="flex w-full items-center gap-3 px-3 py-2 text-left hover:bg-white/[0.04] focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300">
      <SeverityBadge severity={log.severity} />
      <time dateTime={log.observed_at} className="shrink-0 font-mono text-xs text-slate-400">{new Date(log.observed_at).toLocaleTimeString()}</time>
      <span className="min-w-0 flex-1 truncate text-sm text-slate-100">{log.message}</span>
    </button>
    {expanded ? <div id={detailId} role="region" aria-label="Event detail" className="border-t border-white/5 px-3 py-3 text-sm">
      <dl className="grid gap-x-4 gap-y-1 text-xs text-slate-300 sm:grid-cols-[auto_1fr]">
        <dt className="text-slate-400">Source</dt><dd>{sourceTitles[log.source]}</dd>
        <dt className="text-slate-400">Severity</dt><dd>{log.severity}</dd>
        <dt className="text-slate-400">Received</dt><dd className="font-mono">{log.observed_at} ({new Date(log.observed_at).toLocaleString()})</dd>
      </dl>
      <p className="mt-3 whitespace-pre-wrap break-words leading-6 text-slate-100">{log.message}</p>
    </div> : null}
  </li>
}
