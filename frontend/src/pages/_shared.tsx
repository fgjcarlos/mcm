/**
 * Shared UI sub-components used across multiple page files.
 * These are internal to the pages layer — not re-exported as public components.
 */

import type { SchemaValidation, SparkplugMetadata, TopicMessage } from '../types'
import { payloadChips } from '../lib/payload-utils'

// ---- SparkplugBadge --------------------------------------------------------

export function SparkplugBadge({ metadata }: { metadata: SparkplugMetadata }) {
  return (
    <span className="rounded-full bg-orange-400/10 px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] text-orange-200">
      Sparkplug {metadata.message_type}
    </span>
  )
}

// ---- SparkplugDetails -------------------------------------------------------

export function SparkplugDetails({ metadata, compact = false }: { metadata: SparkplugMetadata; compact?: boolean }) {
  const items = [
    ['Group', metadata.group_id],
    ['Message', metadata.message_type],
    ['Edge node', metadata.edge_node_id],
    ['Device', metadata.device_id],
  ].filter(([, value]) => Boolean(value))

  return (
    <div className={`${compact ? 'mt-2' : 'mt-4'} rounded-2xl border border-orange-300/20 bg-orange-400/10 p-3`}>
      <div className="flex flex-wrap items-center gap-2">
        <SparkplugBadge metadata={metadata} />
        <span className="rounded-full bg-slate-950/60 px-2.5 py-1 font-mono text-xs text-orange-100">{metadata.namespace}</span>
      </div>
      <dl className="mt-3 grid gap-2 text-xs text-slate-200 sm:grid-cols-2">
        {items.map(([label, value]) => (
          <div key={label}>
            <dt className="uppercase tracking-[0.18em] text-orange-100/70">{label}</dt>
            <dd className="mt-1 break-all font-mono text-orange-50">{value}</dd>
          </div>
        ))}
      </dl>
    </div>
  )
}

// ---- SchemaValidationBadge --------------------------------------------------

export function SchemaValidationBadge({ validation }: { validation: SchemaValidation }) {
  return (
    <span className={`rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${validation.valid ? 'bg-emerald-400/10 text-emerald-200' : 'bg-red-400/10 text-red-200'}`}>
      Schema {validation.valid ? 'valid' : 'invalid'}
    </span>
  )
}

// ---- SchemaValidationDetails ------------------------------------------------

export function SchemaValidationDetails({ validation }: { validation: SchemaValidation }) {
  return (
    <div className={`mt-4 rounded-2xl border p-3 ${validation.valid ? 'border-emerald-300/20 bg-emerald-400/10' : 'border-red-300/20 bg-red-400/10'}`}>
      <div className="flex flex-wrap items-center gap-2">
        <SchemaValidationBadge validation={validation} />
        <span className="rounded-full bg-slate-950/60 px-2.5 py-1 font-mono text-xs text-slate-100">{validation.schema_name}</span>
        <span className="rounded-full bg-slate-950/60 px-2.5 py-1 font-mono text-xs text-slate-100">{validation.topic_filter}</span>
      </div>
      {!validation.valid && validation.errors?.length ? (
        <ul className="mt-3 list-disc space-y-1 pl-5 text-xs text-red-100">
          {validation.errors.map((error) => (
            <li key={error} className="break-all">{error}</li>
          ))}
        </ul>
      ) : null}
    </div>
  )
}

// ---- Payload components -----------------------------------------------------

export function PayloadFact({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-xs uppercase tracking-[0.18em] text-slate-400">{label}</p>
      <p className="mt-1 break-all font-mono text-slate-100">{value}</p>
    </div>
  )
}

export function PayloadSummary({ event }: { event: TopicMessage }) {
  return (
    <div className="mt-3 flex flex-wrap gap-2">
      {payloadChips(event).map((chip) => (
        <span key={chip} className="rounded-full bg-slate-950/70 px-2.5 py-1 font-mono text-[0.7rem] text-slate-300">{chip}</span>
      ))}
    </div>
  )
}

export function PayloadMetadata({ event }: { event: TopicMessage }) {
  const inspection = event.payload_inspection
  const chips = payloadChips(event)

  return (
    <div className="mt-4 space-y-3 rounded-2xl border border-white/10 bg-slate-950/40 p-4">
      <div className="flex flex-wrap gap-2">
        {chips.map((chip) => (
          <span key={chip} className="rounded-full bg-cyan-400/10 px-3 py-1 font-mono text-xs text-cyan-100">{chip}</span>
        ))}
      </div>
      {inspection?.json_valid ? (
        <div className="grid gap-3 text-sm text-slate-200 sm:grid-cols-2">
          {inspection.detected_type === 'json_object' ? (
            <div className="sm:col-span-2">
              <p className="text-xs uppercase tracking-[0.18em] text-slate-400">Top-level keys</p>
              <div className="mt-2 flex flex-wrap gap-2">
                {(inspection.json_top_level_keys ?? []).length === 0 ? (
                  <span className="text-slate-300">No keys.</span>
                ) : (
                  inspection.json_top_level_keys?.map((key) => (
                    <span key={key} className="rounded-lg bg-white/[0.06] px-2 py-1 font-mono text-xs text-slate-100">{key}</span>
                  ))
                )}
              </div>
            </div>
          ) : null}
          {inspection.detected_type === 'json_array' ? <PayloadFact label="Elements" value={String(inspection.json_element_count ?? 0)} /> : null}
          {inspection.detected_type === 'json_scalar' && inspection.json_scalar_summary ? <PayloadFact label="Scalar" value={inspection.json_scalar_summary} /> : null}
        </div>
      ) : (
        <p className="text-sm text-slate-300">Preview remains bounded and escaped by the UI; raw payloads are not persisted.</p>
      )}
    </div>
  )
}
