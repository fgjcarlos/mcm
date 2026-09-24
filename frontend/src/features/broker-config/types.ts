// Wire types for /api/v1/broker/config/* — see internal/server/broker_config_handlers.go.

export type BrokerConfig = {
  path: string
  body: string
  hash: string
}

export type ValidationIssue = {
  kind: 'unknown' | 'type' | 'scope' | 'multiplicity' | 'dependency' | 'since_version' | string
  line: number
  directive: string
  message: string
}

export type BrokerConfigPreview = {
  revision_id: string
  base_conf_hash: string
  rendered_conf_hash: string
  conf_diff: string
  conf_body: string
  reload_kind: string
  requires_restart: boolean
  validation_issues: ValidationIssue[]
}

export type BrokerConfigAdoption = {
  id: number | string
  source_path: string
  adopted_by: string
  adopted_at: string
}

export type BrokerConfigApplyRequest = {
  revision_id: string
  force?: boolean
  adopt?: boolean
}

export type DirectiveSpec = {
  name: string
  type: string
  scope: string
  multiplicity: string
  reload_kind: string
  since_version: string
  description: string
  default?: unknown
}

export type CatalogResponse = {
  version: string
  directives: DirectiveSpec[]
}
