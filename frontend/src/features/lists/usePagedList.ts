import { useCallback, useEffect, useRef, useState } from 'react'
import { authenticatedFetch, isUnauthorizedResponseError } from '../api/client'

// Shared paged, searchable listing (issue #356). The backend answers with
// { <rowsKey>: [...], total, limit, offset } and echoes the limit/offset it
// applied, so paging always uses the echoed values. The search term, the
// filters and the page live in the URL query under `<namespace>.<param>`
// (users.q, acl.user, deploy.page...), written with replaceState so leaving
// and coming back restores them and the back button skips page changes.

export type ListFilters = Record<string, string>

export type PagedListState<T> = {
  rows: T[]
  total: number
  limit: number
  offset: number
  page: number // 1-based
  pageCount: number // ceil(total / limit), 0 when there are no rows
  loading: boolean
  error: string | null
  hasFilters: boolean // true when a search term or any filter is non-empty
  search: string // the search input value, ahead of the debounced request
  filters: ListFilters
  setSearch: (value: string) => void
  setFilter: (key: string, value: string) => void
  goToPage: (page: number) => void
  reload: () => void
}

export type PagedListOptions = {
  path: string
  rowsKey: string
  token: string
  onUnauthorized?: () => void
  namespace: string
  searchParam?: string
  filterParams?: Record<string, string> // API (and URL) param name per filter key
  pageSize?: number
  debounceMs?: number
}

type Query = { search: string; filters: ListFilters; page: number }
type Envelope<T> = { rows: T[]; total: number; limit: number; offset: number }

const loadError = 'Failed to load the list.'

function readQuery(namespace: string, searchParam: string, filterParams: Record<string, string>): Query {
  const params = new URLSearchParams(window.location.search)
  const filters: ListFilters = {}
  for (const [key, param] of Object.entries(filterParams)) {
    const value = params.get(`${namespace}.${param}`)
    if (value) filters[key] = value
  }
  const page = Number(params.get(`${namespace}.page`))
  return { search: params.get(`${namespace}.${searchParam}`) ?? '', filters, page: Number.isInteger(page) && page > 1 ? page : 1 }
}

function writeQuery(namespace: string, searchParam: string, filterParams: Record<string, string>, query: Query) {
  const params = new URLSearchParams(window.location.search)
  for (const key of [...params.keys()]) if (key.startsWith(`${namespace}.`)) params.delete(key)
  if (query.search.trim()) params.set(`${namespace}.${searchParam}`, query.search)
  for (const [key, value] of Object.entries(query.filters)) if (value.trim()) params.set(`${namespace}.${filterParams[key] ?? key}`, value)
  if (query.page > 1) params.set(`${namespace}.page`, String(query.page))
  const search = params.toString() ? `?${params}` : ''
  if (window.location.search === search) return
  window.history.replaceState(window.history.state, '', `${window.location.pathname}${search}${window.location.hash}`)
}

export function usePagedList<T>({ path, rowsKey, token, onUnauthorized, namespace, searchParam = 'q', filterParams = {}, pageSize = 25, debounceMs = 250 }: PagedListOptions): PagedListState<T> {
  // Callers usually pass filterParams inline; key effects on its content.
  const filterParamsKey = JSON.stringify(filterParams)
  const [query, setQuery] = useState<Query>(() => readQuery(namespace, searchParam, filterParams))
  const [searchInput, setSearchInput] = useState(query.search)
  const [data, setData] = useState<Envelope<T> | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [completedKey, setCompletedKey] = useState<string | null>(null)
  const [tick, setTick] = useState(0)
  const debounceTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const onUnauthorizedRef = useRef(onUnauthorized)
  useEffect(() => {
    onUnauthorizedRef.current = onUnauthorized
  })

  const limit = data?.limit ?? pageSize
  const params = new URLSearchParams()
  if (query.search.trim()) params.set(searchParam, query.search.trim())
  for (const [key, value] of Object.entries(query.filters)) if (value.trim()) params.set(filterParams[key] ?? key, value.trim())
  params.set('limit', String(pageSize))
  params.set('offset', String((query.page - 1) * limit))
  const url = `${path}?${params}`
  const requestKey = `${tick} ${url}`

  useEffect(() => {
    writeQuery(namespace, searchParam, JSON.parse(filterParamsKey), query)
  }, [namespace, searchParam, filterParamsKey, query])

  useEffect(() => {
    let cancelled = false
    authenticatedFetch(url, { token, onUnauthorized: () => onUnauthorizedRef.current?.() })
      .then(async (response) => {
        const body = (await response.json().catch(() => null)) as Record<string, unknown> | null
        if (!response.ok) throw new Error(typeof body?.error === 'string' && body.error ? body.error : loadError)
        const rows = body?.[rowsKey]
        if (!Array.isArray(rows) || typeof body?.total !== 'number' || typeof body.limit !== 'number' || typeof body.offset !== 'number') throw new Error('Unexpected response from the server.')
        return { rows: rows as T[], total: body.total, limit: body.limit, offset: body.offset }
      })
      .then((envelope) => {
        if (cancelled) return
        setData(envelope)
        setError(null)
        setCompletedKey(requestKey)
        // A restored page past the end (the data shrank meanwhile) falls back to the last page.
        const lastPage = Math.max(1, Math.ceil(envelope.total / envelope.limit))
        setQuery((current) => (current.page > lastPage ? { ...current, page: lastPage } : current))
      })
      .catch((err: unknown) => {
        if (cancelled || isUnauthorizedResponseError(err)) return
        // fetch rejects with a TypeError on network failure; its text is not for operators.
        setError(err instanceof Error && !(err instanceof TypeError) ? err.message : loadError)
        setCompletedKey(requestKey)
      })
    return () => {
      cancelled = true
    }
  }, [url, requestKey, rowsKey, token])

  useEffect(() => () => clearTimeout(debounceTimer.current), [])

  const setSearch = useCallback((value: string) => {
    setSearchInput(value)
    clearTimeout(debounceTimer.current)
    debounceTimer.current = setTimeout(() => {
      setQuery((current) => (current.search === value ? current : { ...current, search: value, page: 1 }))
    }, debounceMs)
  }, [debounceMs])

  const setFilter = useCallback((key: string, value: string) => {
    setQuery((current) => ((current.filters[key] ?? '') === value ? current : { ...current, filters: { ...current.filters, [key]: value }, page: 1 }))
  }, [])

  const total = data?.total ?? 0
  const pageCount = total > 0 ? Math.ceil(total / limit) : 0

  const goToPage = useCallback((page: number) => {
    const next = Math.min(Math.max(1, Math.trunc(page) || 1), Math.max(1, pageCount))
    setQuery((current) => (current.page === next ? current : { ...current, page: next }))
  }, [pageCount])

  const reload = useCallback(() => setTick((n) => n + 1), [])

  const filters = Object.fromEntries(Object.entries(query.filters).filter(([, value]) => value !== ''))

  return {
    rows: data?.rows ?? [],
    total,
    limit,
    offset: data?.offset ?? (query.page - 1) * limit,
    page: query.page,
    pageCount,
    loading: completedKey !== requestKey,
    error,
    hasFilters: query.search.trim() !== '' || Object.values(query.filters).some((value) => value.trim() !== ''),
    search: searchInput,
    filters,
    setSearch,
    setFilter,
    goToPage,
    reload,
  }
}
