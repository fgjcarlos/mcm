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
 */

import { renderHook, act } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useBrokerStream } from './useBrokerStream'

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

  constructor(url: string, protocols?: string | string[]) {
    this.url = url
    this.protocols = protocols
    MockWebSocket.instances.push(this)
  }

  addEventListener(
    type: string,
    listener: EventListenerOrEventListenerObject,
  ) {
    const cb =
      typeof listener === 'function'
        ? listener
        : (e: Event | MessageEvent | CloseEvent) => listener.handleEvent(e)
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
    const cb =
      typeof listener === 'function'
        ? listener
        : (e: Event | MessageEvent | CloseEvent) => listener.handleEvent(e)
    set.delete(cb)
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

  /** Simulate a network-level error (readyState goes to CLOSED) */
  errorEvent() {
    this.readyState = 3
    this.emit('error', new Event('error'))
  }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function stubFetchUnused() {
  vi.stubGlobal(
    'fetch',
    vi.fn(() =>
      Promise.reject(new Error('fetch must not be called in these tests')),
    ),
  )
}

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

  // -------------------------------------------------------------------------
  // (a) close → a new WebSocket is constructed after backoff
  // -------------------------------------------------------------------------
  it('(a) reconnects by constructing a new WebSocket after a close event', async () => {
    const { result } = renderHook(() => useBrokerStream('tok'))

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
    renderHook(() => useBrokerStream('tok'))

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
    renderHook(() => useBrokerStream('tok'))

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
    const { result } = renderHook(() => useBrokerStream('tok'))

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
    const { unmount } = renderHook(() => useBrokerStream('tok'))

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
    const { result } = renderHook(() => useBrokerStream('tok'))

    await act(async () => {
      MockWebSocket.instances[0]!.open()
    })

    expect(result.current.streamState).toBe('connected')

    // Simulate a network error
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
})
