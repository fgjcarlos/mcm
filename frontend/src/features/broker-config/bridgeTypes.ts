// Wire types for /api/v1/broker/bridges/* — see internal/server/bridges_handlers.go
// and internal/mosquitto/bridges.

export type BridgeDirection = 'in' | 'out' | 'both'

export type BridgeTopicRule = {
  topic: string
  direction?: BridgeDirection
  qos: number
  local_prefix?: string
  remote_prefix?: string
}

export type BridgeTLSOptions = {
  ca_file?: string
  ca_path?: string
  cert_file?: string
  key_file?: string
  insecure?: boolean
  tls_version?: string
  alpn?: string
  psk?: string
  psk_set?: boolean
  identity?: string
  require_ocsp?: boolean
  outgoing_retain?: boolean
  max_packet_size?: number
}

export type Bridge = {
  name: string
  address: string
  remote_client_id?: string
  local_client_id?: string
  username?: string
  /** Write-only: the server never returns the value, only `password_set`. */
  password?: string
  password_set?: boolean
  local_username?: string
  /** Write-only: the server never returns the value, only `local_password_set`. */
  local_password?: string
  local_password_set?: boolean
  protocol_version?: string
  clean_session?: boolean
  local_clean_session?: boolean
  keepalive_seconds?: number
  restart_timeout?: string
  notifications?: boolean
  notifications_local_only?: boolean
  notification_topic?: string
  try_private?: boolean
  start_type?: string
  threshold?: number
  idle_timeout_seconds?: number
  round_robin?: boolean
  tls?: BridgeTLSOptions
  topics: BridgeTopicRule[]
}

export type BridgeListResponse = {
  bridges?: Bridge[]
}

export type BridgeIssue = {
  kind: string
  severity?: string
  bridge?: string
  field?: string
  topic?: string
  message: string
}

export type BridgePreviewResult = {
  revision_id: string
  kind?: string
  base_conf_hash?: string
  rendered_conf_hash?: string
  conf_diff?: string
  conf_body?: string
  reload_kind?: string
  requires_restart?: boolean
  changed_directives?: string[]
  bridge_issues?: BridgeIssue[]
  has_changes?: boolean
}

export type BridgeApplyRequest = {
  revision_id: string
  force?: boolean
  adopt?: boolean
}

export type BridgeApplyResult = {
  id?: number
  status?: string
  kind?: string
}
