import { useState } from 'react'

type NavItem = {
  id: string
  label: string
  eyebrow: string
  title: string
  description: string
  metrics: Array<{ label: string; value: string }>
}

const navItems: NavItem[] = [
  {
    id: 'dashboard',
    label: 'Dashboard',
    eyebrow: 'Broker overview',
    title: 'Operational snapshot',
    description:
      'Reserve this space for cluster health, retained alerts, connection rates, and the most recent management events.',
    metrics: [
      { label: 'Brokers', value: '01' },
      { label: 'Alerts', value: '00' },
      { label: 'Clients', value: '128' },
    ],
  },
  {
    id: 'users',
    label: 'Users',
    eyebrow: 'Identity',
    title: 'User directory placeholder',
    description:
      'This page will host account provisioning, credential resets, and status views for broker users.',
    metrics: [
      { label: 'Users', value: '42' },
      { label: 'Admins', value: '03' },
      { label: 'Pending', value: '01' },
    ],
  },
  {
    id: 'acls',
    label: 'ACLs',
    eyebrow: 'Authorization',
    title: 'ACL policy workspace',
    description:
      'Use this section later for topic permissions, policy reviews, and audit-safe change workflows.',
    metrics: [
      { label: 'Policies', value: '18' },
      { label: 'Wildcards', value: '05' },
      { label: 'Drafts', value: '02' },
    ],
  },
  {
    id: 'topics',
    label: 'Topics',
    eyebrow: 'Traffic',
    title: 'Topic explorer placeholder',
    description:
      'This area is reserved for retained message browsing, topic activity, and payload inspection tools.',
    metrics: [
      { label: 'Streams', value: '64' },
      { label: 'Retained', value: '11' },
      { label: 'Matches', value: '07' },
    ],
  },
  {
    id: 'clients',
    label: 'Clients',
    eyebrow: 'Connections',
    title: 'Client session monitor',
    description:
      'Future work can surface active sessions, keepalive state, client metadata, and disconnect history here.',
    metrics: [
      { label: 'Online', value: '97' },
      { label: 'Idle', value: '24' },
      { label: 'Blocked', value: '00' },
    ],
  },
  {
    id: 'settings',
    label: 'Settings',
    eyebrow: 'System',
    title: 'Platform settings placeholder',
    description:
      'This page is ready for global defaults, integration configuration, and broker-level safety controls.',
    metrics: [
      { label: 'Profiles', value: '04' },
      { label: 'TLS', value: 'On' },
      { label: 'Audit', value: 'On' },
    ],
  },
]

function App() {
  const [activeId, setActiveId] = useState<string>(navItems[0].id)
  const activeItem = navItems.find((item) => item.id === activeId) ?? navItems[0]

  return (
    <div className="min-h-screen bg-[radial-gradient(circle_at_top,#1d4e89_0%,#0f172a_38%,#020617_100%)] text-slate-100">
      <div className="mx-auto flex min-h-screen w-full max-w-7xl flex-col px-4 py-4 sm:px-6 lg:flex-row lg:px-8 lg:py-8">
        <aside className="mb-4 w-full rounded-[2rem] border border-white/10 bg-slate-950/65 p-5 shadow-2xl shadow-slate-950/40 backdrop-blur lg:mb-0 lg:w-80 lg:p-6">
          <div className="flex items-center justify-between gap-4 border-b border-white/10 pb-5">
            <div>
              <p className="text-xs font-semibold uppercase tracking-[0.3em] text-cyan-300">
                MCM
              </p>
              <h1 className="mt-2 text-2xl font-semibold tracking-tight text-white">
                Control Manager
              </h1>
            </div>
            <div className="rounded-full border border-cyan-400/30 bg-cyan-400/10 px-3 py-1 text-xs font-medium text-cyan-100">
              Alpha
            </div>
          </div>

          <div className="mt-6">
            <p className="text-sm leading-6 text-slate-300">
              Initial frontend shell for Mosquitto Control Manager. Navigation
              is in place so feature pages can land without reworking the
              layout.
            </p>
          </div>

          <nav className="mt-8 space-y-2" aria-label="Primary navigation">
            {navItems.map((item) => {
              const isActive = item.id === activeItem.id

              return (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => setActiveId(item.id)}
                  className={`group flex w-full items-center justify-between rounded-2xl border px-4 py-3 text-left transition ${
                    isActive
                      ? 'border-cyan-300/40 bg-cyan-300/12 text-white shadow-lg shadow-cyan-950/30'
                      : 'border-white/5 bg-white/[0.03] text-slate-300 hover:border-white/15 hover:bg-white/[0.06] hover:text-white'
                  }`}
                >
                  <span>
                    <span className="block text-sm font-semibold">{item.label}</span>
                    <span className="block text-xs uppercase tracking-[0.22em] text-slate-400 group-hover:text-slate-300">
                      {item.eyebrow}
                    </span>
                  </span>
                  <span className="font-mono text-xs text-slate-400">
                    {String(navItems.indexOf(item) + 1).padStart(2, '0')}
                  </span>
                </button>
              )
            })}
          </nav>
        </aside>

        <main className="flex-1 lg:pl-6">
          <div className="rounded-[2rem] border border-white/10 bg-slate-950/55 p-6 shadow-2xl shadow-slate-950/40 backdrop-blur sm:p-8">
            <div className="flex flex-col gap-6 border-b border-white/10 pb-8 xl:flex-row xl:items-end xl:justify-between">
              <div className="max-w-2xl">
                <p className="text-sm font-semibold uppercase tracking-[0.35em] text-cyan-300">
                  {activeItem.eyebrow}
                </p>
                <h2 className="mt-3 text-4xl font-semibold tracking-tight text-white sm:text-5xl">
                  {activeItem.title}
                </h2>
                <p className="mt-4 max-w-xl text-base leading-7 text-slate-300 sm:text-lg">
                  {activeItem.description}
                </p>
              </div>

              <div className="grid grid-cols-3 gap-3 sm:min-w-[24rem]">
                {activeItem.metrics.map((metric) => (
                  <div
                    key={metric.label}
                    className="rounded-2xl border border-white/10 bg-white/[0.04] px-4 py-4"
                  >
                    <div className="font-mono text-2xl font-semibold text-white">
                      {metric.value}
                    </div>
                    <div className="mt-2 text-xs uppercase tracking-[0.22em] text-slate-400">
                      {metric.label}
                    </div>
                  </div>
                ))}
              </div>
            </div>

            <section className="mt-8 grid gap-4 lg:grid-cols-[1.3fr_0.7fr]">
              <article className="rounded-[1.75rem] border border-white/10 bg-[linear-gradient(135deg,rgba(34,211,238,0.16),rgba(15,23,42,0.08)_55%,rgba(249,115,22,0.18))] p-6">
                <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-100/80">
                  Placeholder content
                </p>
                <h3 className="mt-3 text-2xl font-semibold text-white">
                  Ready for feature implementation
                </h3>
                <p className="mt-3 max-w-2xl text-sm leading-7 text-slate-200/90">
                  Issue #7 sets the application shell only. Each navigation
                  target is a stub so future issues can add data loading,
                  actions, tables, and forms without revisiting the frame.
                </p>
              </article>

              <article className="rounded-[1.75rem] border border-dashed border-cyan-300/25 bg-slate-900/70 p-6">
                <p className="text-xs font-semibold uppercase tracking-[0.25em] text-cyan-300">
                  Frontend stack
                </p>
                <ul className="mt-4 space-y-3 text-sm text-slate-300">
                  <li>React 19 with TypeScript and Vite</li>
                  <li>Tailwind CSS via the Vite plugin</li>
                  <li>Single responsive navigation layout</li>
                  <li>Placeholder views for MVP sections</li>
                </ul>
              </article>
            </section>
          </div>
        </main>
      </div>
    </div>
  )
}

export default App
