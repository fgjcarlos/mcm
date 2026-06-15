import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AccountSecurityPanel from './AccountSecurityPanel'
import type { AdminUser } from '../auth/useAuthSession'

// Mock qrcode.react so SVG rendering works cleanly under jsdom
vi.mock('qrcode.react', () => ({
  QRCodeSVG: () => <svg data-testid="qr-code" />,
}))

type RouteHandler = (init?: RequestInit) => Response | Promise<Response>

function jsonResponse(body: unknown, init?: ResponseInit) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
}

function noContentResponse() {
  return new Response(null, { status: 204 })
}

function installFetchMock(routes: Record<string, RouteHandler>) {
  return vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const requestUrl =
      typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
    const key = `${method.toUpperCase()} ${requestUrl}`
    const handler = routes[key]

    if (!handler) {
      throw new Error(`Unhandled fetch: ${key}`)
    }

    return await handler(init)
  })
}

const mfaDisabledUser: AdminUser = {
  id: 1,
  username: 'operator',
  disabled: false,
  role: 'operator',
  mfa_enabled: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

const mfaEnabledUser: AdminUser = {
  ...mfaDisabledUser,
  mfa_enabled: true,
}

const mockSetupResponse = {
  otpauth_url: 'otpauth://totp/MCM:operator?secret=JBSWY3DPEHPK3PXP&issuer=MCM',
  secret: 'JBSWY3DPEHPK3PXP',
  recovery_codes: [
    'abc12-def34',
    'ghi56-jkl78',
    'mno90-pqr12',
    'stu34-vwx56',
    'yza78-bcd90',
    'efg12-hij34',
    'klm56-nop78',
    'qrs90-tuv12',
    'wxy34-zab56',
    'cde78-fgh90',
  ],
}

describe('AccountSecurityPanel — MFA disabled user', () => {
  const token = 'test-token'
  const onLogout = vi.fn()

  beforeEach(() => {
    onLogout.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('shows Enable MFA button when MFA is disabled', () => {
    vi.stubGlobal('fetch', vi.fn())
    render(
      <AccountSecurityPanel
        token={token}
        currentUser={mfaDisabledUser}
        onLogout={onLogout}
      />,
    )
    expect(screen.getByRole('button', { name: /enable mfa/i })).toBeInTheDocument()
    expect(screen.getByText(/mfa is not enabled/i)).toBeInTheDocument()
  })

  it('clicking Enable MFA calls setup and renders QR code, secret, and recovery codes', async () => {
    const fetchMock = installFetchMock({
      'POST /api/v1/auth/mfa/setup': () => jsonResponse(mockSetupResponse),
    })
    vi.stubGlobal('fetch', fetchMock)

    const user = userEvent.setup()
    render(
      <AccountSecurityPanel
        token={token}
        currentUser={mfaDisabledUser}
        onLogout={onLogout}
      />,
    )

    await user.click(screen.getByRole('button', { name: /enable mfa/i }))

    await screen.findByTestId('qr-code')
    // Secret is formatted with spaces — "JBSW Y3DP EHPK 3PXP" — look for "JBSW"
    expect(screen.getByText(/JBSW/)).toBeInTheDocument()
    expect(screen.getByText('abc12-def34')).toBeInTheDocument()
    expect(screen.getByText('ghi56-jkl78')).toBeInTheDocument()

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/auth/mfa/setup',
        expect.objectContaining({
          method: 'POST',
          headers: expect.objectContaining({ Authorization: `Bearer ${token}` }),
        }),
      )
    })
  })

  it('Confirm & enable button is disabled until the saved-codes checkbox is checked', async () => {
    const fetchMock = installFetchMock({
      'POST /api/v1/auth/mfa/setup': () => jsonResponse(mockSetupResponse),
    })
    vi.stubGlobal('fetch', fetchMock)

    const user = userEvent.setup()
    render(
      <AccountSecurityPanel
        token={token}
        currentUser={mfaDisabledUser}
        onLogout={onLogout}
      />,
    )

    await user.click(screen.getByRole('button', { name: /enable mfa/i }))
    await screen.findByTestId('qr-code')

    // Type a 6-digit code
    await user.type(screen.getByPlaceholderText('000000'), '123456')

    const confirmBtn = screen.getByRole('button', { name: /confirm & enable/i })
    // Still disabled — checkbox not checked
    expect(confirmBtn).toBeDisabled()

    // Check the checkbox
    await user.click(screen.getByRole('checkbox'))
    expect(confirmBtn).not.toBeDisabled()
  })

  it('successful verify shows success state and calls onMFAChange', async () => {
    const fetchMock = installFetchMock({
      'POST /api/v1/auth/mfa/setup': () => jsonResponse(mockSetupResponse),
      'POST /api/v1/auth/mfa/verify': () => noContentResponse(),
    })
    vi.stubGlobal('fetch', fetchMock)

    const onMFAChange = vi.fn()
    const user = userEvent.setup()
    render(
      <AccountSecurityPanel
        token={token}
        currentUser={mfaDisabledUser}
        onLogout={onLogout}
        onMFAChange={onMFAChange}
      />,
    )

    await user.click(screen.getByRole('button', { name: /enable mfa/i }))
    await screen.findByTestId('qr-code')

    await user.click(screen.getByRole('checkbox'))
    await user.type(screen.getByPlaceholderText('000000'), '123456')
    await user.click(screen.getByRole('button', { name: /confirm & enable/i }))

    await screen.findByText(/mfa enabled/i)
    expect(onMFAChange).toHaveBeenCalledOnce()

    // QR code and secret should no longer be visible after success
    expect(screen.queryByTestId('qr-code')).not.toBeInTheDocument()
  })

  it('failed verify (401 wrong code) shows error and keeps the verify step — does NOT re-call setup', async () => {
    let setupCallCount = 0
    const fetchMock = installFetchMock({
      'POST /api/v1/auth/mfa/setup': () => {
        setupCallCount++
        return jsonResponse(mockSetupResponse)
      },
      'POST /api/v1/auth/mfa/verify': () =>
        jsonResponse({ error: 'invalid mfa code' }, { status: 401 }),
    })
    vi.stubGlobal('fetch', fetchMock)

    const user = userEvent.setup()
    render(
      <AccountSecurityPanel
        token={token}
        currentUser={mfaDisabledUser}
        onLogout={onLogout}
      />,
    )

    await user.click(screen.getByRole('button', { name: /enable mfa/i }))
    await screen.findByTestId('qr-code')

    await user.click(screen.getByRole('checkbox'))
    await user.type(screen.getByPlaceholderText('000000'), '000000')
    await user.click(screen.getByRole('button', { name: /confirm & enable/i }))

    await screen.findByRole('alert')
    expect(screen.getByRole('alert')).toHaveTextContent(/invalid mfa code/i)

    // Still on the verify step — QR still visible
    expect(screen.getByTestId('qr-code')).toBeInTheDocument()

    // Setup was only called once (no re-rotation)
    expect(setupCallCount).toBe(1)
  })

  it('Cancel during setup clears state and returns to the disabled view', async () => {
    const fetchMock = installFetchMock({
      'POST /api/v1/auth/mfa/setup': () => jsonResponse(mockSetupResponse),
    })
    vi.stubGlobal('fetch', fetchMock)

    const user = userEvent.setup()
    render(
      <AccountSecurityPanel
        token={token}
        currentUser={mfaDisabledUser}
        onLogout={onLogout}
      />,
    )

    await user.click(screen.getByRole('button', { name: /enable mfa/i }))
    await screen.findByTestId('qr-code')

    await user.click(screen.getByRole('button', { name: /cancel/i }))

    expect(screen.getByRole('button', { name: /enable mfa/i })).toBeInTheDocument()
    expect(screen.queryByTestId('qr-code')).not.toBeInTheDocument()
  })

  it('verify step — 401 with authentication required calls onLogout and shows no inline error', async () => {
    const fetchMock = installFetchMock({
      'POST /api/v1/auth/mfa/setup': () => jsonResponse(mockSetupResponse),
      'POST /api/v1/auth/mfa/verify': () =>
        jsonResponse({ error: 'authentication required' }, { status: 401 }),
    })
    vi.stubGlobal('fetch', fetchMock)

    const user = userEvent.setup()
    render(
      <AccountSecurityPanel
        token={token}
        currentUser={mfaDisabledUser}
        onLogout={onLogout}
      />,
    )

    await user.click(screen.getByRole('button', { name: /enable mfa/i }))
    await screen.findByTestId('qr-code')

    await user.click(screen.getByRole('checkbox'))
    await user.type(screen.getByPlaceholderText('000000'), '123456')
    await user.click(screen.getByRole('button', { name: /confirm & enable/i }))

    await waitFor(() => {
      expect(onLogout).toHaveBeenCalledOnce()
    })
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('AccountSecurityPanel — MFA enabled user', () => {
  const token = 'test-token'
  const onLogout = vi.fn()

  beforeEach(() => {
    onLogout.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('shows MFA enabled status and a Disable MFA button', () => {
    vi.stubGlobal('fetch', vi.fn())
    render(
      <AccountSecurityPanel
        token={token}
        currentUser={mfaEnabledUser}
        onLogout={onLogout}
      />,
    )
    expect(screen.getByText(/mfa is enabled/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /disable mfa/i })).toBeInTheDocument()
  })

  it('disable flow requires password — clicking Disable shows password form', async () => {
    vi.stubGlobal('fetch', vi.fn())

    const user = userEvent.setup()
    render(
      <AccountSecurityPanel
        token={token}
        currentUser={mfaEnabledUser}
        onLogout={onLogout}
      />,
    )

    await user.click(screen.getByRole('button', { name: /disable mfa/i }))
    expect(screen.getByPlaceholderText(/enter your password/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /confirm disable/i })).toBeInTheDocument()
  })

  it('successful disable calls DELETE and triggers onMFAChange', async () => {
    const fetchMock = installFetchMock({
      'DELETE /api/v1/auth/mfa': () => noContentResponse(),
    })
    vi.stubGlobal('fetch', fetchMock)

    const onMFAChange = vi.fn()
    const user = userEvent.setup()
    const { rerender } = render(
      <AccountSecurityPanel
        token={token}
        currentUser={mfaEnabledUser}
        onLogout={onLogout}
        onMFAChange={onMFAChange}
      />,
    )

    await user.click(screen.getByRole('button', { name: /disable mfa/i }))
    await user.type(screen.getByPlaceholderText(/enter your password/i), 'my-password')
    await user.click(screen.getByRole('button', { name: /confirm disable/i }))

    await screen.findByText(/mfa disabled/i)
    expect(onMFAChange).toHaveBeenCalledOnce()

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/auth/mfa',
        expect.objectContaining({
          method: 'DELETE',
        }),
      )
    })

    // Simulate onMFAChange → refreshCurrentUser → currentUser prop updates to mfa_enabled: false
    rerender(
      <AccountSecurityPanel
        token={token}
        currentUser={mfaDisabledUser}
        onLogout={onLogout}
        onMFAChange={onMFAChange}
      />,
    )

    // Banner must still be visible after the prop transition (Fix 1)
    expect(screen.getByRole('alert')).toHaveTextContent(/mfa removed/i)
  })

  it('disable with wrong password shows error', async () => {
    const fetchMock = installFetchMock({
      'DELETE /api/v1/auth/mfa': () =>
        jsonResponse({ error: 'invalid credentials' }, { status: 401 }),
    })
    vi.stubGlobal('fetch', fetchMock)

    const user = userEvent.setup()
    render(
      <AccountSecurityPanel
        token={token}
        currentUser={mfaEnabledUser}
        onLogout={onLogout}
      />,
    )

    await user.click(screen.getByRole('button', { name: /disable mfa/i }))
    await user.type(screen.getByPlaceholderText(/enter your password/i), 'wrong')
    await user.click(screen.getByRole('button', { name: /confirm disable/i }))

    await screen.findByRole('alert')
    expect(screen.getByRole('alert')).toHaveTextContent(/invalid credentials/i)
    // Still on the disable form
    expect(screen.getByPlaceholderText(/enter your password/i)).toBeInTheDocument()
  })

  it('disable step — 401 with authentication required calls onLogout and shows no inline error', async () => {
    const fetchMock = installFetchMock({
      'DELETE /api/v1/auth/mfa': () =>
        jsonResponse({ error: 'authentication required' }, { status: 401 }),
    })
    vi.stubGlobal('fetch', fetchMock)

    const user = userEvent.setup()
    render(
      <AccountSecurityPanel
        token={token}
        currentUser={mfaEnabledUser}
        onLogout={onLogout}
      />,
    )

    await user.click(screen.getByRole('button', { name: /disable mfa/i }))
    await user.type(screen.getByPlaceholderText(/enter your password/i), 'somepassword')
    await user.click(screen.getByRole('button', { name: /confirm disable/i }))

    await waitFor(() => {
      expect(onLogout).toHaveBeenCalledOnce()
    })
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('AccountSecurityPanel — null currentUser', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders a loading placeholder when currentUser is null', () => {
    vi.stubGlobal('fetch', vi.fn())
    render(
      <AccountSecurityPanel
        token="tok"
        currentUser={null}
        onLogout={vi.fn()}
      />,
    )
    expect(screen.getByText(/loading account/i)).toBeInTheDocument()
  })
})
