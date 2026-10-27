// Wire types for /api/v1/broker/plugins/* — see internal/server/plugins_handlers.go
// and internal/mosquitto/plugins.

// pluginBackendLabels names the backend that owns the access control of the
// broker in the language the operator reads.
export const pluginBackendLabels: Record<string, string> = {
  none: 'no access control',
  file: 'password and ACL files',
  'dynamic-security': 'dynamic security plugin',
}

// pluginBackendTones gives every backend its own colour: a broker without
// any access control is worth a warning, the two others are states the
// operator chose.
export const pluginBackendTones: Record<string, string> = {
  none: 'bg-amber-400/10 text-amber-200',
  file: 'bg-cyan-400/10 text-cyan-200',
  'dynamic-security': 'bg-emerald-400/10 text-emerald-200',
}

// PluginOptionSpec describes one option MCM writes for a plugin. The name
// carries no `plugin_opt_` prefix, because that is how the plugin receives
// it.
export type PluginOptionSpec = {
  name: string
  description?: string
  required?: boolean
  default?: string
  placeholder?: boolean
}

// PluginSpec describes one plugin the catalog supports for a broker
// version.
export type PluginSpec = {
  id: string
  title: string
  description?: string
  module: string
  modules?: string[]
  since_version?: string
  reload_kind?: string
  options?: PluginOptionSpec[]
}

// Plugin is one plugin configured in the managed configuration file.
//
// The id is empty for a plugin the catalog does not know: MCM preserves such
// a plugin byte-for-byte and never edits it.
export type Plugin = {
  id?: string
  module: string
  /** The options MCM writes, keyed by the option name without the prefix. */
  options?: Record<string, string>
  /** The options the catalog does not know. MCM preserves them untouched. */
  unknown_options?: Record<string, string>
}

// PluginDocument is the desired state of the plugin section of the
// configuration file.
export type PluginDocument = {
  plugins: Plugin[]
}

export type PluginIssue = {
  kind: string
  plugin?: string
  option?: string
  message: string
}

export type PluginsResponse = {
  path: string
  version: string
  catalog: PluginSpec[]
  document: PluginDocument
  backend: string
  warnings?: string[]
}

export type PluginPreviewResult = {
  revision_id: string
  kind?: string
  base_conf_hash?: string
  rendered_conf_hash?: string
  conf_diff?: string
  conf_body?: string
  reload_kind?: string
  requires_restart?: boolean
  changed_directives?: string[]
  has_changes?: boolean
}

export type PluginApplyRequest = {
  revision_id: string
  force?: boolean
  adopt?: boolean
}

export type PluginApplyResult = {
  id?: number
  status?: string
  kind?: string
}
