import type { BrokerLog } from '../broker/useBrokerStream'
import { Lamp } from '../ui/primitives'
import type { LampTone } from '../ui/styles'

// A logged error is a failure, a warning a caution; debug stays unlit.
const severityTone: Record<BrokerLog['severity'], LampTone> = { debug: 'off', info: 'neutral', warning: 'caution', error: 'failed' }

export function SeverityBadge({ severity }: { severity: BrokerLog['severity'] }) {
  return <Lamp tone={severityTone[severity]} className="w-20 shrink-0">{severity}</Lamp>
}
