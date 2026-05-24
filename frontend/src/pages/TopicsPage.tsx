import type { TopicMessage } from '../types'
import {
  PayloadMetadata,
  PayloadSummary,
  SchemaValidationBadge,
  SchemaValidationDetails,
  SparkplugBadge,
  SparkplugDetails,
} from './_shared'

type Props = {
  topics: TopicMessage[]
  latestTopic?: TopicMessage
}

export function TopicsPage({ topics, latestTopic }: Props) {
  return (
    <section className="mt-8 grid gap-4 lg:grid-cols-[0.85fr_1.15fr]">
      <article className="rounded-[1.75rem] border border-white/10 bg-[linear-gradient(135deg,rgba(34,211,238,0.16),rgba(15,23,42,0.08)_55%,rgba(249,115,22,0.18))] p-6">
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-100/80">Latest message</p>
        {latestTopic ? (
          <>
            <h3 className="mt-3 break-all text-2xl font-semibold text-white">{latestTopic.topic}</h3>
            {latestTopic.schema_validation ? <SchemaValidationDetails validation={latestTopic.schema_validation} /> : null}
            {latestTopic.sparkplug ? <SparkplugDetails metadata={latestTopic.sparkplug} /> : null}
            <PayloadMetadata event={latestTopic} />
            <pre className="mt-4 max-h-64 overflow-auto rounded-2xl border border-white/10 bg-slate-950/70 p-4 text-sm text-slate-100">{latestTopic.payload_preview}</pre>
          </>
        ) : (
          <p className="mt-3 text-sm leading-7 text-slate-200/90">Waiting for messages from the development broker.</p>
        )}
      </article>

      <article className="rounded-[1.75rem] border border-dashed border-cyan-300/25 bg-slate-900/70 p-6">
        <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">Topic explorer</p>
        <div className="mt-4 space-y-3">
          {topics.length === 0 ? (
            <p className="text-sm text-slate-300">No topic activity received yet.</p>
          ) : (
            topics.map((topic, index) => (
              <div key={`${topic.topic}-${topic.observed_at}-${index}`} className="rounded-2xl border border-white/10 bg-white/[0.04] p-4">
                <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                  <div className="flex flex-wrap items-center gap-2">
                    <p className="break-all font-mono text-sm text-cyan-100">{topic.topic}</p>
                    {topic.schema_validation ? <SchemaValidationBadge validation={topic.schema_validation} /> : null}
                    {topic.sparkplug ? <SparkplugBadge metadata={topic.sparkplug} /> : null}
                  </div>
                  <span className="text-xs text-slate-400">{new Date(topic.observed_at).toLocaleTimeString()}</span>
                </div>
                {topic.sparkplug ? <SparkplugDetails metadata={topic.sparkplug} compact /> : null}
                <p className="mt-2 line-clamp-2 break-all text-sm text-slate-300">{topic.payload_preview}</p>
                <PayloadSummary event={topic} />
              </div>
            ))
          )}
        </div>
      </article>
    </section>
  )
}
