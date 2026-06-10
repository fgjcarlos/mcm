import { useEffect, useMemo, useRef, useState } from 'react'

export type SparkplugMetadata = {
  namespace: string
  group_id: string
  message_type: string
  edge_node_id: string
  device_id?: string
}

export type SparkplugDecodedMetric = {
  name: string
  alias?: number
  datatype: string
  value: unknown
  timestamp?: number
  is_null?: boolean
}

export type SparkplugDecodedPayload = {
  timestamp: number
  seq: number
  metrics: SparkplugDecodedMetric[]
  truncated: boolean
}

export type PayloadInspection = {
  detected_type: 'json_object' | 'json_array' | 'json_scalar' | 'text' | 'binary' | string
  byte_length: number
  truncated: boolean
  json_valid: boolean
  json_top_level_keys?: string[]
  json_element_count?: number
  json_scalar_summary?: string
}

export type SchemaValidation = {
  schema_id: number
  schema_name: string
  topic_filter: string
  valid: boolean
  errors?: string[]
}

export type BrokerEvent = {
  type: 'broker_status' | 'topic_message' | 'broker_log'
  status?: 'connected' | 'disconnected'
  topic?: string
  payload_preview?: string
  payload_format?: 'json' | 'text' | 'binary'
  payload_bytes?: number
  truncated?: boolean
  payload_inspection?: PayloadInspection
  schema_validation?: SchemaValidation
  sparkplug?: SparkplugMetadata
  sparkplug_metrics?: SparkplugDecodedPayload
  source?: string
  severity?: 'debug' | 'info' | 'warning' | 'error'
  message?: string
  observed_at: string
}

export type TopicMessage = BrokerEvent & { type: 'topic_message'; topic: string }
export type BrokerLog = BrokerEvent & { type: 'broker_log'; source: string; severity: 'debug' | 'info' | 'warning' | 'error'; message: string }
export type BrokerConnectionStatus = 'connected' | 'disconnected'
export type BrokerStreamState = 'connecting' | 'connected' | 'reconnecting' | 'disconnected'

export type BrokerTrafficItem = {
  name: string
  count: number
  percentage: number
}

export type BrokerRatePoint = {
  timestamp: string
  count: number
}

export type BrokerTrafficMetrics = {
  snapshot_at?: string
  window_seconds: number
  message_count: number
  message_rate_per_minute: number
  rate_points: BrokerRatePoint[]
  top_topics: BrokerTrafficItem[]
  top_clients: BrokerTrafficItem[]
  top_clients_available: boolean
  top_clients_note: string
  persistence: string
}

type StatusResponse = {
  broker: {
    status: BrokerConnectionStatus
    metrics: {
      traffic: BrokerTrafficMetrics
    }
  }
}

export const trafficWindowSeconds = 300
export const defaultClientNote = 'Client identity is not included in MQTT application messages observed via wildcard subscriptions. Enable broker-side client metrics or log ingestion to populate this widget in a future release.'

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

export function useBrokerStream(token: string) {
  const [brokerStatus, setBrokerStatus] = useState<BrokerConnectionStatus>('disconnected')
  const [streamState, setStreamState] = useState<BrokerStreamState>('connecting')
  const [topics, setTopics] = useState<TopicMessage[]>([])
  const [liveTrafficMetrics, setLiveTrafficMetrics] = useState<BrokerTrafficMetrics>(emptyTrafficMetrics)
  const [logs, setLogs] = useState<BrokerLog[]>([])
  const liveTrafficEvents = useRef<TopicMessage[]>([])

  useEffect(() => {
    let cancelled = false
    fetch('/api/v1/status')
      .then((response) => (response.ok ? response.json() : Promise.reject(new Error('status request failed'))))
      .then((status: StatusResponse) => {
        if (cancelled) return
        const traffic = withSnapshotAt(status.broker.metrics.traffic ?? emptyTrafficMetrics)
        setBrokerStatus(status.broker.status)
        setLiveTrafficMetrics(liveTrafficEvents.current.length > 0 ? buildTrafficMetrics(liveTrafficEvents.current, traffic) : traffic)
      })
      .catch(() => {
        if (!cancelled) {
          setLiveTrafficMetrics(liveTrafficEvents.current.length > 0 ? buildTrafficMetrics(liveTrafficEvents.current, emptyTrafficMetrics) : emptyTrafficMetrics)
        }
      })

    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    // Exponential backoff constants
    const BASE_DELAY_MS = 1000
    const MAX_DELAY_MS = 30_000

    let destroyed = false
    let currentSocket: WebSocket | null = null
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null
    let backoffMs = BASE_DELAY_MS

    function connect() {
      if (destroyed) return

      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      const socket = new WebSocket(
        `${protocol}//${window.location.host}/api/v1/broker/events`,
        ['mcm.v1', `Bearer.${token}`],
      )
      currentSocket = socket

      socket.addEventListener('open', () => {
        if (destroyed || socket !== currentSocket) return
        // Reset backoff on a successful connection
        backoffMs = BASE_DELAY_MS
        setStreamState('connected')
      })

      function scheduleReconnect() {
        if (destroyed || reconnectTimer !== null) return
        setStreamState('reconnecting')
        // Apply jitter: ±10% of the current delay
        const jitter = (Math.random() * 0.2 - 0.1) * backoffMs
        const delay = Math.min(backoffMs + jitter, MAX_DELAY_MS)
        reconnectTimer = setTimeout(() => {
          reconnectTimer = null
          connect()
        }, delay)
        // Double the backoff for the next failure, capped at MAX_DELAY_MS
        backoffMs = Math.min(backoffMs * 2, MAX_DELAY_MS)
      }

      socket.addEventListener('close', () => {
        if (destroyed || socket !== currentSocket) return
        scheduleReconnect()
      })

      socket.addEventListener('error', () => {
        if (destroyed || socket !== currentSocket) return
        // 'error' is followed by 'close' in the browser WebSocket spec.
        // Guard with reconnectTimer !== null check in scheduleReconnect so
        // the close handler does not schedule a second timer.
        scheduleReconnect()
      })

      socket.addEventListener('message', (message) => {
        if (destroyed || socket !== currentSocket) return
        try {
          const event = JSON.parse((message as MessageEvent).data) as BrokerEvent
          if (event.type === 'broker_status' && event.status) {
            setBrokerStatus(event.status)
          }
          if (event.type === 'topic_message' && event.topic) {
            const topicEvent = event as TopicMessage
            liveTrafficEvents.current = pruneTrafficEvents([topicEvent, ...liveTrafficEvents.current])
            setTopics((current) => [topicEvent, ...current].slice(0, 20))
            setLiveTrafficMetrics((current) => addTopicEventToTrafficMetrics(current, topicEvent))
          }
          if (event.type === 'broker_log' && event.source && event.severity && event.message) {
            setLogs((current) => [event as BrokerLog, ...current].slice(0, 100))
          }
        } catch {
          // Malformed frame — log and skip. Do NOT change stream state;
          // the WebSocket connection is still alive.
          console.warn('[useBrokerStream] Could not parse broker event frame; skipping.')
        }
      })
    }

    connect()

    return () => {
      destroyed = true
      if (reconnectTimer !== null) {
        clearTimeout(reconnectTimer)
        reconnectTimer = null
      }
      currentSocket?.close()
      currentSocket = null
    }
  }, [token])

  const uniqueTopicCount = useMemo(() => new Set(topics.map((topic) => topic.topic)).size, [topics])

  return {
    brokerStatus,
    streamState,
    topics,
    latestTopic: topics[0],
    logs,
    uniqueTopicCount,
    liveTrafficMetrics,
  }
}

export function pruneTrafficEvents(events: TopicMessage[]) {
  const cutoff = Date.now() - trafficWindowSeconds * 1000
  return events.filter((event) => new Date(event.observed_at).getTime() >= cutoff).slice(0, 5000)
}

export function buildTrafficMetrics(events: TopicMessage[], base: BrokerTrafficMetrics): BrokerTrafficMetrics {
  const pruned = pruneTrafficEvents(events)
  const topicCounts = new Map<string, number>()
  const bucketCounts = new Map<number, number>()
  let messageCount = base.message_count

  base.top_topics.forEach((item) => topicCounts.set(item.name, item.count))
  base.rate_points.forEach((point) => bucketCounts.set(new Date(point.timestamp).getTime(), point.count))

  pruned.forEach((event) => {
    if (!isAfterSnapshot(event, base)) return
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

function withSnapshotAt(metrics: BrokerTrafficMetrics): BrokerTrafficMetrics {
  return metrics.snapshot_at ? metrics : { ...metrics, snapshot_at: new Date().toISOString() }
}

export function addTopicEventToTrafficMetrics(base: BrokerTrafficMetrics, event: TopicMessage): BrokerTrafficMetrics {
  if (!isAfterSnapshot(event, base)) return base

  const observedAt = new Date(event.observed_at)
  const observedMs = observedAt.getTime()
  if (!Number.isFinite(observedMs)) return base

  const messageCount = base.message_count + 1
  const topicCounts = new Map<string, number>()
  base.top_topics.forEach((item) => topicCounts.set(item.name, item.count))
  topicCounts.set(event.topic, (topicCounts.get(event.topic) ?? 0) + 1)

  observedAt.setSeconds(0, 0)
  const bucketCounts = new Map<number, number>()
  base.rate_points.forEach((point) => bucketCounts.set(new Date(point.timestamp).getTime(), point.count))
  bucketCounts.set(observedAt.getTime(), (bucketCounts.get(observedAt.getTime()) ?? 0) + 1)

  const ratePoints: BrokerRatePoint[] = []
  for (let offset = Math.floor(trafficWindowSeconds / 60); offset >= 0; offset -= 1) {
    const timestamp = new Date(observedAt.getTime() - offset * 60_000)
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

function isAfterSnapshot(event: TopicMessage, base: BrokerTrafficMetrics) {
  const snapshotMs = base.snapshot_at ? new Date(base.snapshot_at).getTime() : Number.NEGATIVE_INFINITY
  const observedMs = new Date(event.observed_at).getTime()
  return Number.isFinite(observedMs) && (!Number.isFinite(snapshotMs) || observedMs > snapshotMs)
}
