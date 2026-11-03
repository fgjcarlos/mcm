import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import RecentMFAConfirmation from './RecentMFAConfirmation'
import { useRecentMFA } from './useRecentMFA'

function Confirmation({ token, onLogout }: { token: string; onLogout: () => void }) {
  return <RecentMFAConfirmation state={useRecentMFA(token, onLogout)} />
}

afterEach(() => vi.unstubAllGlobals())

it('confirms MFA in memory and does not transfer a proof to another session', async () => {
  const fetchMock = vi.fn(async () => new Response(JSON.stringify({ proof: 'bound-proof', expires_at: new Date(Date.now() + 300000).toISOString() })))
  vi.stubGlobal('fetch', fetchMock)
  const user = userEvent.setup()
  const onLogout = vi.fn()
  const { rerender } = render(<Confirmation token="first" onLogout={onLogout} />)
  await user.type(screen.getByLabelText('MFA code'), '123456')
  await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
  await screen.findByText(/MFA confirmed/)
  expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/mfa/step-up', expect.objectContaining({ headers: expect.objectContaining({ Authorization: 'Bearer first' }), body: '{"code":"123456"}' }))
  expect(screen.getByLabelText('MFA code')).toHaveValue('')
  rerender(<Confirmation token="second" onLogout={onLogout} />)
  expect(screen.queryByText(/MFA confirmed/)).not.toBeInTheDocument()
})

it('reports a rejected code without treating it as an expired login', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ error: 'invalid or reused mfa code' }), { status: 401 })))
  const onLogout = vi.fn()
  const user = userEvent.setup()
  render(<Confirmation token="first" onLogout={onLogout} />)
  await user.type(screen.getByLabelText('MFA code'), '123456')
  await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('invalid or reused mfa code')
  expect(onLogout).not.toHaveBeenCalled()
})
