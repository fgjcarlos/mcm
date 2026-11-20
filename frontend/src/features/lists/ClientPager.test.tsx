import { act, fireEvent, render, renderHook, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { ClientPager } from './ClientPager'
import { useClientPage } from './useClientPage'

const items = Array.from({ length: 23 }, (_, i) => `item ${i + 1}`)
const noun = { one: 'entry', many: 'entries' }

describe('useClientPage', () => {
  it('slices the items into pages and keeps each row with its original index', () => {
    const { result } = renderHook(() => useClientPage(items, { pageSize: 10 }))
    expect(result.current.page).toBe(1)
    expect(result.current.pageCount).toBe(3)
    expect(result.current.rows.map((row) => row.item)).toEqual(items.slice(0, 10))

    act(() => result.current.goToPage(3))
    expect(result.current.rows).toEqual([{ item: 'item 21', index: 20 }, { item: 'item 22', index: 21 }, { item: 'item 23', index: 22 }])
  })

  it('clamps the page when the items shrink and ignores out-of-range requests', () => {
    const { result, rerender } = renderHook(({ list }) => useClientPage(list, { pageSize: 10 }), { initialProps: { list: items } })
    act(() => result.current.goToPage(3))
    rerender({ list: items.slice(0, 12) })
    expect(result.current.page).toBe(2)
    expect(result.current.rows.map((row) => row.item)).toEqual(['item 11', 'item 12'])

    act(() => result.current.goToPage(0))
    expect(result.current.page).toBe(1)
    rerender({ list: [] })
    expect(result.current.page).toBe(1)
    expect(result.current.pageCount).toBe(1)
    expect(result.current.rows).toEqual([])
  })

  it('filters with the match function, back to page 1, keeping the original indexes', () => {
    const { result } = renderHook(() => useClientPage(items, { pageSize: 10, match: (item, query) => item.endsWith(query) }))
    act(() => result.current.goToPage(2))
    act(() => result.current.setSearch('3'))
    expect(result.current.page).toBe(1)
    expect(result.current.total).toBe(3)
    expect(result.current.rows).toEqual([{ item: 'item 3', index: 2 }, { item: 'item 13', index: 12 }, { item: 'item 23', index: 22 }])
  })

  it('showLast clears the search and lands on the page holding the newest item', () => {
    const { result, rerender } = renderHook(({ list }) => useClientPage(list, { pageSize: 10, match: (item, query) => item.includes(query) }), { initialProps: { list: items } })
    act(() => result.current.setSearch('item 1'))
    act(() => result.current.showLast())
    rerender({ list: [...items, 'item 24'] })
    expect(result.current.search).toBe('')
    expect(result.current.page).toBe(3)
    expect(result.current.rows.at(-1)).toEqual({ item: 'item 24', index: 23 })
  })
})

describe('ClientPager', () => {
  it('shows the position, the total and moves between pages', () => {
    function Harness() {
      const pager = useClientPage(items, { pageSize: 10 })
      return <><ClientPager pager={pager} noun={noun} /><ul>{pager.rows.map((row) => <li key={row.index}>{row.item}</li>)}</ul></>
    }
    render(<Harness />)
    const nav = screen.getByRole('navigation', { name: 'entries pages' })
    expect(within(nav).getByText('23 entries')).toBeInTheDocument()
    expect(within(nav).getByText('Page 1 of 3')).toBeInTheDocument()
    expect(within(nav).getByRole('button', { name: 'Previous page' })).toBeDisabled()

    fireEvent.click(within(nav).getByRole('button', { name: 'Next page' }))
    expect(within(nav).getByText('Page 2 of 3')).toBeInTheDocument()
    expect(screen.getByText('item 11')).toBeInTheDocument()
    expect(screen.queryByText('item 1')).not.toBeInTheDocument()
    fireEvent.click(within(nav).getByRole('button', { name: 'Next page' }))
    expect(within(nav).getByRole('button', { name: 'Next page' })).toBeDisabled()
  })

  it('renders nothing for a single page without a search', () => {
    function Harness() {
      const pager = useClientPage(items.slice(0, 4), { pageSize: 10, match: (item, query) => item.includes(query) })
      return <ClientPager pager={pager} noun={noun} searchLabel="Filter entries" />
    }
    const { container } = render(<Harness />)
    expect(container).toBeEmptyDOMElement()
  })

  it('offers the search box once the list spans several pages and reports no match', () => {
    function Harness() {
      const pager = useClientPage(items, { pageSize: 10, match: (item, query) => item.includes(query) })
      return <ClientPager pager={pager} noun={noun} searchLabel="Filter entries" />
    }
    render(<Harness />)
    fireEvent.change(screen.getByRole('searchbox', { name: 'Filter entries' }), { target: { value: 'nope' } })
    expect(screen.getByText('0 entries')).toBeInTheDocument()
    expect(screen.getByText('No entries match the filter.')).toBeInTheDocument()
    expect(screen.getByRole('searchbox', { name: 'Filter entries' })).toHaveValue('nope')
  })
})
