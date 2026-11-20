import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import ChangeRoute from './ChangeRoute'
import type { useRecentMFA } from '../mfa/useRecentMFA'

const mfa = { proof: 'proof', expires: Date.now() + 300000, code: '', setCode: vi.fn(), busy: false, error: '', confirm: vi.fn(), clear: vi.fn() } as ReturnType<typeof useRecentMFA>

// The probe proves activation only for a verified result; otherwise it says what the result lamp says.
it.each([
  ['active_verified', 'Probe: done'],
  ['applied', 'Probe: not proven'],
  ['failed', 'Probe: failed'],
])('reports the probe honestly for a %s result', async (status, probe) => {
  const user = userEvent.setup()
  render(<ChangeRoute hasChanges reviewKey="rev-1" mfa={mfa} applyLabel="Apply" onApply={async () => ({ status })} />)
  await user.click(screen.getByRole('button', { name: 'Apply' }))
  await user.click(screen.getByRole('button', { name: 'Confirm Apply' }))
  expect(await within(screen.getByRole('list', { name: 'Change route' })).findByRole('listitem', { name: probe })).toBeInTheDocument()
})

// Arming the apply replaces the Apply button, so focus moves to the first control of the confirmation (#359).
it.each([
  ['without a recent MFA proof', { ...mfa, proof: '' }, () => screen.getByLabelText('MFA code')],
  ['with a recent MFA proof', mfa, () => screen.getByRole('button', { name: 'Confirm Apply' })],
])('moves focus into the confirmation when Apply is pressed %s', async (_, state, target) => {
  const user = userEvent.setup()
  render(<ChangeRoute hasChanges reviewKey="rev-1" mfa={state} applyLabel="Apply" onApply={async () => null} />)
  await user.click(screen.getByRole('button', { name: 'Apply' }))
  expect(target()).toHaveFocus()
})

it('moves focus to Confirm once the MFA proof arrives, since the verified form is replaced by text (#359)', async () => {
  const user = userEvent.setup()
  const route = (state: typeof mfa) => <ChangeRoute hasChanges reviewKey="rev-1" mfa={state} applyLabel="Apply" onApply={async () => null} />
  const { rerender } = render(route({ ...mfa, proof: '' }))
  await user.click(screen.getByRole('button', { name: 'Apply' }))

  rerender(route(mfa))

  expect(screen.getByRole('button', { name: 'Confirm Apply' })).toHaveFocus()
})

it('returns focus to Apply when the confirmation is cancelled (#359)', async () => {
  const user = userEvent.setup()
  render(<ChangeRoute hasChanges reviewKey="rev-1" mfa={mfa} applyLabel="Apply" onApply={async () => null} />)
  await user.click(screen.getByRole('button', { name: 'Apply' }))

  await user.click(screen.getByRole('button', { name: 'Cancel' }))

  expect(screen.getByRole('button', { name: 'Apply' })).toHaveFocus()
})
