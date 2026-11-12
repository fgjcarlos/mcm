import type { BrokerStreamState, LiveEntry } from '../broker/useBrokerStream'
import type { useLiveFeed } from './useLiveFeed'

// Shared chrome of the live views (issue #357): the window limit, the
// pause/resume control, the not-connected banner and the live feed state.
// The noun names what the feed lists ("event"/"events", "message"/"messages").

export type LiveFeed = ReturnType<typeof useLiveFeed<LiveEntry>>

export type LiveFeedNoun = { one: string; many: string }

function plural(count: number, one: string, many: string) {
  return `${count} ${count === 1 ? one : many}`
}

function feedStateText(feed: LiveFeed, noun: LiveFeedNoun, windowSize: number) {
  if (feed.mode === 'paused') {
    const arrived = `Paused · ${plural(feed.arrivedWhilePaused, `new ${noun.one}`, `new ${noun.many}`)} since pausing`
    return feed.overflowed ? `${arrived}. More arrived than the live window keeps, so the oldest of them will not be shown.` : arrived
  }
  if (feed.mode === 'resumed') {
    // Arrivals may be hidden by the filters or already dropped from the
    // window, so say where they went rather than that they are all shown.
    const arrived = `Back to live · ${plural(feed.resumedArrivals, noun.one, noun.many)} arrived while paused`
    return feed.resumedOverflowed
      ? `${arrived}; only the newest ${windowSize} are kept, so the oldest of them were dropped.`
      : `${arrived}; the live window now starts with the newest ${noun.many}.`
  }
  return `Live · new ${noun.many} appear at the top.`
}

export function LiveFeedChrome({ feed, streamState, windowSize, noun, label }: {
  feed: LiveFeed
  streamState: BrokerStreamState
  windowSize: number
  noun: LiveFeedNoun
  // Eyebrow naming the feed, for example "Log feed".
  label: string
}) {
  const connected = streamState === 'connected'
  return <>
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div>
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">{label} · {streamState}</p>
        <p className="mt-2 max-w-2xl text-sm text-slate-300">Live window: the newest {windowSize} {noun.many} received while this page is open. Older {noun.many} are dropped, so there is no history to page through.</p>
      </div>
      <button type="button" onClick={feed.mode === 'paused' ? feed.resume : feed.pause} className="rounded-xl border border-cyan-300/30 px-4 py-2 text-sm font-semibold text-cyan-100 hover:bg-cyan-400/10">
        {feed.mode === 'paused' ? 'Resume live' : 'Pause'}
      </button>
    </div>

    {connected ? null : <p className="mt-4 rounded-xl border border-amber-300/30 bg-amber-400/10 p-3 text-sm text-amber-100">Not connected to the live event stream ({streamState}). New {noun.many} will not arrive until it reconnects.</p>}

    <p role="status" aria-label="Live feed state" className={`mt-4 text-sm ${feed.mode === 'paused' ? 'text-amber-200' : 'text-emerald-200'}`}>{feedStateText(feed, noun, windowSize)}</p>
  </>
}
