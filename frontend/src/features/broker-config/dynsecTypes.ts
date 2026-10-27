// Wire types for /api/v1/broker/dynsec/* — see internal/server/plugins_handlers.go
// and internal/dynsec.

// dynSecBackendLabels names the backend that owns the access control of the
// broker in the language the operator reads.
export const dynSecBackendLabels: Record<string, string> = {
  none: 'no access control',
  file: 'password and ACL files',
  'dynamic-security': 'dynamic security plugin',
}

// dynSecBackendTones gives every backend its own colour.
export const dynSecBackendTones: Record<string, string> = {
  none: 'bg-amber-400/10 text-amber-200',
  file: 'bg-cyan-400/10 text-cyan-200',
  'dynamic-security': 'bg-emerald-400/10 text-emerald-200',
}

// DynSecACLType is the kind of access an access control entry grants or
// denies. The plugin evaluates the events of the MQTT protocol separately.
export type DynSecACLType =
  | 'publishClientSend'
  | 'publishClientReceive'
  | 'subscribeLiteral'
  | 'subscribePattern'
  | 'unsubscribeLiteral'
  | 'unsubscribePattern'

// dynSecACLTypes lists every accepted access type, in the order the plugin
// documents them.
export const dynSecACLTypes: DynSecACLType[] = [
  'publishClientSend',
  'publishClientReceive',
  'subscribeLiteral',
  'subscribePattern',
  'unsubscribeLiteral',
  'unsubscribePattern',
]

export type DynSecACL = {
  acltype: DynSecACLType
  topic: string
  allow: boolean
  priority: number
}

export type DynSecRoleRef = {
  rolename: string
  priority: number
}

export type DynSecGroupRef = {
  groupname: string
  priority: number
}

export type DynSecClientRef = {
  username: string
  priority: number
}

export type DynSecRole = {
  rolename: string
  textname?: string
  textdescription?: string
  acls: DynSecACL[]
}

export type DynSecClient = {
  username: string
  textname?: string
  textdescription?: string
  clientid?: string
  /** Write-only: the server never returns the value, only `password_set`. */
  password?: string
  password_set?: boolean
  roles: DynSecRoleRef[]
  groups: DynSecGroupRef[]
  disabled?: boolean
}

export type DynSecGroup = {
  groupname: string
  textname?: string
  textdescription?: string
  roles: DynSecRoleRef[]
  clients: DynSecClientRef[]
}

export type DynSecDefaultAccess = {
  publish_client_send: boolean
  publish_client_receive: boolean
  subscribe: boolean
  unsubscribe: boolean
}

export type DynSecDocument = {
  clients: DynSecClient[]
  groups: DynSecGroup[]
  roles: DynSecRole[]
  default_access: DynSecDefaultAccess
  anonymous_group?: string
}

export type DynSecGuard = {
  ok: boolean
  management_client?: string
  allowed?: string[]
  violations?: string[]
  reasons?: string[]
}

export type DynSecIssue = {
  kind: string
  object?: string
  field?: string
  message: string
}

export type DynSecCommand = {
  command: string
  username?: string
  password?: string
  clientid?: string
  textname?: string
  textdescription?: string
  groupname?: string
  rolename?: string
  acltype?: string
  topic?: string
  allow?: boolean
  priority?: number
}

export type DynSecChange = {
  kind: string
  name: string
  action: string
  summary: string
  commands?: DynSecCommand[]
}

export type DynSecPlan = {
  changes: DynSecChange[]
}

export type DynSecCredential = {
  username: string
  password: string
  reason?: string
}

export type DynSecPreviewResult = {
  plan_id: string
  kind: string
  plan: DynSecPlan
  guard: DynSecGuard
  backend: string
  has_changes: boolean
  /** The passwords of the clients the migration created. Shown once. */
  credentials?: DynSecCredential[]
  config_revision_id?: string
  config_diff?: string
  warnings?: string[]
}

export type DynSecApplyRequest = {
  plan_id: string
  document?: DynSecDocument
}

export type DynSecApplyResult = {
  plan_id: string
  document: DynSecDocument
  guard: DynSecGuard
  deployment?: {
    id?: number | string
    status?: string
  }
}

export type DynSecState = {
  backend: string
  enabled: boolean
  management_client?: string
  document: DynSecDocument
  guard: DynSecGuard
  warnings?: string[]
}
