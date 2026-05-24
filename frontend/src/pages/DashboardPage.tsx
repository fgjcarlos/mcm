import type { BrokerTrafficMetrics, TopicMessage } from '../types'
import { RateChart } from '../components/RateChart'
import { TrafficBars } from '../components/TrafficBars'
import { defaultClientNote } from '../lib/traffic-metrics'
import { SparkplugDetails } from './_shared'
import { getStatus } from '../lib/api'
import { usePolling } from '../hooks/usePolling'

function HotspotCard({ title, empty, items }: { title: string; empty: string; items: { name: string; count: number; percentage: number }[] }) {
  return (
    <article className="rounded-[1.75rem] border border-white/10 bg-slate-900/70 p-6">
      <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">{title}</p>
      {items.length === 0 ? <p className="mt-4 text-sm text-slate-300">{empty}</p> : <TrafficBars items={items} />}
    </article>
  )
}

type Props = {
  metrics: BrokerTrafficMetrics
  topics: TopicMessage[]
  latestTopic?: TopicMessage
}

// Poll the status endpoint every 30 s so the dashboard reflects fresh server-side
// metrics even when WebSocket traffic is sparse. Stale data is kept visible on error.
const STATUS_INTERVAL_MS = 30_000

export function DashboardPage({ metrics: wsMetrics, topics, latestTopic }: Props) {
  const { data: statusData } = usePolling(getStatus, STATUS_INTERVAL_MS)

  // Prefer freshly-polled server metrics when available; fall back to live WebSocket
  // metrics that are built up from the stream.
  const metrics = statusData?.broker.metrics.traffic ?? wsMetrics

  return (
    <section className="mt-8 space-y-4">
      <div className="grid gap-4 lg:grid-cols-3">
        <article className="rounded-[1.75rem] border border-cyan-300/20 bg-cyan-400/10 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-100/80">Message rate</p>
          <div className="mt-3 flex items-end gap-2">
            <span className="font-mono text-4xl font-semibold text-white">{metrics.message_rate_per_minute.toFixed(1)}</span>
            <span className="pb-1 text-sm text-cyan-100">msg/min</span>
          </div>
          <p className="mt-2 text-sm text-slate-300">{metrics.message_count} messages in the last {Math.round(metrics.window_seconds / 60)} minutes.</p>
          <RateChart points={metrics.rate_points} />
        </article>

        <HotspotCard title="Top topics" empty="No topic activity in the recent window." items={metrics.top_topics} />

        <article className="rounded-[1.75rem] border border-white/10 bg-slate-900/70 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Top clients</p>
          {metrics.top_clients_available && metrics.top_clients.length > 0 ? (
            <TrafficBars items={metrics.top_clients} />
          ) : (
            <div className="mt-4 rounded-2xl border border-dashed border-amber-300/25 bg-amber-400/10 p-4 text-sm leading-6 text-amber-50">
              <p className="font-semibold">Not available from subscribed messages</p>
              <p className="mt-2 text-amber-50/80">{metrics.top_clients_note || defaultClientNote}</p>
            </div>
          )}
        </article>
      </div>

      <div className="grid gap-4 lg:grid-cols-[0.85fr_1.15fr]">
        <article className="rounded-[1.75rem] border border-white/10 bg-white/[0.04] p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Metric source</p>
          <p className="mt-3 text-sm leading-6 text-slate-300">{metrics.persistence || 'Recent traffic is held in memory for this browser session.'}</p>
        </article>
        <article className="rounded-[1.75rem] border border-white/10 bg-white/[0.04] p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Latest activity</p>
          <p className="mt-3 text-sm text-slate-300">{latestTopic ? latestTopic.topic : topics.length === 0 ? 'Waiting for topic traffic.' : `${topics.length} recent messages loaded.`}</p>
          {latestTopic?.sparkplug ? <SparkplugDetails metadata={latestTopic.sparkplug} compact /> : null}
        </article>
      </div>
    </section>
  )
}
