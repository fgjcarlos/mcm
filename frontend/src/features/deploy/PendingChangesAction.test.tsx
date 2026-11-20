import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { leverPrimary, leverSecondary } from '../ui/styles'
import { PendingChangesAction } from './PendingChangesAction'

it.each([['admin', 'Review and apply'], ['operator', 'Preview changes']])('stays a secondary control by default for %s', (role, name) => {
  render(<PendingChangesAction role={role} onReview={() => {}} />)
  expect(screen.getByRole('button', { name }).className).toBe(`mt-4 ${leverSecondary}`)
})

it('becomes the primary lever only when asked (#359)', () => {
  render(<PendingChangesAction role="admin" primary onReview={() => {}} />)
  expect(screen.getByRole('button', { name: 'Review and apply' }).className).toBe(`mt-4 ${leverPrimary}`)
})
