import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import SettingsPanel from './SettingsPanel'

type RouteHandler = (init?: RequestInit) => Response | Promise<Response>

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
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

const persistenceField = {
  directive: 'persistence',
  section: 'persistence',
  type: 'boolean',
  multiplicity: 'once',
  reload_kind: 'reload',
  scope: 'global',
  description: 'Enable the persistent store.',
  default: 'false',
  present: true,
  values: ['true'],
}

const autosaveField = {
  directive: 'autosave_interval',
  section: 'persistence',
  type: 'integer',
  unit: 'seconds',
  min: 0,
  max: 2147483647,
  default: '1800',
  multiplicity: 'once',
  reload_kind: 'reload',
  scope: 'global',
  description: 'How often the broker writes the persistence database.',
  present: false,
}

const logTypeField = {
  directive: 'log_type',
  section: 'logging',
  type: 'enum',
  allowed_values: ['error', 'warning', 'notice', 'information'],
  multiplicity: 'many',
  reload_kind: 'reload',
  scope: 'global',
  description: 'Categories of messages the broker logs.',
  present: true,
  values: ['error'],
}

const logDestField = {
  directive: 'log_dest',
  section: 'logging',
  type: 'enum',
  allowed_values: ['stdout', 'stderr', 'file'],
  multiplicity: 'many',
  reload_kind: 'reload',
  scope: 'global',
  description: 'Destination of log messages.',
  present: true,
  values: ['stdout'],
}

const maxConnectionsField = {
  directive: 'max_connections',
  section: 'sessions',
  type: 'integer',
  unit: 'connections',
  min: -1,
  max: 2147483647,
  default: '-1',
  multiplicity: 'once_per_block',
  reload_kind: 'restart',
  scope: 'any',
  description: 'Maximum number of concurrent client connections.',
  present: false,
}

const settingsResponse = {
  path: '/mosquitto/config/mosquitto.conf',
  hash: 'abc123',
  version: '2.0',
  document: {
    entries: [
      { directive: 'persistence', values: ['true'] },
      { directive: 'autosave_interval', unset: true },
      { directive: 'log_type', values: ['error'] },
      { directive: 'log_dest', values: ['stdout'] },
      { directive: 'max_connections', unset: true },
    ],
  },
  sections: [
    {
      id: 'persistence',
      title: 'Persistence and autosave',
      hint: 'Where the broker keeps its state.',
      fields: [persistenceField, autosaveField],
    },
    {
      id: 'sessions',
      title: 'Connections and sessions',
      fields: [maxConnectionsField],
    },
    {
      id: 'logging',
      title: 'Logging',
      fields: [logTypeField, logDestField],
    },
  ],
  warnings: [],
}

const preview = {
  revision_id: 'revision-123',
  base_conf_hash: 'abc123',
  rendered_conf_hash: 'def456',
  conf_diff: '-persistence true\n+persistence false\n',
  conf_body: 'persistence false\n',
  reload_kind: 'reload',
  requires_restart: false,
  changed_directives: ['persistence'],
  has_changes: true,
}

function renderPanel(routes: Record<string, RouteHandler> = {}) {
  vi.stubGlobal('fetch', installFetchMock({
    'GET /api/v1/broker/settings': () => jsonResponse(settingsResponse),
    'POST /api/v1/broker/settings/preview': () => jsonResponse(preview),
    ...routes,
  }))
  return render(<SettingsPanel token="tok" onLogout={() => {}} role="admin" />)
}

afterEach(() => vi.unstubAllGlobals())

describe('SettingsPanel', () => {
  it('renders the catalog sections with their units, bounds and help text', async () => {
    renderPanel()
    await screen.findByText('Persistence and autosave')

    expect(screen.getByText('Connections and sessions')).toBeInTheDocument()
    expect(screen.getByText('Logging')).toBeInTheDocument()
    expect(screen.getByText('Enable the persistent store.')).toBeInTheDocument()
    expect(screen.getByText('How often the broker writes the persistence database.')).toBeInTheDocument()
    // The bounds and the default come from the catalog.
    expect(screen.getByText('0 – 2147483647 seconds')).toBeInTheDocument()
    expect(screen.getAllByText(/default/).length).toBeGreaterThan(0)
  })

  it('marks the directives that need a restart', async () => {
    renderPanel()
    await screen.findByText('Connections and sessions')
    const badges = screen.getAllByText('restart')
    expect(badges.length).toBeGreaterThan(0)
    expect(screen.getAllByText('reload').length).toBeGreaterThan(0)
  })

  it('shows the current value and the broker default for a directive that is not set', async () => {
    renderPanel()
    await screen.findByText('Persistence and autosave')
    expect(screen.getAllByText(/the broker default applies/).length).toBeGreaterThan(0)
    expect(screen.getByLabelText('Set autosave_interval')).not.toBeChecked()

    // Switching the directive on reveals the editor with the bounds of
    // the broker.
    const user = userEvent.setup()
    await user.click(screen.getByLabelText('Set autosave_interval'))
    const autosave = screen.getByLabelText('autosave_interval value') as HTMLInputElement
    expect(autosave.value).toBe('1800')
    expect(autosave.min).toBe('0')
    expect(autosave.max).toBe('2147483647')
  })

  it('sends the edited document to the preview endpoint', async () => {
    const bodies: string[] = []
    const user = userEvent.setup()
    renderPanel({
      'POST /api/v1/broker/settings/preview': (init) => {
        bodies.push(String(init?.body))
        return jsonResponse(preview)
      },
    })
    await screen.findByText('Persistence and autosave')

    const persistence = await screen.findByLabelText('persistence value')
    await user.selectOptions(persistence, 'false')
    expect(screen.getByText('Unsaved changes')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Preview changes' }))
    await screen.findByText(/revision-123/)

    const body = JSON.parse(bodies[0]) as { entries: Array<{ directive: string; values?: string[]; unset?: boolean }> }
    const persistenceEntry = body.entries.find((entry) => entry.directive === 'persistence')
    expect(persistenceEntry?.values).toEqual(['false'])
    // A directive the operator did not touch is sent as unset so the
    // broker default keeps applying.
    const autosaveEntry = body.entries.find((entry) => entry.directive === 'autosave_interval')
    expect(autosaveEntry?.unset).toBe(true)
  })

  it('adds and removes a repeatable directive', async () => {
    const bodies: string[] = []
    const user = userEvent.setup()
    renderPanel({
      'POST /api/v1/broker/settings/preview': (init) => {
        bodies.push(String(init?.body))
        return jsonResponse(preview)
      },
    })
    await screen.findByText('Logging')

    expect(screen.getAllByLabelText('log_type value')).toHaveLength(1)
    await user.click(screen.getByRole('button', { name: 'Add log_type' }))
    expect(screen.getAllByLabelText('log_type value')).toHaveLength(2)

    await user.click(screen.getByRole('button', { name: 'Remove log_type 1' }))
    expect(screen.getAllByLabelText('log_type value')).toHaveLength(1)
  })

  it('asks for the log file path when the file destination is chosen', async () => {
    const user = userEvent.setup()
    renderPanel()
    await screen.findByText('Logging')

    const destination = await screen.findByLabelText('log_dest value')
    await user.selectOptions(destination, 'file')
    const path = await screen.findByLabelText('log_dest path')
    expect(path).toHaveValue('')
  })

  it('requires a restart confirmation before applying a restart-required change', async () => {
    const user = userEvent.setup()
    renderPanel({
      'POST /api/v1/broker/settings/preview': () => jsonResponse({ ...preview, reload_kind: 'restart', requires_restart: true }),
    })
    await screen.findByText('Connections and sessions')

    await user.click(screen.getByRole('button', { name: 'Preview changes' }))
    await screen.findByText(/revision-123/)

    const applyButton = screen.getByRole('button', { name: 'Apply' })
    expect(applyButton).toBeDisabled()
    await user.click(screen.getByLabelText('Confirm broker restart'))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Apply' })).toBeEnabled())
  })

  it('surfaces the issues the server rejects the document with', async () => {
    const user = userEvent.setup()
    renderPanel({
      'POST /api/v1/broker/settings/preview': () => jsonResponse({
        error: 'broker settings are invalid',
        issues: [{ kind: 'range', directive: 'max_inflight_messages', message: '"max_inflight_messages" must be 65535 messages or less' }],
      }, 400),
    })
    await screen.findByText('Persistence and autosave')

    await user.click(screen.getByRole('button', { name: 'Preview changes' }))
    await screen.findByText('broker settings are invalid')
    expect(screen.getByText(/must be 65535 messages or less/)).toBeInTheDocument()
  })

  it('keeps Apply disabled for a role that cannot apply', async () => {
    const user = userEvent.setup()
    vi.stubGlobal('fetch', installFetchMock({
      'GET /api/v1/broker/settings': () => jsonResponse(settingsResponse),
      'POST /api/v1/broker/settings/preview': () => jsonResponse(preview),
    }))
    render(<SettingsPanel token="tok" onLogout={() => {}} role="operator" />)
    await screen.findByText('Persistence and autosave')

    await user.click(screen.getByRole('button', { name: 'Preview changes' }))
    await screen.findByText(/revision-123/)
    expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled()
  })

  it('keeps Preview disabled for a role that cannot preview', async () => {
    vi.stubGlobal('fetch', installFetchMock({
      'GET /api/v1/broker/settings': () => jsonResponse(settingsResponse),
    }))
    render(<SettingsPanel token="tok" onLogout={() => {}} role="auditor" />)
    await screen.findByText('Persistence and autosave')

    expect(screen.getByRole('button', { name: 'Preview changes' })).toBeDisabled()
  })

  it('reports that the broker settings are not configured', async () => {
    vi.stubGlobal('fetch', installFetchMock({
      'GET /api/v1/broker/settings': () => jsonResponse({ error: 'not configured' }, 503),
    }))
    render(<SettingsPanel token="tok" onLogout={() => {}} role="admin" />)
    expect(await screen.findByText('Broker settings are not configured on this MCM instance.')).toBeInTheDocument()
  })
})
