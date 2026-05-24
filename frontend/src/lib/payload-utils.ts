import type { TopicMessage } from '../types'

export function payloadChips(event: TopicMessage): string[] {
  const inspection = event.payload_inspection
  const type = inspection?.detected_type ?? event.payload_format ?? 'unknown'
  const bytes = inspection?.byte_length ?? event.payload_bytes
  return [
    type,
    bytes === undefined ? undefined : `${bytes} bytes`,
    inspection?.json_valid ? 'valid JSON' : event.payload_format === 'json' ? 'JSON' : undefined,
    event.schema_validation ? `schema ${event.schema_validation.valid ? 'valid' : 'invalid'}` : undefined,
    (inspection?.truncated ?? event.truncated) ? 'truncated' : undefined,
  ].filter((chip): chip is string => Boolean(chip))
}
