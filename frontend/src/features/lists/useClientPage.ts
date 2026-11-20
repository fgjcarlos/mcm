import { useState } from 'react'

// Client-side paging of a list the browser already holds (live feeds,
// histories and editors whose endpoints return the whole collection). Each
// row keeps its index in the original array so editors can still update the
// right item. Server-paged listings use usePagedList instead.

export type ClientPageRow<T> = { item: T; index: number }

export type ClientPageState<T> = {
  rows: ClientPageRow<T>[] // the current page
  page: number // 1-based, always within 1..pageCount
  pageCount: number // at least 1
  pageSize: number
  total: number // items matching the search
  itemCount: number // all items, before the search
  searchable: boolean
  search: string
  setSearch: (value: string) => void
  goToPage: (page: number) => void
  showLast: () => void // clears the search and follows the last item (an editor's new row)
}

export function useClientPage<T>(items: readonly T[], { pageSize = 10, match }: { pageSize?: number; match?: (item: T, query: string) => boolean } = {}): ClientPageState<T> {
  const [requested, setRequested] = useState(1)
  const [search, setSearchValue] = useState('')
  const query = search.trim().toLowerCase()
  const matching = items.map((item, index) => ({ item, index })).filter(({ item }) => !match || !query || match(item, query))
  const pageCount = Math.max(1, Math.ceil(matching.length / pageSize))
  // Derived, so a list that shrinks (or showLast's Infinity) lands on a real page.
  const page = Math.min(Math.max(1, requested), pageCount)
  return {
    rows: matching.slice((page - 1) * pageSize, page * pageSize),
    page,
    pageCount,
    pageSize,
    total: matching.length,
    itemCount: items.length,
    searchable: Boolean(match),
    search,
    setSearch: (value) => {
      setSearchValue(value)
      setRequested(1)
    },
    goToPage: (next) => setRequested(Math.trunc(next) || 1),
    showLast: () => {
      setSearchValue('')
      setRequested(Number.POSITIVE_INFINITY)
    },
  }
}
