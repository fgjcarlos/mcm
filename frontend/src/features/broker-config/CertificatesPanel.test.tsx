import { render, screen, within } from '@testing-library/react'
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

    const pendingRow = screen.getByText('rev-pending').closest('li') as HTMLElement
    await user.click(within(pendingRow).getByRole('button', { name: 'Activate' }))

    expect(await screen.findByText(/Revision rev-pending activated/)).toBeInTheDocument()
    expect(screen.getByText(/e2e-tls port 8883: verified/)).toBeInTheDocument()
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
