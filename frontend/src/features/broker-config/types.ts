export interface ListenerSpec {
  id: string
  port: number
  bind: string
  protocols: string[]
}

export interface ListenerComposeStatus {
  compose_path?: string
  mapped_ports?: number[]
  unmapped_ports?: number[]
  disabled: boolean
}

export interface ListenerPreviewResult {
  revision_id: string
  base_hash?: string
  rendered_hash?: string
  needs_restart: boolean
  diff: string
  rendered: string
  issues?: Array<{ kind: string; listener_id?: string; directive?: string; message: string }>
  warnings?: Array<{ kind: string; listener_id?: string; key?: string; message: string }>
  compose_status?: ListenerComposeStatus
  created_at?: string
}

export interface ListenerApplyRequest {
  revision_id: string
  confirm: boolean
}

export interface ListenerApplyResult {
  revision_id: string
  applied: boolean
}
