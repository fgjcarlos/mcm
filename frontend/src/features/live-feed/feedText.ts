import type { BrokerStreamState } from '../broker/useBrokerStream'

// Wording shared by the live views (Logs, Topics) and the dashboard home.

export type LiveFeedNoun = { one: string; many: string }

// "1 event", "3 events".
export function plural(count: number, one: string, many: string) {
  return `${count} ${count === 1 ? one : many}`
}

// Why a live list shows no rows, most specific reason first.
// - receivedAny: the (paused or live) window holds entries for this list, so
//   the filters hid them all.
// - pausedWhileArriving: the view was paused on an empty window while new
//   entries keep arriving, which is neither idle nor disconnected.
// - filterLabel: how the view names its filter controls ("filter", "filters").
export function emptyFeedText({ noun, filterLabel, receivedAny, pausedWhileArriving, streamState }: {
  noun: LiveFeedNoun
  filterLabel: string
  receivedAny: boolean
  pausedWhileArriving: boolean
  streamState: BrokerStreamState
}) {
  if (receivedAny) return `No ${noun.many} match the ${filterLabel}.`
  if (pausedWhileArriving) return `Paused before any ${noun.many} arrived. Resume live to see them.`
  if (streamState !== 'connected') return `No ${noun.many}: the live stream is ${streamState}.`
  return `Connected · no ${noun.many} received yet.`
}
