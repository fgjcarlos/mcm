import type { BrokerRatePoint, BrokerTrafficItem, BrokerTrafficMetrics, TopicMessage } from '../types'

export const trafficWindowSeconds = 300
export const defaultClientNote =
  'Client identity is not included in MQTT application messages observed via wildcard subscriptions. Enable broker-side client metrics or log ingestion to populate this widget in a future release.'

export const emptyTrafficMetrics: BrokerTrafficMetrics = {
  window_seconds: trafficWindowSeconds,
  message_count: 0,
  message_rate_per_minute: 0,
  rate_points: [],
  top_topics: [],
  top_clients: [],
  top_clients_available: false,
  top_clients_note: defaultClientNote,
  persistence: 'Waiting for broker metrics.',
}

export function pruneTrafficEvents(events: TopicMessage[]): TopicMessage[] {
  const cutoff = Date.now() - trafficWindowSeconds * 1000
  return events.filter((event) => new Date(event.observed_at).getTime() >= cutoff).slice(0, 5000)
}

export function buildTrafficMetrics(events: TopicMessage[], base: BrokerTrafficMetrics): BrokerTrafficMetrics {
  const pruned = pruneTrafficEvents(events)
  const topicCounts = new Map<string, number>()
  const bucketCounts = new Map<number, number>()
  let messageCount = base.message_count

  base.top_topics.forEach((item: BrokerTrafficItem) => topicCounts.set(item.name, item.count))
  base.rate_points.forEach((point: BrokerRatePoint) => bucketCounts.set(new Date(point.timestamp).getTime(), point.count))

  pruned.forEach((event) => {
    messageCount += 1
    topicCounts.set(event.topic, (topicCounts.get(event.topic) ?? 0) + 1)
    const observedAt = new Date(event.observed_at)
    observedAt.setSeconds(0, 0)
    bucketCounts.set(observedAt.getTime(), (bucketCounts.get(observedAt.getTime()) ?? 0) + 1)
  })

  const now = new Date()
  now.setSeconds(0, 0)
  const ratePoints: BrokerRatePoint[] = []
  for (let offset = Math.floor(trafficWindowSeconds / 60); offset >= 0; offset -= 1) {
    const timestamp = new Date(now.getTime() - offset * 60_000)
    ratePoints.push({ timestamp: timestamp.toISOString(), count: bucketCounts.get(timestamp.getTime()) ?? 0 })
  }

  const topTopics = [...topicCounts.entries()]
    .map(([name, count]) => ({ name, count, percentage: messageCount === 0 ? 0 : (count * 100) / messageCount }))
    .sort((left, right) => right.count - left.count || left.name.localeCompare(right.name))
    .slice(0, 5)

  return {
    ...base,
    window_seconds: trafficWindowSeconds,
    message_count: messageCount,
    message_rate_per_minute: messageCount / (trafficWindowSeconds / 60),
    rate_points: ratePoints,
    top_topics: topTopics,
    top_clients_available: false,
    top_clients_note: base.top_clients_note || defaultClientNote,
  }
}
