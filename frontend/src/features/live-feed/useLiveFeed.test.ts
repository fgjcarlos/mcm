import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useLiveFeed } from './useLiveFeed'

type Entry = { id: number; label: string }

// Newest first, like the live windows in useBrokerStream.
function window(newest: number, size: number): Entry[] {
  const entries: Entry[] = []
  for (let id = newest; id > Math.max(0, newest - size); id -= 1) entries.push({ id, label: `entry ${id}` })
  return entries
}

describe('useLiveFeed', () => {
  it('shows the live entries while live', () => {
    const { result, rerender } = renderHook(({ items }) => useLiveFeed(items, 5), { initialProps: { items: window(2, 5) } })
    expect(result.current.mode).toBe('live')
    rerender({ items: window(3, 5) })
    expect(result.current.visible.map((entry) => entry.id)).toEqual([3, 2, 1])
    expect(result.current.arrivedWhilePaused).toBe(0)
  })

  it('keeps the same entries while paused and counts what arrives meanwhile', () => {
    const { result, rerender } = renderHook(({ items }) => useLiveFeed(items, 5), { initialProps: { items: window(3, 5) } })
    const shown = result.current.visible
    act(() => result.current.pause())
    rerender({ items: window(5, 5) })
    expect(result.current.mode).toBe('paused')
    expect(result.current.visible).toBe(shown)
    expect(result.current.arrivedWhilePaused).toBe(2)
    expect(result.current.overflowed).toBe(false)
  })

  it('reports when more entries arrived while paused than the live window keeps', () => {
    const { result, rerender } = renderHook(({ items }) => useLiveFeed(items, 5), { initialProps: { items: window(3, 5) } })
    act(() => result.current.pause())
    rerender({ items: window(9, 5) })
    expect(result.current.arrivedWhilePaused).toBe(6)
    expect(result.current.overflowed).toBe(true)
    act(() => result.current.resume())
    expect(result.current.resumedArrivals).toBe(6)
    expect(result.current.resumedOverflowed).toBe(true)
  })

  it('counts arrivals when paused before anything arrived', () => {
    const { result, rerender } = renderHook(({ items }) => useLiveFeed(items, 5), { initialProps: { items: [] as Entry[] } })
    act(() => result.current.pause())
    rerender({ items: window(2, 5) })
    expect(result.current.visible).toEqual([])
    expect(result.current.arrivedWhilePaused).toBe(2)
  })

  it('returns to the newest entries on resume and says how many arrived while paused', () => {
    const { result, rerender } = renderHook(({ items }) => useLiveFeed(items, 5), { initialProps: { items: window(3, 5) } })
    act(() => result.current.pause())
    rerender({ items: window(5, 5) })
    act(() => result.current.resume())
    expect(result.current.mode).toBe('resumed')
    expect(result.current.visible.map((entry) => entry.id)).toEqual([5, 4, 3, 2, 1])
    expect(result.current.resumedArrivals).toBe(2)
    expect(result.current.resumedOverflowed).toBe(false)
    expect(result.current.arrivedWhilePaused).toBe(0)
    rerender({ items: window(6, 5) })
    expect(result.current.mode).toBe('resumed')
    expect(result.current.visible[0]!.id).toBe(6)
  })

  it('leaves the back-to-live notice when paused again', () => {
    const { result } = renderHook(({ items }) => useLiveFeed(items, 5), { initialProps: { items: window(3, 5) } })
    act(() => result.current.pause())
    act(() => result.current.resume())
    act(() => result.current.pause())
    expect(result.current.mode).toBe('paused')
    expect(result.current.arrivedWhilePaused).toBe(0)
  })
})
