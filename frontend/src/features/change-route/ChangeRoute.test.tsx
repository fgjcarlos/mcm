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
