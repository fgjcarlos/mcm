import { input, leverSecondary, notice } from '../ui/styles'
import type { ListNoun } from './ListControls'
import type { ClientPageState } from './useClientPage'

// Controls of a client-paged list (useClientPage): an optional filter box and
// the page position. Hidden while everything fits on one page, so short lists
// stay quiet; the nav is labelled per noun because a view can hold several.
export function ClientPager<T>({ pager, noun, searchLabel }: { pager: ClientPageState<T>; noun: ListNoun; searchLabel?: string }) {
  const filtering = pager.searchable && Boolean(searchLabel)
  if (pager.itemCount <= pager.pageSize && !(filtering && pager.search)) return null
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-end justify-between gap-4">
        {filtering ? (
          <input type="search" aria-label={searchLabel} placeholder={searchLabel} value={pager.search} onChange={(event) => pager.setSearch(event.target.value)} className={input} />
        ) : <span />}
        <nav aria-label={`${noun.many} pages`} className="flex flex-wrap items-center gap-3 font-mono text-xs text-enamel-soft">
          <span className="tabular-nums">{pager.total} {pager.total === 1 ? noun.one : noun.many}</span>
          <button type="button" onClick={() => pager.goToPage(pager.page - 1)} disabled={pager.page <= 1} className={leverSecondary}>Previous page</button>
          <span className="tabular-nums">Page {pager.page} of {pager.pageCount}</span>
          <button type="button" onClick={() => pager.goToPage(pager.page + 1)} disabled={pager.page >= pager.pageCount} className={leverSecondary}>Next page</button>
        </nav>
      </div>
      {filtering && pager.search && pager.total === 0 ? <p className={notice.neutral}>No {noun.many} match the filter.</p> : null}
    </div>
  )
}
