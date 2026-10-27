import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import PluginsPanel from './PluginsPanel'

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

const catalogEntry = {
  id: 'dynamic-security',
  title: 'Dynamic Security',
  description: 'Keeps the security database of the broker.',
  module: '/usr/lib/mosquitto_dynamic_security.so',
  modules: ['/usr/lib/mosquitto_dynamic_security.so'],
  since_version: '2.0',
  reload_kind: 'restart',
  options: [
    {
      name: 'config_file',
      description: 'The database the plugin stores its state in.',
      required: true,
      default: '/mosquitto/config/dynamic-security.json',
      placeholder: true,
    },
  ],
}

// The second plugin is one the catalog does not know: MCM preserves it
// byte-for-byte and never edits it.
const storedPlugins = [
  {
    id: 'dynamic-security',
    module: '/usr/lib/mosquitto_dynamic_security.so',
    options: { config_file: '/mosquitto/config/dynamic-security.json' },
    unknown_options: { something_unknown: 'keep-me' },
  },
  {
    module: '/usr/lib/other_plugin.so',
    unknown_options: { whatever: '1' },
  },
]

function pluginsResponse(plugins: unknown[]) {
  return {
    path: '/var/lib/mosquitto-config/mosquitto.conf',
    version: '2.0',
    catalog: [catalogEntry],
    document: { plugins },
    backend: 'none',
    warnings: [],
  }
}

describe('PluginsPanel', () => {
  const originalFetch = globalThis.fetch

  beforeEach(() => {
    vi.restoreAllMocks()
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
  })

  it('shows the catalog and the plugins the broker loads', async () => {
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/plugins': () => jsonResponse(pluginsResponse(storedPlugins)),
    }) as unknown as typeof fetch

    render(<PluginsPanel token="token" onLogout={() => {}} role="auditor" />)

    await waitFor(() => expect(screen.getByLabelText('Module dynamic-security')).toHaveValue('/usr/lib/mosquitto_dynamic_security.so'))
    expect(screen.getAllByText('Dynamic Security').length).toBeGreaterThan(0)
    expect(screen.getByText(/plugin_opt_config_file/)).toBeInTheDocument()
    expect(screen.getByText('no access control')).toBeInTheDocument()
  })

  it('shows an unknown plugin read-only and keeps its options', async () => {
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/plugins': () => jsonResponse(pluginsResponse(storedPlugins)),
    }) as unknown as typeof fetch

    render(<PluginsPanel token="token" onLogout={() => {}} role="auditor" />)

    await waitFor(() => expect(screen.getByLabelText('Module dynamic-security')).toBeInTheDocument())
    expect(screen.getByText(/MCM does not know this plugin/)).toBeInTheDocument()
    expect(screen.getByText(/plugin_opt_whatever/)).toBeInTheDocument()
    // The unknown plugin has no editable field.
    expect(screen.queryByLabelText('Module /usr/lib/other_plugin.so')).not.toBeInTheDocument()
    // The option the catalog does not know is preserved and read-only.
    expect(screen.getByText(/plugin_opt_something_unknown/)).toBeInTheDocument()
    expect(screen.queryByLabelText('Option something_unknown dynamic-security')).not.toBeInTheDocument()
  })

  it('refuses the preview to an auditor', async () => {
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/plugins': () => jsonResponse(pluginsResponse(storedPlugins)),
    }) as unknown as typeof fetch

    render(<PluginsPanel token="token" onLogout={() => {}} role="auditor" />)

    await waitFor(() => expect(screen.getByLabelText('Module dynamic-security')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Preview' })).toBeDisabled()
  })

  it('enables a plugin of the catalog', async () => {
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/plugins': () => jsonResponse(pluginsResponse([])),
    }) as unknown as typeof fetch

    render(<PluginsPanel token="token" onLogout={() => {}} role="operator" />)

    await waitFor(() => expect(screen.getByText(/No plugin is configured/)).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Enable Dynamic Security' }))

    expect(await screen.findByLabelText('Module dynamic-security')).toHaveValue('/usr/lib/mosquitto_dynamic_security.so')
    expect(screen.getByLabelText('Option config_file dynamic-security')).toHaveValue('/mosquitto/config/dynamic-security.json')
    expect(screen.getByText(/Unsaved changes/)).toBeInTheDocument()
  })

  it('previews the edited plugins and shows the diff', async () => {
    const previewBody = {
      revision_id: 'revision-1',
      kind: 'plugins',
      reload_kind: 'restart',
      requires_restart: true,
      conf_diff: '+plugin /usr/lib/mosquitto_dynamic_security.so',
      changed_directives: ['plugin'],
    }
    let previewRequest: { plugins?: unknown[] } | null = null
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/plugins': () => jsonResponse(pluginsResponse(storedPlugins)),
      'POST /api/v1/broker/plugins/preview': (init) => {
        previewRequest = JSON.parse(String(init?.body ?? '{}'))
        return jsonResponse(previewBody)
      },
    }) as unknown as typeof fetch

    render(<PluginsPanel token="token" onLogout={() => {}} role="operator" />)

    await waitFor(() => expect(screen.getByLabelText('Module dynamic-security')).toBeInTheDocument())
    const module = screen.getByLabelText('Module dynamic-security')
    await userEvent.clear(module)
    await userEvent.type(module, '/usr/lib/other/mosquitto_dynamic_security.so')
    await userEvent.click(screen.getByRole('button', { name: 'Preview' }))

    expect(await screen.findByText(/revision revision-1/)).toBeInTheDocument()
    expect(screen.getByText(/Mosquitto loads a plugin at start-up/)).toBeInTheDocument()
    expect(previewRequest).not.toBeNull()
    const sent = (previewRequest as unknown as { plugins: Array<{ module: string }> }).plugins
    expect(sent[0].module).toBe('/usr/lib/other/mosquitto_dynamic_security.so')
  })

  it('applies the reviewed revision with a restart confirmation', async () => {
    let applyRequest: { revision_id?: string; force?: boolean; adopt?: boolean } | null = null
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/plugins': () => jsonResponse(pluginsResponse(storedPlugins)),
      'POST /api/v1/broker/plugins/preview': () =>
        jsonResponse({
          revision_id: 'revision-1',
          kind: 'plugins',
          reload_kind: 'restart',
          requires_restart: true,
          conf_diff: '+plugin /usr/lib/mosquitto_dynamic_security.so',
        }),
      'POST /api/v1/broker/plugins/apply': (init) => {
        applyRequest = JSON.parse(String(init?.body ?? '{}'))
        return jsonResponse({ id: 9, status: 'active_verified', kind: 'plugins' })
      },
    }) as unknown as typeof fetch

    render(<PluginsPanel token="token" onLogout={() => {}} role="admin" />)

    await waitFor(() => expect(screen.getByLabelText('Module dynamic-security')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Preview' }))

    const applyButton = await screen.findByRole('button', { name: 'Apply' })
    expect(applyButton).toBeDisabled()

    await userEvent.click(screen.getByLabelText('Confirm broker restart'))
    expect(applyButton).toBeEnabled()
    await userEvent.click(applyButton)

    await waitFor(() => expect(applyRequest).not.toBeNull())
    expect((applyRequest as unknown as { revision_id: string }).revision_id).toBe('revision-1')
    expect((applyRequest as unknown as { force: boolean }).force).toBe(true)
    expect((applyRequest as unknown as { adopt: boolean }).adopt).toBe(true)
    expect(await screen.findByText(/Plugins applied/)).toBeInTheDocument()
  })

  it('shows the findings of a refused preview', async () => {
    globalThis.fetch = installFetchMock({
      'GET /api/v1/broker/plugins': () => jsonResponse(pluginsResponse(storedPlugins)),
      'POST /api/v1/broker/plugins/preview': () =>
        jsonResponse(
          {
            error: 'plugin configuration is invalid',
            issues: [{ kind: 'missing_option', plugin: 'dynamic-security', option: 'config_file', message: 'the plugin "dynamic-security" needs the option "plugin_opt_config_file"' }],
          },
          400,
        ),
    }) as unknown as typeof fetch

    render(<PluginsPanel token="token" onLogout={() => {}} role="operator" />)

    await waitFor(() => expect(screen.getByLabelText('Module dynamic-security')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Preview' }))

    expect(await screen.findByText(/plugin configuration is invalid/)).toBeInTheDocument()
    expect(screen.getByText(/needs the option "plugin_opt_config_file"/)).toBeInTheDocument()
  })
})
