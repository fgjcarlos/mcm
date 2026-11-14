import type { PagedListState } from './usePagedList'

// Controls of a paged listing (issue #356): the search box, the listing's
// filters, the page position with the total and the load error. All state
// lives in usePagedList; this component only renders it.

export type ListFilterField = {
  key: string // the filter key passed to setFilter
  label: string
  options?: { value: string; label: string }[] // a select; a text input when absent
}

export type ListNoun = { one: string; many: string }

const inputClass = 'rounded-xl border border-white/10 bg-slate-950/60 px-3 py-2 text-sm text-white outline-none transition focus:border-cyan-300/60 focus:ring-2 focus:ring-cyan-300/30'
const pageButtonClass = 'rounded-xl border border-white/10 px-3 py-1.5 text-xs text-slate-300 transition hover:border-white/20 hover:text-white disabled:opacity-50'

export function ListControls<T>({ list, noun, searchLabel, filterFields = [] }: { list: PagedListState<T>; noun: ListNoun; searchLabel?: string; filterFields?: ListFilterField[] }) {
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-wrap items-end gap-3">
          {searchLabel ? (
            <input type="search" aria-label={searchLabel} placeholder={searchLabel} value={list.search} onChange={(event) => list.setSearch(event.target.value)} className={inputClass} />
          ) : null}
          {filterFields.map((field) => field.options ? (
            <select key={field.key} aria-label={field.label} value={list.filters[field.key] ?? ''} onChange={(event) => list.setFilter(field.key, event.target.value)} className={inputClass}>
              {field.options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
            </select>
          ) : (
            <input key={field.key} type="text" aria-label={field.label} placeholder={field.label} value={list.filters[field.key] ?? ''} onChange={(event) => list.setFilter(field.key, event.target.value)} className={inputClass} />
          ))}
        </div>
        <nav aria-label="Pagination" className="flex items-center gap-3 text-xs text-slate-300">
          <span>{list.total} {list.total === 1 ? noun.one : noun.many}</span>
          <button type="button" onClick={() => list.goToPage(list.page - 1)} disabled={list.loading || list.page <= 1} className={pageButtonClass}>Previous page</button>
          <span>Page {list.page} of {Math.max(1, list.pageCount)}</span>
          <button type="button" onClick={() => list.goToPage(list.page + 1)} disabled={list.loading || list.page >= list.pageCount} className={pageButtonClass}>Next page</button>
        </nav>
      </div>
      {list.error ? <p role="alert" className="rounded-xl border border-dashed border-amber-300/30 bg-amber-400/10 px-4 py-3 text-sm text-amber-100">{list.error}</p> : null}
    </div>
  )
}
