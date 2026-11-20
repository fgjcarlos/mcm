import { useEffect, useState } from 'react'
import { liveLogWindow, type BrokerLog, type BrokerStreamState, type LiveBrokerLog } from '../broker/useBrokerStream'
import { emptyLogFilter, isLogFilterActive, logSeverities, logSources, matchesLogFilter, parseLogFilterQuery, serializeLogFilterQuery, type LogFilter, type LogSourceFilter } from './logFilter'
import { useLiveFeed } from './useLiveFeed'
import { LiveFeedChrome } from './LiveFeedChrome'
import { emptyFeedText } from './feedText'
import { SeverityBadge } from './SeverityBadge'
import { useClientPage } from '../lists/useClientPage'
import { ClientPager } from '../lists/ClientPager'
import { fieldLabel, input, leverToggle, select, textLink } from '../ui/styles'

// Live Logs view (issue #357): compact rows with an expandable detail,
// filters, a visual pause and the limit of the live window. MCM events stay
// separate from Mosquitto log entries. The filter lives in the URL query
// (issue #354): the view opens with the filter of its link and keeps the URL
// in step without adding history entries.

// title: heading of the source region and its name in the event detail.
// option: the shorter name in the Source select.
const sourceLabels: Record<BrokerLog['source'], { title: string; option: string }> = {
  mcm: { title: 'MCM events and logs', option: 'MCM events' },
  mosquitto_log: { title: 'Broker logs (Mosquitto)', option: 'Broker logs (Mosquitto)' },
}

const eventNoun = { one: 'event', many: 'events' }
// One pager per source region, so each nav needs its own name.
const lineNouns: Record<BrokerLog['source'], { one: string; many: string }> = {
  mcm: { one: 'MCM log line', many: 'MCM log lines' },
  mosquitto_log: { one: 'Mosquitto log line', many: 'Mosquitto log lines' },
}

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

  return <section className="mt-8">
    <LiveFeedChrome feed={feed} streamState={streamState} windowSize={liveLogWindow} noun={eventNoun} label="Log feed" />

    <div className="mt-6 flex flex-wrap items-end gap-4 border-t border-track pt-4">
      <label className="flex flex-col gap-2"><span className={fieldLabel}>Source</span>
        <select aria-label="Source" value={filter.source} onChange={(event) => setFilter((current) => ({ ...current, source: event.target.value as LogSourceFilter }))} className={select}>
          <option value="all">All sources</option>
          {logSources.map((source) => <option key={source} value={source}>{sourceLabels[source].option}</option>)}
        </select>
      </label>
      <div role="group" aria-label="Severity" className="flex flex-col gap-2">
        <span className={fieldLabel}>Severity</span>
        <span className="flex flex-wrap gap-2">
          {logSeverities.map((severity) => <button key={severity} type="button" aria-pressed={filter.severities.includes(severity)} onClick={() => toggleSeverity(severity)} className={leverToggle}>{severity}</button>)}
        </span>
      </div>
      <label className="flex flex-col gap-2"><span className={fieldLabel}>Search messages</span>
        <input type="search" aria-label="Search messages" value={filter.text} onChange={(event) => setFilter((current) => ({ ...current, text: event.target.value }))} className={input} />
      </label>
      {isLogFilterActive(filter) ? <button type="button" onClick={() => setFilter(emptyLogFilter)} className={`h-9 text-sm ${textLink}`}>Clear filters</button> : null}
    </div>

    <div className={`mt-6 grid gap-x-8 gap-y-6 ${filter.source === 'all' ? 'lg:grid-cols-2' : ''}`}>
      {logSources.filter((source) => filter.source === 'all' || filter.source === source).map((source) => {
        const received = feed.visible.filter((log) => log.source === source)
        const shown = received.filter((log) => matchesLogFilter(log, filter))
        return <section key={source} aria-label={sourceLabels[source].title} className="min-w-0">
          <h4 className="border-b border-track-strong pb-2 text-sm font-semibold text-enamel-bright">{sourceLabels[source].title}</h4>
          {shown.length > 0 ? <PagedLogList logs={shown} noun={lineNouns[source]} expanded={expanded} onToggle={toggleExpanded} /> : <p className="mt-3 text-sm text-enamel-soft">{emptyFeedText({
            noun: eventNoun,
            filterLabel: 'filters',
            receivedAny: received.length > 0,
            // Paused on an empty region while this source keeps receiving.
            pausedWhileArriving: feed.mode === 'paused' && logs.some((log) => log.source === source),
            streamState,
          })}</p>}
        </section>
      })}
    </div>
  </section>
}

function PagedLogList({ logs, noun, expanded, onToggle }: { logs: LiveBrokerLog[]; noun: { one: string; many: string }; expanded: ReadonlySet<number>; onToggle: (id: number) => void }) {
  const pager = useClientPage(logs, { pageSize: 25 })
  return <>
    <div className="not-empty:my-3"><ClientPager pager={pager} noun={noun} /></div>
    <ol className="divide-y divide-track">
      {pager.rows.map(({ item: log }) => <LogRow key={log.id} log={log} expanded={expanded.has(log.id)} onToggle={() => onToggle(log.id)} />)}
    </ol>
  </>
}

function LogRow({ log, expanded, onToggle }: { log: LiveBrokerLog; expanded: boolean; onToggle: () => void }) {
  const detailId = `log-detail-${log.id}`
  return <li>
    <button type="button" aria-expanded={expanded} aria-controls={detailId} onClick={onToggle} className="flex w-full items-center gap-3 px-2 py-2 text-left transition-colors duration-150 hover:bg-frame-raised aria-expanded:bg-frame-raised">
      <SeverityBadge severity={log.severity} />
      <time dateTime={log.observed_at} className="shrink-0 font-mono text-xs tabular-nums text-enamel-muted">{new Date(log.observed_at).toLocaleTimeString()}</time>
      <span className="min-w-0 flex-1 truncate text-sm text-enamel">{log.message}</span>
    </button>
    {expanded ? <div id={detailId} role="region" aria-label="Event detail" className="bg-frame-raised px-2 pb-3 pt-1 text-sm">
      <dl className="grid gap-x-4 gap-y-1 text-xs text-enamel sm:grid-cols-[auto_1fr]">
        <dt className="text-enamel-muted">Source</dt><dd>{sourceLabels[log.source].title}</dd>
        <dt className="text-enamel-muted">Severity</dt><dd className="font-mono">{log.severity}</dd>
        <dt className="text-enamel-muted">Received</dt><dd className="font-mono">{log.observed_at} ({new Date(log.observed_at).toLocaleString()})</dd>
      </dl>
      <p className="mt-3 whitespace-pre-wrap break-words font-mono text-xs leading-5 text-enamel-bright">{log.message}</p>
    </div> : null}
  </li>
}
