import { useState } from 'react'
import { liveLogWindow, type BrokerLog, type BrokerStreamState, type LiveBrokerLog } from '../broker/useBrokerStream'
import { emptyLogFilter, isLogFilterActive, logSeverities, matchesLogFilter, type LogFilter, type LogSourceFilter } from './logFilter'
import { useLiveFeed } from './useLiveFeed'
import { SeverityBadge } from './SeverityBadge'

// Live Logs view (issue #357): compact rows with an expandable detail,
// filters, a visual pause and the limit of the live window. MCM events stay
// separate from Mosquitto log entries.

const sourceTitles: Record<BrokerLog['source'], string> = {
  mcm: 'MCM events and logs',
  mosquitto_log: 'Broker logs (Mosquitto)',
}

const sources: BrokerLog['source'][] = ['mcm', 'mosquitto_log']

function plural(count: number, one: string, many: string) {
  return `${count} ${count === 1 ? one : many}`
}

function feedStateText(feed: ReturnType<typeof useLiveFeed<LiveBrokerLog>>) {
  if (feed.mode === 'paused') {
    const arrived = `Paused · ${plural(feed.arrivedWhilePaused, 'new event', 'new events')} since pausing`
    return feed.overflowed ? `${arrived}. More arrived than the live window keeps, so the oldest of them will not be shown.` : arrived
  }
  if (feed.mode === 'resumed') {
    return feed.resumedArrivals === 1
      ? 'Back to live · 1 event that arrived while paused is now shown.'
      : `Back to live · ${feed.resumedArrivals} events that arrived while paused are now shown.`
  }
  return 'Live · new events appear at the top.'
}

export function LogsPanel({ logs, streamState }: { logs: LiveBrokerLog[]; streamState: BrokerStreamState }) {
  const feed = useLiveFeed(logs, liveLogWindow)
  const [filter, setFilter] = useState<LogFilter>(emptyLogFilter)
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(new Set())
  const connected = streamState === 'connected'

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
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div>
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Log feed · {streamState}</p>
        <p className="mt-2 max-w-2xl text-sm text-slate-300">Live window: the newest {liveLogWindow} events received while this page is open. Older events are dropped, so there is no history to page through.</p>
      </div>
      <button type="button" onClick={feed.mode === 'paused' ? feed.resume : feed.pause} className="rounded-xl border border-cyan-300/30 px-4 py-2 text-sm font-semibold text-cyan-100 hover:bg-cyan-400/10">
        {feed.mode === 'paused' ? 'Resume live' : 'Pause'}
      </button>
    </div>

    {connected ? null : <p className="mt-4 rounded-xl border border-amber-300/30 bg-amber-400/10 p-3 text-sm text-amber-100">Not connected to the live event stream ({streamState}). New events will not arrive until it reconnects.</p>}

    <p role="status" aria-label="Live feed state" className={`mt-4 text-sm ${feed.mode === 'paused' ? 'text-amber-200' : 'text-emerald-200'}`}>{feedStateText(feed)}</p>

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
          </ol> : <p className="mt-3 text-sm text-slate-300">{emptyText(received.length > 0, streamState)}</p>}
        </section>
      })}
    </div>
  </section>
}

function emptyText(hasEvents: boolean, streamState: BrokerStreamState) {
  if (hasEvents) return 'No events match the filters.'
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
