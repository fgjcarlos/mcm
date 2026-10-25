// Wire types for /api/v1/broker/settings/* — see internal/server/broker_settings_handlers.go
// and internal/mosquitto/settings/settings.go.

export type SettingsEntry = {
  directive: string
  values?: string[]
  unset?: boolean
}

export type SettingsDocument = {
  entries: SettingsEntry[]
}

export type SettingsField = {
  directive: string
  section: string
  type: string
  unit?: string
  min?: number
  max?: number
  default?: string
  multiplicity: string
  reload_kind: string
  scope: string
  allowed_values?: string[]
  duration_suffixes?: string[]
  description: string
  present: boolean
  values?: string[]
}

export type SettingsSection = {
  id: string
  title: string
  hint?: string
  fields: SettingsField[]
}

export type BrokerSettings = {
  path: string
  hash: string
  version: string
  document: SettingsDocument
  sections: SettingsSection[]
  warnings?: string[]
}

export type SettingsIssue = {
  kind: string
  directive: string
  message: string
}

export type SettingsPreview = {
  revision_id: string
  base_conf_hash: string
  rendered_conf_hash: string
  conf_diff: string
  conf_body: string
  reload_kind: string
  // The server omits the flag when it is false, like the broker-config
  // preview of issue #298.
  requires_restart?: boolean
  changed_directives?: string[]
  has_changes: boolean
}

export type SettingsApplyRequest = {
  revision_id: string
  force?: boolean
  adopt?: boolean
}
