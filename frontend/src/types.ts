export type SparkplugMetadata = {
  namespace: string
  group_id: string
  message_type: string
  edge_node_id: string
  device_id?: string
}

export type PayloadInspection = {
  detected_type: 'json_object' | 'json_array' | 'json_scalar' | 'text' | 'binary' | string
  byte_length: number
  truncated: boolean
  json_valid: boolean
  json_top_level_keys?: string[]
  json_element_count?: number
  json_scalar_summary?: string
}

export type SchemaValidation = {
  schema_id: number
  schema_name: string
  topic_filter: string
  valid: boolean
  errors?: string[]
}

export type BrokerEvent = {
  type: 'broker_status' | 'topic_message' | 'broker_log'
  status?: 'connected' | 'disconnected'
  topic?: string
  payload_preview?: string
  payload_format?: 'json' | 'text' | 'binary'
  payload_bytes?: number
  truncated?: boolean
  payload_inspection?: PayloadInspection
  schema_validation?: SchemaValidation
  sparkplug?: SparkplugMetadata
  source?: string
  severity?: 'debug' | 'info' | 'warning' | 'error'
  message?: string
  observed_at: string
}

export type TopicMessage = BrokerEvent & { type: 'topic_message'; topic: string }
export type BrokerLog = BrokerEvent & { type: 'broker_log'; source: string; severity: 'debug' | 'info' | 'warning' | 'error'; message: string }

export type AuditEvent = {
  id: number
  occurred_at: string
  actor: string
  action: string
  resource_type: string
  resource_id?: string
  result: 'success' | 'failure' | string
  metadata?: Record<string, unknown>
}

export type SecurityEvent = {
  id: number
  category: string
  reason: string
  username?: string
  source_ip?: string
  method?: string
  path?: string
  observed_at: string
}

export type ACLRule = {
  id: string
  principal: string
  topic_filter: string
  permission: 'read' | 'write' | 'readwrite'
  description?: string
}

export type MQTTUser = {
  id: number
  username: string
  disabled: boolean
  created_at: string
  updated_at: string
}

export type Deployment = {
  id: string
  status: 'applied' | 'rolled_back' | 'rollback_failed' | string
  message?: string
  created_at: string
}

export type DeployPreview = {
  acl_diff: string
  passwd_diff: string
  has_changes: boolean
}

export type BrokerTrafficItem = {
  name: string
  count: number
  percentage: number
}

export type BrokerRatePoint = {
  timestamp: string
  count: number
}

export type BrokerTrafficMetrics = {
  window_seconds: number
  message_count: number
  message_rate_per_minute: number
  rate_points: BrokerRatePoint[]
  top_topics: BrokerTrafficItem[]
  top_clients: BrokerTrafficItem[]
  top_clients_available: boolean
  top_clients_note: string
  persistence: string
}

export type StatusResponse = {
  broker: {
    status: 'connected' | 'disconnected'
    metrics: {
      traffic: BrokerTrafficMetrics
    }
  }
}

export type AdminUser = {
  id: number
  username: string
  disabled: boolean
  role: 'viewer' | 'auditor' | 'operator' | 'admin' | string
  mfa_enabled?: boolean
  created_at: string
  updated_at: string
}

export type LoginResponse = {
  token?: string
  expires_at?: string
  user?: AdminUser
  mfa_required?: boolean
  mfa_challenge?: string
}

export type NavItem = {
  id: string
  label: string
  eyebrow: string
  title: string
  description: string
}
