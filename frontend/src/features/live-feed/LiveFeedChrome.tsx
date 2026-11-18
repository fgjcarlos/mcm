import type { BrokerStreamState, LiveEntry } from '../broker/useBrokerStream'
import type { useLiveFeed } from './useLiveFeed'
import { plural, type LiveFeedNoun } from './feedText'
import { Lamp } from '../ui/primitives'
import { bodyText, leverSecondary, notice } from '../ui/styles'

// Shared chrome of the live views (issue #357): the window limit, the
// pause/resume control, the not-connected banner and the live feed state.
// The noun names what the feed lists ("event"/"events", "message"/"messages").

export type LiveFeed = ReturnType<typeof useLiveFeed<LiveEntry>>

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
  // Heading naming the feed, for example "Log feed".
  label: string
}) {
  const connected = streamState === 'connected'
  return <>
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div>
        {/* The stream is connected, not verified: an enamel lamp, amber when it is not up. */}
        <h3 className="flex flex-wrap items-center gap-x-3 gap-y-1 text-lg font-semibold text-enamel-bright">{label} <Lamp tone={connected ? 'neutral' : 'caution'}>{streamState}</Lamp></h3>
        <p className={`mt-1 max-w-[68ch] ${bodyText}`}>Live window: the newest {windowSize} {noun.many} received while this page is open. Older {noun.many} are dropped, so there is no history to page through.</p>
      </div>
      <button type="button" onClick={feed.mode === 'paused' ? feed.resume : feed.pause} className={leverSecondary}>
        {feed.mode === 'paused' ? 'Resume live' : 'Pause'}
      </button>
    </div>

    {connected ? null : <p className={`mt-4 ${notice.caution}`}>Not connected to the live event stream ({streamState}). New {noun.many} will not arrive until it reconnects.</p>}

    <p role="status" aria-label="Live feed state" className={`mt-4 font-mono text-[0.8125rem] ${feed.mode === 'paused' ? 'text-lamp' : 'text-enamel-soft'}`}>{feedStateText(feed, noun, windowSize)}</p>
  </>
}
