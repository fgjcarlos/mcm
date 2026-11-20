import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import CertificatesPanel from './CertificatesPanel'

type RouteHandler = (init?: RequestInit) => Response | Promise<Response>

function jsonResponse(body: unknown, init?: ResponseInit) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
}

function installFetchMock(routes: Record<string, RouteHandler>) {
  return vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const requestUrl = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
    const handler = routes[`${method.toUpperCase()} ${requestUrl}`]
    if (!handler) throw new Error(`Unhandled fetch: ${method} ${requestUrl}`)
    return await handler(init)
  })
}

const profile = {
  name: 'production',
  description: 'broker certificate',
  active_revision: 'rev-active',
  expiry_status: 'valid',
  revisions: [
    {
      revision_id: 'rev-active',
      state: 'active',
      subject: 'CN=mosquitto',
      fingerprint_sha256: 'aabbcc',
      expiry_status: 'valid',
    },
    {
      revision_id: 'rev-pending',
      state: 'pending',
      subject: 'CN=mosquitto',
      fingerprint_sha256: 'ddeeff',
      expiry_status: 'valid',
    },
  ],
}

function baseRoutes(): Record<string, RouteHandler> {
  return {
    'GET /api/v1/broker/certificates': () => jsonResponse({ profiles: [profile] }),
  }
}

const stepUpRoute = {
  'POST /api/v1/auth/mfa/step-up': () => jsonResponse({ proof: 'recent-proof', expires_at: new Date(Date.now() + 300000).toISOString() }),
}

async function confirmMFA(user: { type: typeof userEvent.type; click: typeof userEvent.click } = userEvent) {
  await user.type(screen.getByLabelText('MFA code'), '123456')
  await user.click(screen.getByRole('button', { name: 'Verify MFA' }))
  await screen.findByText(/MFA confirmed/)
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('CertificatesPanel', () => {
  it('lists the certificate profiles with their active revision and expiry', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes()))

    render(<CertificatesPanel token="tok" onLogout={() => {}} role="auditor" />)

    expect(await screen.findByText('production')).toBeInTheDocument()
    expect(screen.getByText(/active revision: rev-active/)).toBeInTheDocument()
    expect(screen.getByText('rev-pending')).toBeInTheDocument()
  })

  it('uploads the certificate material as PEM text and never sends the key twice', async () => {
    const uploadBodies: string[] = []
    vi.stubGlobal('fetch', installFetchMock({
      ...baseRoutes(),
      'POST /api/v1/broker/certificates/profiles/production/revisions': (init) => {
        uploadBodies.push(String(init?.body))
        return jsonResponse({ revision_id: 'rev-new', state: 'pending' }, { status: 201 })
      },
    }))

    const user = userEvent.setup()
    render(<CertificatesPanel token="tok" onLogout={() => {}} role="operator" />)
    await screen.findByText('production')

    await user.type(screen.getByLabelText('Profile name'), 'production')
    await user.upload(
      screen.getByLabelText('Server certificate'),
      new File(['-----BEGIN CERTIFICATE-----server-----END CERTIFICATE-----'], 'server.crt', { type: 'application/x-pem-file' }),
    )
    await user.upload(
      screen.getByLabelText('Server private key'),
      new File(['-----BEGIN PRIVATE KEY-----secret-----END PRIVATE KEY-----'], 'server.key', { type: 'application/x-pem-file' }),
    )
    await user.type(screen.getByLabelText('Expected names'), 'mosquitto.example.internal')
    await user.click(screen.getByRole('button', { name: 'Upload revision' }))

    expect(await screen.findByText(/Revision rev-new stored/)).toBeInTheDocument()
    expect(uploadBodies).toHaveLength(1)
    const body = JSON.parse(uploadBodies[0]) as Record<string, unknown>
    expect(body.server_cert_pem).toContain('BEGIN CERTIFICATE')
    expect(body.server_key_pem).toContain('BEGIN PRIVATE KEY')
    expect(body.expected_names).toEqual(['mosquitto.example.internal'])
    expect(body.ca_cert_pem).toBe('')
  })

  it('activates a revision and reports the broker verification', async () => {
    vi.stubGlobal('fetch', installFetchMock({
      ...baseRoutes(),
      ...stepUpRoute,
      'POST /api/v1/broker/certificates/apply': () => jsonResponse({
        profile: 'production',
        revision_id: 'rev-pending',
        previous_revision: 'rev-active',
        restarted: true,
        recovered: false,
        verification: [{ listener_id: 'e2e-tls', port: 8883, status: 'verified', reason: 'the broker served the expected certificate' }],
      }),
    }))

    const user = userEvent.setup()
    render(<CertificatesPanel token="tok" onLogout={() => {}} role="admin" />)
    await screen.findByText('production')

    await confirmMFA(user)
    const pendingRow = screen.getByText('rev-pending').closest('li') as HTMLElement
    await user.click(within(pendingRow).getByRole('button', { name: 'Activate' }))

    expect(await screen.findByText(/Revision rev-pending activated/)).toBeInTheDocument()
    expect(screen.getByText(/e2e-tls port 8883: verified/)).toBeInTheDocument()
  })

  it('sends the recent MFA proof with the activation and blocks Activate until it exists', async () => {
    const applyHeaders: HeadersInit[] = []
    vi.stubGlobal('fetch', installFetchMock({
      ...baseRoutes(),
      ...stepUpRoute,
      'POST /api/v1/broker/certificates/apply': (init) => {
        applyHeaders.push(init?.headers ?? {})
        return jsonResponse({ profile: 'production', revision_id: 'rev-pending', previous_revision: 'rev-active', restarted: true, recovered: false, verification: [] })
      },
    }))
    const user = userEvent.setup()
    render(<CertificatesPanel token="tok" onLogout={() => {}} role="admin" />)
    await screen.findByText('production')

    const pendingRow = screen.getByText('rev-pending').closest('li') as HTMLElement
    expect(within(pendingRow).getByRole('button', { name: 'Activate' })).toBeDisabled()
    expect(screen.getByText('Confirm MFA above before applying.')).toBeVisible()
    await confirmMFA(user)
    await user.click(within(pendingRow).getByRole('button', { name: 'Activate' }))

    await waitFor(() => expect(applyHeaders).toHaveLength(1))
    expect(applyHeaders[0]).toEqual(expect.objectContaining({ 'X-MCM-MFA': 'recent-proof' }))
  })

  it('keeps the activation out of reach for roles below admin', async () => {
    vi.stubGlobal('fetch', installFetchMock(baseRoutes()))

    render(<CertificatesPanel token="tok" onLogout={() => {}} role="operator" />)
    await screen.findByText('production')

    const activateButtons = screen.getAllByRole('button', { name: 'Activate' })
    expect(activateButtons.length).toBeGreaterThan(0)
    for (const button of activateButtons) {
      expect(button).toBeDisabled()
    }
  })

  it('pages and filters the certificate profiles by name', async () => {
    const profiles = Array.from({ length: 12 }, (_, i) => ({ name: `profile-${String(i + 1).padStart(2, '0')}`, active_revision: '', expiry_status: 'valid', revisions: [] }))
    vi.stubGlobal('fetch', installFetchMock({ 'GET /api/v1/broker/certificates': () => jsonResponse({ profiles }) }))
    const user = userEvent.setup()
    render(<CertificatesPanel token="tok" onLogout={() => {}} role="auditor" />)

    expect(await screen.findByText('profile-10')).toBeInTheDocument()
    expect(screen.getByText('profile-01')).toBeInTheDocument()
    expect(screen.queryByText('profile-11')).not.toBeInTheDocument()
    const pages = screen.getByRole('navigation', { name: 'certificate profiles pages' })
    expect(pages).toHaveTextContent('Page 1 of 2')
    await user.click(within(pages).getByRole('button', { name: 'Next page' }))
    expect(screen.getByText('profile-12')).toBeInTheDocument()
    expect(screen.queryByText('profile-01')).not.toBeInTheDocument()
    await user.type(screen.getByLabelText('Filter certificate profiles by name'), 'PROFILE-03')
    expect(screen.getByText('profile-03')).toBeInTheDocument()
    expect(screen.queryByText('profile-12')).not.toBeInTheDocument()
  })

  it('pages the revisions of each profile five at a time', async () => {
    const revisions = Array.from({ length: 7 }, (_, i) => ({ revision_id: `rev-${i + 1}`, state: 'pending', expiry_status: 'valid' }))
    vi.stubGlobal('fetch', installFetchMock({ 'GET /api/v1/broker/certificates': () => jsonResponse({ profiles: [{ ...profile, revisions }] }) }))
    const user = userEvent.setup()
    render(<CertificatesPanel token="tok" onLogout={() => {}} role="auditor" />)

    expect(await screen.findByText('rev-5')).toBeInTheDocument()
    expect(screen.getByText('rev-1')).toBeInTheDocument()
    expect(screen.queryByText('rev-6')).not.toBeInTheDocument()
    const pages = screen.getByRole('navigation', { name: 'production revisions pages' })
    expect(pages).toHaveTextContent('Page 1 of 2')
    await user.click(within(pages).getByRole('button', { name: 'Next page' }))
    expect(screen.getByText('rev-6')).toBeInTheDocument()
    expect(screen.getByText('rev-7')).toBeInTheDocument()
    expect(screen.queryByText('rev-1')).not.toBeInTheDocument()
  })

  it('reports the certificate surface as disabled when the API answers 503', async () => {
    vi.stubGlobal('fetch', installFetchMock({
      'GET /api/v1/broker/certificates': () => jsonResponse({ error: 'the certificate surface is disabled' }, { status: 503 }),
    }))

    render(<CertificatesPanel token="tok" onLogout={() => {}} role="admin" />)

    expect(await screen.findByText(/The certificate surface is disabled/)).toBeInTheDocument()
  })

  it('previews a revision before it is activated', async () => {
    vi.stubGlobal('fetch', installFetchMock({
      ...baseRoutes(),
      'POST /api/v1/broker/certificates/preview': () => jsonResponse({
        profile: 'production',
        revision_id: 'rev-pending',
        current_revision: 'rev-active',
        restart_required: true,
        files: {
          cert_path: '/mosquitto/config/certs/production/active/server.crt',
          key_path: '/mosquitto/config/certs/production/active/server.key',
          ca_path: '/mosquitto/config/certs/production/active/ca.crt',
        },
      }),
    }))

    const user = userEvent.setup()
    render(<CertificatesPanel token="tok" onLogout={() => {}} role="operator" />)
    await screen.findByText('production')

    const pendingRow = screen.getByText('rev-pending').closest('li') as HTMLElement
    await user.click(within(pendingRow).getByRole('button', { name: 'Preview' }))

    expect(await screen.findByText('production · rev-pending')).toBeInTheDocument()
    expect(screen.getByText(/certificate: \/mosquitto\/config\/certs/)).toBeInTheDocument()
    expect(screen.getByText(/restart required: yes/)).toBeInTheDocument()
  })
})
