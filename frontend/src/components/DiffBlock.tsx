export function DiffBlock({ content }: { content: string }) {
  return (
    <pre className="max-h-64 overflow-auto rounded-2xl border border-white/10 bg-slate-950/70 p-4 font-mono text-xs leading-5">
      {content.split('\n').map((line, idx) => (
        <span
          key={idx}
          className={`block ${line.startsWith('+') ? 'bg-emerald-500/10 text-emerald-200' : line.startsWith('-') ? 'bg-red-500/10 text-red-200' : 'text-slate-300'}`}
        >
          {line || ' '}
        </span>
      ))}
    </pre>
  )
}
