import { createElement } from 'react'
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ListControls } from './ListControls'
import type { PagedListState } from './usePagedList'

function state(overrides: Partial<PagedListState<unknown>> = {}): PagedListState<unknown> {
  return {
    rows: [], total: 60, limit: 25, offset: 25, page: 2, pageCount: 3, loading: false, error: null, hasFilters: false,
    search: '', filters: {}, setSearch: vi.fn(), setFilter: vi.fn(), goToPage: vi.fn(), reload: vi.fn(),
    ...overrides,
  }
}

const noun = { one: 'rule', many: 'rules' }

describe('ListControls', () => {
  it('shows the position and total and asks the hook for the neighbour pages', () => {
    const list = state()
    render(createElement(ListControls, { list, noun }))

    expect(screen.getByText('Page 2 of 3')).toBeInTheDocument()
    expect(screen.getByText('60 rules')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
    fireEvent.click(screen.getByRole('button', { name: 'Previous page' }))
    expect(list.goToPage).toHaveBeenNthCalledWith(1, 3)
    expect(list.goToPage).toHaveBeenNthCalledWith(2, 1)
  })

  it('disables both page buttons while a page loads and at the boundaries', () => {
    const { rerender } = render(createElement(ListControls, { list: state({ loading: true }), noun }))
    expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Next page' })).toBeDisabled()

    rerender(createElement(ListControls, { list: state({ total: 0, page: 1, pageCount: 0 }), noun }))
    expect(screen.getByText('Page 1 of 1')).toBeInTheDocument()
    expect(screen.getByText('0 rules')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Next page' })).toBeDisabled()
  })

  it('binds the search box and the filters, and shows the load error', () => {
    const list = state({ search: 'abc', filters: { status: 'failed' }, error: 'Failed to load the list.' })
    render(createElement(ListControls, {
      list, noun, searchLabel: 'Search rules',
      filterFields: [{ key: 'user', label: 'Filter by user' }, { key: 'status', label: 'Filter by status', options: [{ value: '', label: 'All' }, { value: 'failed', label: 'failed' }] }],
    }))

    expect(screen.getByRole('searchbox', { name: 'Search rules' })).toHaveValue('abc')
    expect(screen.getByRole('combobox', { name: 'Filter by status' })).toHaveValue('failed')
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search rules' }), { target: { value: 'abcd' } })
    fireEvent.change(screen.getByRole('textbox', { name: 'Filter by user' }), { target: { value: 'gw' } })
    fireEvent.change(screen.getByRole('combobox', { name: 'Filter by status' }), { target: { value: '' } })
    expect(list.setSearch).toHaveBeenCalledWith('abcd')
    expect(list.setFilter).toHaveBeenCalledWith('user', 'gw')
    expect(list.setFilter).toHaveBeenCalledWith('status', '')
    expect(screen.getByRole('alert')).toHaveTextContent('Failed to load the list.')
  })
})
