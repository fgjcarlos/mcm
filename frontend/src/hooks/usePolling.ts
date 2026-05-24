import { useEffect, useRef, useState } from 'react'

type UsePollingResult<T> = {
  data: T | null
  error: Error | null
  loading: boolean
}

export function usePolling<T>(
  fetcher: () => Promise<T>,
  intervalMs: number,
  options?: { enabled?: boolean },
): UsePollingResult<T> {
  const enabled = options?.enabled ?? true
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<Error | null>(null)
  // Start as `true` so callers can show a loading indicator before the first response.
  const [loading, setLoading] = useState(true)

  // Keep a stable ref to the latest fetcher so the interval does not re-register
  // every time the caller re-renders with a new closure.
  const fetcherRef = useRef(fetcher)
  useEffect(() => {
    fetcherRef.current = fetcher
  })

  // Tick counter: incrementing it triggers a new fetch cycle.
  // The effect fires once on mount and then after each interval expires.
  const [tick, setTick] = useState(0)

  useEffect(() => {
    if (!enabled) return

    const id = setInterval(() => setTick((n) => n + 1), intervalMs)
    return () => clearInterval(id)
  }, [enabled, intervalMs])

  // A separate effect reacts to tick changes and performs the fetch.
  // All setState calls are made only inside .then()/.catch() callbacks so they
  // are never called synchronously in the effect body.
  useEffect(() => {
    if (!enabled) return

    let cancelled = false
    fetcherRef.current()
      .then((result) => {
        if (cancelled) return
        setData(result)
        setError(null)
        setLoading(false)
      })
      .catch((err: unknown) => {
        if (cancelled) return
        // Keep stale data visible on error — only update the error state.
        setError(err instanceof Error ? err : new Error(String(err)))
        setLoading(false)
      })
    return () => { cancelled = true }
  // `tick` is intentionally included so a new fetch fires for each interval tick.
  // `enabled` is included to abort in-flight fetches when disabled.
  }, [enabled, tick])

  return { data, error, loading }
}
