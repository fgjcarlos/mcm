import { createContext, useContext, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import type { BrokerEvent, BrokerLog, BrokerTrafficMetrics, TopicMessage } from '../types'
import { getStatus } from '../lib/api'
import { buildTrafficMetrics, emptyTrafficMetrics, pruneTrafficEvents } from '../lib/traffic-metrics'

type StreamState = 'connecting' | 'connected' | 'disconnected'

type BrokerContextValue = {
  brokerStatus: 'connected' | 'disconnected'
  streamState: StreamState
  topics: TopicMessage[]
  logs: BrokerLog[]
  liveTrafficMetrics: BrokerTrafficMetrics
  latestTopic: TopicMessage | undefined
  uniqueTopicCount: number
}

const BrokerContext = createContext<BrokerContextValue | null>(null)

// Exponential backoff constants
const BASE_DELAY_MS = 1_000
const MAX_RETRIES = 5
const JITTER_FACTOR = 0.2 // ±20%

/** Returns a delay in ms for the given attempt index (0-based) with ±20% jitter. */
function backoffDelay(attempt: number): number {
  const base = BASE_DELAY_MS * Math.pow(2, attempt)
  const jitter = base * JITTER_FACTOR * (Math.random() * 2 - 1) // range: [-20%, +20%]
  return Math.round(base + jitter)
}

export function BrokerProvider({ token, children }: { token: string; children: ReactNode }) {
  const [brokerStatus, setBrokerStatus] = useState<'connected' | 'disconnected'>('disconnected')
  const [streamState, setStreamState] = useState<StreamState>('connecting')
  const [topics, setTopics] = useState<TopicMessage[]>([])
  const [trafficEvents, setTrafficEvents] = useState<TopicMessage[]>([])
  const [trafficMetrics, setTrafficMetrics] = useState<BrokerTrafficMetrics>(emptyTrafficMetrics)
  const [logs, setLogs] = useState<BrokerLog[]>([])

  // Retry counter persists across reconnection attempts within one token lifecycle.
  const retryCountRef = useRef(0)
  // Whether the component is still mounted — prevents state updates after unmount.
  const mountedRef = useRef(true)
  // Keep the token in a ref so the reconnect closure always reads the latest value.
  const tokenRef = useRef(token)
  useEffect(() => {
    tokenRef.current = token
  })

  // One-shot status fetch on mount to populate initial broker/metrics state.
  useEffect(() => {
    let cancelled = false
    getStatus()
      .then((status) => {
        if (cancelled) return
        setBrokerStatus(status.broker.status)
        setTrafficMetrics(status.broker.metrics.traffic ?? emptyTrafficMetrics)
      })
      .catch(() => {
        if (!cancelled) setTrafficMetrics(emptyTrafficMetrics)
      })
    return () => {
      cancelled = true
    }
  }, [])

  // WebSocket connection with exponential-backoff reconnection.
  useEffect(() => {
    mountedRef.current = true
    retryCountRef.current = 0

    let timeoutId: ReturnType<typeof setTimeout> | null = null
    let socket: WebSocket | null = null

    function connect() {
      if (!mountedRef.current) return

      setStreamState('connecting')

      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      const ws = new WebSocket(
        `${protocol}//${window.location.host}/api/v1/broker/events`,
        ['mcm.v1', `Bearer.${tokenRef.current}`],
      )
      socket = ws

      ws.addEventListener('open', () => {
        if (!mountedRef.current) { ws.close(); return }
        // Successful connection — reset retry counter.
        retryCountRef.current = 0
        setStreamState('connected')
      })

      ws.addEventListener('close', (event) => {
        if (!mountedRef.current) return

        setStreamState('disconnected')

        // Close code 4001: treat as auth failure — let the app re-authenticate.
        // No automatic reconnect for auth errors; the token may be stale.
        if (event.code === 4001) {
          return
        }

        if (retryCountRef.current < MAX_RETRIES) {
          const delay = backoffDelay(retryCountRef.current)
          retryCountRef.current += 1
          timeoutId = setTimeout(connect, delay)
        }
        // When MAX_RETRIES is exceeded, stay 'disconnected' permanently for this session.
      })

      ws.addEventListener('error', () => {
        // The 'close' event fires immediately after 'error', so reconnect logic lives there.
        if (mountedRef.current) setStreamState('disconnected')
      })

      ws.addEventListener('message', (message) => {
        if (!mountedRef.current) return
        try {
          const event = JSON.parse(message.data) as BrokerEvent
          if (event.type === 'broker_status' && event.status) {
            setBrokerStatus(event.status)
          }
          if (event.type === 'topic_message' && event.topic) {
            const topicEvent = event as TopicMessage
            setTopics((current) => [topicEvent, ...current].slice(0, 20))
            setTrafficEvents((current) => pruneTrafficEvents([topicEvent, ...current]))
          }
          if (event.type === 'broker_log' && event.source && event.severity && event.message) {
            setLogs((current) => [event as BrokerLog, ...current].slice(0, 100))
          }
        } catch {
          setStreamState('disconnected')
        }
      })
    }

    connect()

    return () => {
      mountedRef.current = false
      if (timeoutId !== null) clearTimeout(timeoutId)
      if (socket !== null) socket.close()
    }
  }, [token])

  const uniqueTopicCount = useMemo(() => new Set(topics.map((t) => t.topic)).size, [topics])
  const liveTrafficMetrics = useMemo(
    () => (trafficEvents.length > 0 ? buildTrafficMetrics(trafficEvents, trafficMetrics) : trafficMetrics),
    [trafficEvents, trafficMetrics],
  )
  const latestTopic = topics[0]

  const value = useMemo<BrokerContextValue>(
    () => ({
      brokerStatus,
      streamState,
      topics,
      logs,
      liveTrafficMetrics,
      latestTopic,
      uniqueTopicCount,
    }),
    [brokerStatus, streamState, topics, logs, liveTrafficMetrics, latestTopic, uniqueTopicCount],
  )

  return <BrokerContext.Provider value={value}>{children}</BrokerContext.Provider>
}

// eslint-disable-next-line react-refresh/only-export-components
export function useBroker(): BrokerContextValue {
  const ctx = useContext(BrokerContext)
  if (!ctx) throw new Error('useBroker must be used within a BrokerProvider')
  return ctx
}
