import type { ReactNode } from 'react'
import { fieldLabel, input, leverSecondary, notice, select } from '../ui/styles'
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

// A visible label above the control, as in the Logs view: a placeholder vanishes
// once a value is typed and a select shows only its chosen option (#406).
function ListField({ label, children }: { label: string; children: ReactNode }) {
  return <label className="flex min-w-0 max-w-full flex-col gap-2"><span className={fieldLabel}>{label}</span>{children}</label>
}

export function ListControls<T>({ list, noun, searchLabel, filterFields = [] }: { list: PagedListState<T>; noun: ListNoun; searchLabel?: string; filterFields?: ListFilterField[] }) {
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex min-w-0 flex-wrap items-end gap-3">
          {searchLabel ? (
            <ListField label={searchLabel}>
              <input type="search" aria-label={searchLabel} value={list.search} onChange={(event) => list.setSearch(event.target.value)} className={`${input} max-w-full`} />
            </ListField>
          ) : null}
          {filterFields.map((field) => (
            <ListField key={field.key} label={field.label}>
              {field.options ? (
                <select aria-label={field.label} value={list.filters[field.key] ?? ''} onChange={(event) => list.setFilter(field.key, event.target.value)} className={`${select} max-w-full`}>
                  {field.options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
                </select>
              ) : (
                <input type="text" aria-label={field.label} value={list.filters[field.key] ?? ''} onChange={(event) => list.setFilter(field.key, event.target.value)} className={`${input} max-w-full`} />
              )}
            </ListField>
          ))}
        </div>
        <nav aria-label="Pagination" className="flex flex-wrap items-center gap-3 font-mono text-xs text-enamel-soft">
          <span className="tabular-nums">{list.total} {list.total === 1 ? noun.one : noun.many}</span>
          <button type="button" onClick={() => list.goToPage(list.page - 1)} disabled={list.loading || list.page <= 1} className={leverSecondary}>Previous page</button>
          <span className="tabular-nums">Page {list.page} of {Math.max(1, list.pageCount)}</span>
          <button type="button" onClick={() => list.goToPage(list.page + 1)} disabled={list.loading || list.page >= list.pageCount} className={leverSecondary}>Next page</button>
        </nav>
      </div>
      {list.error ? <p role="alert" className={notice.failed}>{list.error}</p> : null}
    </div>
  )
}
