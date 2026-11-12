import { useCallback, useState } from 'react'
import type { LiveEntry } from '../broker/useBrokerStream'

// Visual pause for a live window (issue #357). The stream keeps receiving
// while paused; the view keeps showing the entries it had, so the row being
// read neither moves nor loses focus. Entries are newest first and their ids
// grow by one per arrival, which makes the arrival count exact even after the
// window dropped older entries.

export type LiveFeedMode = 'live' | 'paused' | 'resumed'

type PauseState<T> =
  | { mode: 'live' }
  | { mode: 'paused'; snapshot: T[]; newestIdAtPause: number }
  | { mode: 'resumed'; arrivals: number }

export function useLiveFeed<T extends LiveEntry>(items: T[], windowSize: number) {
  const [state, setState] = useState<PauseState<T>>({ mode: 'live' })
  const newestId = items[0]?.id ?? 0

  const pause = useCallback(() => {
    setState({ mode: 'paused', snapshot: items, newestIdAtPause: items[0]?.id ?? 0 })
  }, [items])

  const resume = useCallback(() => {
    setState((current) => current.mode === 'paused'
      ? { mode: 'resumed', arrivals: Math.max(0, newestId - current.newestIdAtPause) }
      : current)
  }, [newestId])

  const arrivedWhilePaused = state.mode === 'paused' ? Math.max(0, newestId - state.newestIdAtPause) : 0

  return {
    mode: state.mode as LiveFeedMode,
    visible: state.mode === 'paused' ? state.snapshot : items,
    arrivedWhilePaused,
    // More arrived than the window keeps: resuming will not show all of them.
    overflowed: arrivedWhilePaused > windowSize,
    resumedArrivals: state.mode === 'resumed' ? state.arrivals : 0,
    // Some of those arrivals were dropped before the view returned to live.
    resumedOverflowed: state.mode === 'resumed' && state.arrivals > windowSize,
    pause,
    resume,
  }
}
