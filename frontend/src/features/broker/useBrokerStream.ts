import { useEffect, useMemo, useRef, useState } from 'react'
import { authenticatedFetch } from '../api/client'

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
  type: 'broker_status' | 'topic_message' | 'broker_log' | 'broker_log_entry' | 'broker_sys_metric'
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
export type BrokerLog = BrokerEvent & { type: 'broker_log' | 'broker_log_entry'; source: 'mcm' | 'mosquitto_log'; severity: 'debug' | 'info' | 'warning' | 'error'; message: string }
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
    observed_at?: string
    target?: { address?: string }
    metrics: {
      last_message_at?: string
      traffic: BrokerTrafficMetrics
    }
  }
}

// Whether the broker status has been read at least once. "failed" means MCM
// could not read it, which is not the same as a disconnected broker.
export type BrokerStatusLoad = 'loading' | 'loaded' | 'failed'

export type BrokerStatusFacts = {
  load: BrokerStatusLoad
  observedAt?: string
  lastMessageAt?: string
  address?: string
}

// Go serializes an unset time as its zero value; treat it as absent.
function meaningfulTime(value?: string) {
  if (!value) return undefined
  const parsed = Date.parse(value)
  return Number.isNaN(parsed) || new Date(parsed).getUTCFullYear() < 2000 ? undefined : value
}

function laterTime(current?: string, next?: string) {
  if (!next) return current
  if (!current) return next
  return Date.parse(next) > Date.parse(current) ? next : current
}

function factsFromStatus(status: StatusResponse, current: BrokerStatusFacts): BrokerStatusFacts {
  return {
    load: 'loaded',
    observedAt: meaningfulTime(status.broker.observed_at),
    lastMessageAt: laterTime(current.lastMessageAt, meaningfulTime(status.broker.metrics.last_message_at)),
    address: status.broker.target?.address || current.address,
  }
}

export const trafficWindowSeconds = 300
export const defaultClientNote = 'Client identity is not included in MQTT application messages observed via wildcard subscriptions. Broker-sourced client metrics and broker log entries are reported separately by the broker diagnostics route.'

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

export function useBrokerStream(token: string, onLogout: () => void) {
  const [brokerStatus, setBrokerStatus] = useState<BrokerConnectionStatus>('disconnected')
  const [streamState, setStreamState] = useState<BrokerStreamState>('connecting')
  const [topics, setTopics] = useState<TopicMessage[]>([])
  const [liveTrafficMetrics, setLiveTrafficMetrics] = useState<BrokerTrafficMetrics>(emptyTrafficMetrics)
  const [logs, setLogs] = useState<BrokerLog[]>([])
  const [statusFacts, setStatusFacts] = useState<BrokerStatusFacts>({ load: 'loading' })
  const liveTrafficEvents = useRef<TopicMessage[]>([])

  useEffect(() => {
    let cancelled = false
    authenticatedFetch('/api/v1/status', { token, onUnauthorized: onLogout })
      .then((response) => (response.ok ? response.json() : Promise.reject(new Error('status request failed'))))
      .then((status: StatusResponse) => {
        if (cancelled) return
        const traffic = withSnapshotAt(status.broker.metrics.traffic ?? emptyTrafficMetrics)
        setBrokerStatus(status.broker.status)
        setStatusFacts((current) => factsFromStatus(status, current))
        setLiveTrafficMetrics(liveTrafficEvents.current.length > 0 ? buildTrafficMetrics(liveTrafficEvents.current, traffic) : traffic)
      })
      .catch(() => {
        if (!cancelled) {
          setStatusFacts((current) => ({ ...current, load: 'failed' }))
          setLiveTrafficMetrics(liveTrafficEvents.current.length > 0 ? buildTrafficMetrics(liveTrafficEvents.current, emptyTrafficMetrics) : emptyTrafficMetrics)
        }
      })

    return () => {
      cancelled = true
    }
  }, [token, onLogout])

  useEffect(() => {
    // Exponential backoff constants
    const BASE_DELAY_MS = 1000
    const MAX_DELAY_MS = 30_000

    let destroyed = false
    let currentSocket: WebSocket | null = null
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null
    let backoffMs = BASE_DELAY_MS
    let isFirstConnection = true

    // Helper to fetch and re-anchor snapshot_at on reconnect (issue #257)
    const refetchSnapshot = () => {
      if (destroyed) return
      authenticatedFetch('/api/v1/status', { token, onUnauthorized: onLogout })
        .then((response) => (response.ok ? response.json() : Promise.reject(new Error('status request failed'))))
        .then((status: StatusResponse) => {
          if (destroyed) return
          const traffic = withSnapshotAt(status.broker.metrics.traffic ?? emptyTrafficMetrics)
          setBrokerStatus(status.broker.status)
          setStatusFacts((current) => factsFromStatus(status, current))
          setLiveTrafficMetrics(liveTrafficEvents.current.length > 0 ? buildTrafficMetrics(liveTrafficEvents.current, traffic) : traffic)
        })
        .catch(() => {
          if (!destroyed) {
            setStatusFacts((current) => ({ ...current, load: 'failed' }))
            setLiveTrafficMetrics(liveTrafficEvents.current.length > 0 ? buildTrafficMetrics(liveTrafficEvents.current, emptyTrafficMetrics) : emptyTrafficMetrics)
          }
        })
    }

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
        // Re-anchor snapshot_at after reconnect (but not on first connection)
        // This fixes issue #257: events arriving after reconnect were being dropped
        // because they were <= the snapshot_at from the initial mount
        if (!isFirstConnection) {
          refetchSnapshot()
        }
        isFirstConnection = false
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
            setStatusFacts((current) => ({ ...current, load: 'loaded', observedAt: meaningfulTime(event.observed_at) ?? current.observedAt }))
          }
          if (event.type === 'topic_message' && event.topic && event.source === 'observed') {
            const topicEvent = event as TopicMessage
            liveTrafficEvents.current = pruneTrafficEvents([topicEvent, ...liveTrafficEvents.current])
            setTopics((current) => [topicEvent, ...current].slice(0, 20))
            setStatusFacts((current) => ({ ...current, lastMessageAt: laterTime(current.lastMessageAt, meaningfulTime(topicEvent.observed_at)) }))
            setLiveTrafficMetrics((current) => addTopicEventToTrafficMetrics(current, topicEvent))
          }
          if ((event.type === 'broker_log' || event.type === 'broker_log_entry') && event.source && event.severity && event.message) {
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
  }, [token, onLogout])

  const uniqueTopicCount = useMemo(() => new Set(topics.map((topic) => topic.topic)).size, [topics])

  return {
    brokerStatus,
    streamState,
    topics,
    latestTopic: topics[0],
    logs,
    uniqueTopicCount,
    liveTrafficMetrics,
    statusFacts,
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
