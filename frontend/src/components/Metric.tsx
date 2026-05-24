export function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-2xl border border-white/10 bg-white/[0.04] px-4 py-4">
      <div className="font-mono text-2xl font-semibold capitalize text-white">{value}</div>
      <div className="mt-2 text-xs uppercase tracking-[0.22em] text-slate-400">{label}</div>
    </div>
  )
}
