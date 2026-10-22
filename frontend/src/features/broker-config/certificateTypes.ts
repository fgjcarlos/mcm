// Wire types for /api/v1/broker/certificates — see internal/server/certificate_handlers.go.
//
// The private key is write-only material: the upload request carries it once
// and no response type has a field for it.

export interface CertificateIssue {
  kind: string
  field?: string
  message: string
}

export interface CertificateRevision {
  revision_id: string
  state: string
  actor?: string
  material_hash?: string
  subject?: string
  issuer?: string
  dns_names?: string[]
  ip_addresses?: string[]
  not_before?: string
  not_after?: string
  fingerprint_sha256?: string
  key_algorithm?: string
  is_ca?: boolean
  has_ca?: boolean
  has_chain?: boolean
  serial_number?: string
  expiry_status?: string
  failure_reason?: string
  created_at?: string
  activated_at?: string
  issues?: CertificateIssue[]
  warnings?: CertificateIssue[]
}

export interface CertificateProfile {
  name: string
  description?: string
  active_revision?: string
  expiry_status?: string
  created_at?: string
  updated_at?: string
  revisions?: CertificateRevision[]
}

export interface CertificateListResponse {
  profiles?: CertificateProfile[]
}

export interface CertificateFiles {
  ca_path?: string
  cert_path?: string
  key_path?: string
  chain_path?: string
}

export interface CertificatePreview {
  profile: string
  revision_id: string
  current_revision?: string
  subject?: string
  issuer?: string
  dns_names?: string[]
  not_before?: string
  not_after?: string
  fingerprint_sha256?: string
  expiry_status?: string
  files?: CertificateFiles
  restart_required?: boolean
  warnings?: CertificateIssue[]
}

export interface CertificateVerification {
  listener_id?: string
  port?: number
  status: string
  reason?: string
  detail?: string
}

export interface CertificateApplyResult {
  profile: string
  revision_id: string
  previous_revision?: string
  restarted?: boolean
  recovered?: boolean
  recovery_note?: string
  verification?: CertificateVerification[]
}

export interface CertificateRevisionRequest {
  ca_cert_pem: string
  server_cert_pem: string
  server_key_pem: string
  chain_pem?: string
  expected_names?: string[]
  description?: string
  require_ca?: boolean
}

export interface CertificateActionRequest {
  profile: string
  revision_id: string
}
