/**
 * Tests for useBrokerStream reconnect logic (issue #169).
 *
 * The hook must:
 *   (a) reconnect with a new WebSocket after a close event
 *   (b) grow the backoff delay on consecutive failures
 *   (c) reset backoff to the base delay after a successful open
 *   (d) ignore malformed frames without changing stream state
 *   (e) cancel pending reconnect timers on unmount
 *   (+) treat an error event the same as a close (schedule reconnect, not permanent disconnect)
 *   (+) error followed by close schedules exactly ONE reconnect (double-schedule guard)
 */

import { renderHook, act } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  addTopicEventToTrafficMetrics,
  buildTrafficMetrics,
  type BrokerTrafficMetrics,
  type TopicMessage,
  useBrokerStream,
} from './useBrokerStream'

// ---------------------------------------------------------------------------
// MockWebSocket — does NOT auto-open; tests call .open() explicitly
// ---------------------------------------------------------------------------

class MockWebSocket {
  static instances: MockWebSocket[] = []

  readonly url: string
  readonly protocols: string | string[] | undefined
  readyState = 0

  private listeners = new Map<
    string,
    Set<(event: Event | MessageEvent | CloseEvent) => void>
  >()

  /**
   * Tracks the wrapped callback for each EventListenerObject so that
   * removeEventListener can delete the exact same reference that was stored
   * by addEventListener — not a new wrapper that would always be a no-op.
   */
  private listenerWrappers = new WeakMap<
    EventListenerObject,
    (event: Event | MessageEvent | CloseEvent) => void
  >()

  constructor(url: string, protocols?: string | string[]) {
    this.url = url
    this.protocols = protocols
    MockWebSocket.instances.push(this)
  }

  addEventListener(
    type: string,
    listener: EventListenerOrEventListenerObject,
  ) {
    let cb: (event: Event | MessageEvent | CloseEvent) => void
    if (typeof listener === 'function') {
      cb = listener
    } else {
      // Reuse the same wrapper if addEventListener is called multiple times
      // with the same object reference (idempotency mirrors the browser spec).
      let wrapper = this.listenerWrappers.get(listener)
      if (!wrapper) {
        wrapper = (e: Event | MessageEvent | CloseEvent) => listener.handleEvent(e)
        this.listenerWrappers.set(listener, wrapper)
      }
      cb = wrapper
    }
    const set =
      this.listeners.get(type) ??
      new Set<(event: Event | MessageEvent | CloseEvent) => void>()
    set.add(cb)
    this.listeners.set(type, set)
  }

  removeEventListener(
    type: string,
    listener: EventListenerOrEventListenerObject,
  ) {
    const set = this.listeners.get(type)
    if (!set) return
    // Resolve the same reference that was stored during addEventListener.
    const cb =
      typeof listener === 'function'
        ? listener
        : this.listenerWrappers.get(listener)
    if (cb) set.delete(cb)
  }

  close() {
    if (this.readyState === 3) return
    this.readyState = 3
    this.emit('close', new Event('close'))
  }

  send() {}

  emit(type: string, event: Event | MessageEvent | CloseEvent) {
    for (const listener of this.listeners.get(type) ?? []) {
      listener(event)
    }
  }

  /** Simulate the server accepting the connection */
  open() {
    this.readyState = 1
    this.emit('open', new Event('open'))
  }

  /** Simulate a well-formed incoming message */
  message(data: unknown) {
    this.emit(
      'message',
      new MessageEvent('message', { data: JSON.stringify(data) }),
    )
  }

  /** Simulate a non-JSON incoming frame */
  malformedMessage(raw: string) {
    this.emit('message', new MessageEvent('message', { data: raw }))
  }

  /** Simulate the server closing the connection */
  serverClose() {
    if (this.readyState === 3) return
    this.readyState = 3
    this.emit('close', new Event('close'))
  }

  /**
   * Simulate a network-level error followed by a close event.
   *
   * Real browser WebSockets always fire 'close' after 'error'. The mock
   * replicates this so the double-schedule guard in the hook is exercised:
   * the error handler schedules a reconnect timer, and the subsequent close
   * event must NOT schedule a second one.
   */
  errorEvent() {
    this.readyState = 3
    this.emit('error', new Event('error'))
    this.emit('close', new Event('close'))
  }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function stubFetchUnused() {
  vi.stubGlobal(
    'fetch',
    vi.fn(() =>
      Promise.resolve(new Response(JSON.stringify({
        broker: {
          status: 'connected',
          metrics: {
            traffic: {
              window_seconds: 300,
              message_count: 0,
              message_rate_per_minute: 0,
              rate_points: [],
              top_topics: [],
              top_clients: [],
              top_clients_available: false,
              top_clients_note: '',
              persistence: 'test',
            },
          },
        },
      }), { status: 200, headers: { 'Content-Type': 'application/json' } })),
    ),
  )
}

function baseTrafficMetrics(): BrokerTrafficMetrics {
  return {
    snapshot_at: '2026-01-01T00:02:00.000Z',
    window_seconds: 300,
    message_count: 6,
    message_rate_per_minute: 1.2,
    rate_points: [
      { timestamp: '2026-01-01T00:01:00.000Z', count: 2 },
      { timestamp: '2026-01-01T00:02:00.000Z', count: 4 },
    ],
    top_topics: [{ name: 'factory/line-1/temperature', count: 4, percentage: 67 }],
    top_clients: [],
    top_clients_available: false,
    top_clients_note: '',
    persistence: 'persisted broker metric events are used when available',
  }
}

function topicMessage(topic: string, observedAt: string): TopicMessage {
  return {
    type: 'topic_message',
    topic,
    payload_preview: '{}',
    payload_format: 'json',
    payload_bytes: 2,
    observed_at: observedAt,
  }
}

function statusResponse(traffic: BrokerTrafficMetrics) {
  return new Response(
    JSON.stringify({
      broker: {
        status: 'connected',
        metrics: { traffic },
      },
    }),
    { status: 200, headers: { 'Content-Type': 'application/json' } },
  )
}

describe('traffic metric accumulation (issue #170)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-01T00:03:00.000Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('does not count live events that overlap the status snapshot', () => {
    const metrics = buildTrafficMetrics(
      [
        topicMessage('factory/line-1/temperature', '2026-01-01T00:01:30.000Z'),
        topicMessage('factory/line-1/temperature', '2026-01-01T00:02:00.000Z'),
        topicMessage('factory/line-2/pressure', '2026-01-01T00:02:01.000Z'),
      ],
      baseTrafficMetrics(),
    )

    expect(metrics.message_count).toBe(7)
    expect(metrics.message_rate_per_minute).toBe(1.4)
    expect(metrics.top_topics).toEqual([
      { name: 'factory/line-1/temperature', count: 4, percentage: (4 * 100) / 7 },
      { name: 'factory/line-2/pressure', count: 1, percentage: (1 * 100) / 7 },
    ])
    expect(metrics.rate_points.find((point) => point.timestamp === '2026-01-01T00:02:00.000Z')?.count).toBe(5)
  })

  it('increments a baseline snapshot one live event at a time', () => {
    const overlapped = addTopicEventToTrafficMetrics(
      baseTrafficMetrics(),
      topicMessage('factory/line-1/temperature', '2026-01-01T00:02:00.000Z'),
    )
    const metrics = addTopicEventToTrafficMetrics(
      overlapped,
      topicMessage('factory/line-1/temperature', '2026-01-01T00:02:01.000Z'),
    )

    expect(metrics.message_count).toBe(7)
    expect(metrics.message_rate_per_minute).toBe(1.4)
    expect(metrics.top_topics[0]).toEqual({
      name: 'factory/line-1/temperature',
      count: 5,
      percentage: (5 * 100) / 7,
    })
  })
})

const noopLogout = () => {}

// ---------------------------------------------------------------------------
// Test suite
// ---------------------------------------------------------------------------

describe('useBrokerStream — reconnect behaviour (issue #169)', () => {
  beforeEach(() => {
    MockWebSocket.instances = []
    vi.stubGlobal('WebSocket', MockWebSocket)
    vi.useFakeTimers()
    stubFetchUnused()
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('seeds dashboard traffic from status and only increments non-overlapping live events', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(statusResponse(baseTrafficMetrics())),
      ),
    )

    const { result } = renderHook(() => useBrokerStream('tok', noopLogout))

    await act(async () => {})
    expect(result.current.liveTrafficMetrics.message_count).toBe(6)

    await act(async () => {
      MockWebSocket.instances[0]!.open()
      MockWebSocket.instances[0]!.message(topicMessage('factory/line-1/temperature', '2026-01-01T00:02:00.000Z'))
      MockWebSocket.instances[0]!.message(topicMessage('factory/line-1/temperature', '2026-01-01T00:02:01.000Z'))
    })

    expect(result.current.liveTrafficMetrics.message_count).toBe(7)
    expect(result.current.liveTrafficMetrics.message_rate_per_minute).toBe(1.4)
    expect(result.current.liveTrafficMetrics.top_topics[0]).toEqual({
      name: 'factory/line-1/temperature',
      count: 5,
      percentage: (5 * 100) / 7,
    })
  })

  it('keeps live traffic metrics when a topic arrives before the status snapshot resolves', async () => {
    vi.setSystemTime(new Date('2026-01-01T00:03:00.000Z'))
    let resolveStatus!: (response: Response) => void
    const statusPromise = new Promise<Response>((resolve) => {
      resolveStatus = resolve
    })
    vi.stubGlobal('fetch', vi.fn(() => statusPromise))

    const { result } = renderHook(() => useBrokerStream('tok', noopLogout))

    await act(async () => {
      MockWebSocket.instances[0]!.open()
      MockWebSocket.instances[0]!.message(topicMessage('factory/line-2/pressure', '2026-01-01T00:02:30.000Z'))
    })

    expect(result.current.liveTrafficMetrics.message_count).toBe(1)

    await act(async () => {
      resolveStatus(statusResponse(baseTrafficMetrics()))
      await statusPromise
    })

    expect(result.current.liveTrafficMetrics.message_count).toBe(7)
    expect(result.current.liveTrafficMetrics.message_rate_per_minute).toBe(1.4)
    expect(result.current.liveTrafficMetrics.top_topics).toEqual([
      { name: 'factory/line-1/temperature', count: 4, percentage: (4 * 100) / 7 },
      { name: 'factory/line-2/pressure', count: 1, percentage: (1 * 100) / 7 },
    ])
    expect(result.current.liveTrafficMetrics.rate_points.find((point) => point.timestamp === '2026-01-01T00:02:00.000Z')?.count).toBe(5)
  })

  // -------------------------------------------------------------------------
  // (a) close → a new WebSocket is constructed after backoff
  // -------------------------------------------------------------------------
  it('(a) reconnects by constructing a new WebSocket after a close event', async () => {
    const { result } = renderHook(() => useBrokerStream('tok', noopLogout))

    // First socket opens successfully
    await act(async () => {
      MockWebSocket.instances[0]!.open()
    })

    expect(result.current.streamState).toBe('connected')
    expect(MockWebSocket.instances).toHaveLength(1)

    // Server closes the connection
    await act(async () => {
      MockWebSocket.instances[0]!.serverClose()
    })

    // The hook must transition to a non-disconnected, non-connected state
    // (either 'reconnecting' or at minimum schedule a reconnect)
    expect(result.current.streamState).toBe('reconnecting')

    // Advance past the base backoff (1 s)
    await act(async () => {
      vi.advanceTimersByTime(1500)
    })

    // A second WebSocket must have been created
    expect(MockWebSocket.instances).toHaveLength(2)

    // Opening the second socket should restore 'connected'
    await act(async () => {
      MockWebSocket.instances[1]!.open()
    })

    expect(result.current.streamState).toBe('connected')
  })

  // -------------------------------------------------------------------------
  // (b) backoff grows between consecutive failures
  // -------------------------------------------------------------------------
  it('(b) doubles the backoff delay on consecutive connection failures', async () => {
    renderHook(() => useBrokerStream('tok', noopLogout))

    // First socket — fail immediately without opening (first failure)
    await act(async () => {
      MockWebSocket.instances[0]!.serverClose()
    })

    expect(MockWebSocket.instances).toHaveLength(1)

    // Advance 1.1 s → first reconnect fires (base delay ≈ 1 s)
    await act(async () => {
      vi.advanceTimersByTime(1100)
    })

    expect(MockWebSocket.instances).toHaveLength(2)

    // Second socket also fails without opening
    await act(async () => {
      MockWebSocket.instances[1]!.serverClose()
    })

    // Backoff should now be ≈ 2 s; advancing only 1.1 s must NOT trigger a third socket
    await act(async () => {
      vi.advanceTimersByTime(1100)
    })

    expect(MockWebSocket.instances).toHaveLength(2)

    // Advance the remaining time (total > 2 s from the second failure)
    await act(async () => {
      vi.advanceTimersByTime(1500)
    })

    expect(MockWebSocket.instances).toHaveLength(3)
  })

  // -------------------------------------------------------------------------
  // (c) successful open resets backoff to base delay
  // -------------------------------------------------------------------------
  it('(c) resets backoff to the base delay after a successful connection', async () => {
    renderHook(() => useBrokerStream('tok', noopLogout))

    // Fail once
    await act(async () => {
      MockWebSocket.instances[0]!.serverClose()
    })

    // Wait for first reconnect (base delay)
    await act(async () => {
      vi.advanceTimersByTime(1100)
    })

    expect(MockWebSocket.instances).toHaveLength(2)

    // Second socket opens successfully → resets backoff
    await act(async () => {
      MockWebSocket.instances[1]!.open()
    })

    // Now the second socket also closes
    await act(async () => {
      MockWebSocket.instances[1]!.serverClose()
    })

    // Backoff was reset: a mere 1.1 s should be enough to trigger the third socket
    await act(async () => {
      vi.advanceTimersByTime(1100)
    })

    expect(MockWebSocket.instances).toHaveLength(3)
  })

  // -------------------------------------------------------------------------
  // (d) malformed frame does not change stream state; subsequent frames work
  // -------------------------------------------------------------------------
  it('(d) ignores malformed frames without changing stream state', async () => {
    const onBrokerStatusChange = vi.fn()

    // We'll observe side-effects via the hook's returned brokerStatus.
    // Use a wrapper to track calls to setBrokerStatus indirectly.
    const { result } = renderHook(() => useBrokerStream('tok', noopLogout))

    await act(async () => {
      MockWebSocket.instances[0]!.open()
    })

    expect(result.current.streamState).toBe('connected')

    // Send a malformed (non-JSON) frame
    await act(async () => {
      MockWebSocket.instances[0]!.malformedMessage('not json at all {{{')
    })

    // State must remain 'connected' — not 'disconnected'
    expect(result.current.streamState).toBe('connected')

    // Send a valid broker_status event after the bad frame
    await act(async () => {
      MockWebSocket.instances[0]!.message({
        type: 'broker_status',
        status: 'connected',
        observed_at: '2026-01-01T00:00:00Z',
      })
    })

    // brokerStatus should have been updated — the socket is still alive
    expect(result.current.brokerStatus).toBe('connected')
    void onBrokerStatusChange
  })

  // -------------------------------------------------------------------------
  // (e) unmount cancels pending reconnect timers
  // -------------------------------------------------------------------------
  it('(e) does not reconnect after the component unmounts', async () => {
    const { unmount } = renderHook(() => useBrokerStream('tok', noopLogout))

    // Trigger a close — would schedule a reconnect timer
    await act(async () => {
      MockWebSocket.instances[0]!.serverClose()
    })

    // Unmount before the backoff fires
    unmount()

    // Advance well past the backoff window
    await act(async () => {
      vi.advanceTimersByTime(5000)
    })

    // Must NOT have constructed a second WebSocket
    expect(MockWebSocket.instances).toHaveLength(1)
  })

  // -------------------------------------------------------------------------
  // error event → reconnect, not permanent disconnection
  // -------------------------------------------------------------------------
  it('schedules a reconnect on an error event instead of staying permanently disconnected', async () => {
    const { result } = renderHook(() => useBrokerStream('tok', noopLogout))

    await act(async () => {
      MockWebSocket.instances[0]!.open()
    })

    expect(result.current.streamState).toBe('connected')

    // Simulate a network error (fires error then close, as a real browser does)
    await act(async () => {
      MockWebSocket.instances[0]!.errorEvent()
    })

    // Must be 'reconnecting', NOT permanently 'disconnected'
    expect(result.current.streamState).toBe('reconnecting')

    // Advance past the backoff
    await act(async () => {
      vi.advanceTimersByTime(1500)
    })

    // A new WebSocket must have been constructed
    expect(MockWebSocket.instances).toHaveLength(2)
  })

  // -------------------------------------------------------------------------
  // double-schedule guard: error + close must schedule exactly ONE reconnect
  // -------------------------------------------------------------------------
  it('schedules exactly one reconnect when error is followed by close', async () => {
    const { result } = renderHook(() => useBrokerStream('tok', noopLogout))

    await act(async () => {
      MockWebSocket.instances[0]!.open()
    })

    expect(result.current.streamState).toBe('connected')

    // errorEvent() fires both 'error' and then 'close' synchronously.
    // The hook's scheduleReconnect guard (reconnectTimer !== null) must
    // prevent the close handler from scheduling a second timer.
    await act(async () => {
      MockWebSocket.instances[0]!.errorEvent()
    })

    expect(result.current.streamState).toBe('reconnecting')

    // Advance well past the backoff window — only ONE new socket should appear
    await act(async () => {
      vi.advanceTimersByTime(3000)
    })

    // Exactly two sockets total: the original + one reconnect (not two)
    expect(MockWebSocket.instances).toHaveLength(2)
  })
})
