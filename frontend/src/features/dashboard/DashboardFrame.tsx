import type { ReactNode } from 'react'
import type { AdminUser } from '../auth/useAuthSession'
import type { BrokerConnectionStatus, BrokerStreamState } from '../broker/useBrokerStream'

export type NavItem = {
  id: string
  label: string
  eyebrow: string
  title: string
  description: string
}

type DashboardFrameProps = {
  navItems: NavItem[]
  activeItem: NavItem
  activeId: string
  brokerStatus: BrokerConnectionStatus
  streamState: BrokerStreamState
  uniqueTopicCount: number
  logCount: number
  currentUser: AdminUser
  onSelectNav: (id: string) => void
  onLogout: () => void
  children: ReactNode
}

export function DashboardFrame({
  navItems,
  activeItem,
  activeId,
  brokerStatus,
  streamState,
  uniqueTopicCount,
  logCount,
  currentUser,
  onSelectNav,
  onLogout,
  children,
}: DashboardFrameProps) {
  return (
    <div className="min-h-screen bg-[radial-gradient(circle_at_top,#1d4e89_0%,#0f172a_38%,#020617_100%)] text-slate-100">
      <div className="mx-auto flex min-h-screen w-full max-w-7xl flex-col px-4 py-4 sm:px-6 lg:flex-row lg:px-8 lg:py-8">
        <aside className="mb-4 w-full rounded-[2rem] border border-white/10 bg-slate-950/65 p-5 shadow-2xl shadow-slate-950/40 backdrop-blur lg:mb-0 lg:w-80 lg:p-6">
          <div className="flex items-center justify-between gap-4 border-b border-white/10 pb-5">
            <div>
              <p className="text-xs font-semibold uppercase tracking-[0.3em] text-cyan-300">MCM</p>
              <h1 className="mt-2 text-2xl font-semibold tracking-tight text-white">Control Manager</h1>
            </div>
            <div className="rounded-full border border-cyan-400/30 bg-cyan-400/10 px-3 py-1 text-xs font-medium text-cyan-100">Alpha</div>
          </div>

          <div className="mt-6 rounded-2xl border border-white/10 bg-white/[0.04] p-4">
            <div className="flex items-center justify-between">
              <span className="text-xs uppercase tracking-[0.22em] text-slate-400">Broker</span>
              <span className={`h-3 w-3 rounded-full ${brokerStatus === 'connected' ? 'bg-emerald-400 shadow-[0_0_20px_rgba(52,211,153,0.9)]' : 'bg-rose-400'}`} />
            </div>
            <p className="mt-2 text-lg font-semibold capitalize text-white">{brokerStatus}</p>
            <p className="mt-1 text-xs text-slate-400">Event stream: {streamState}</p>
          </div>

          <nav className="mt-8 space-y-2" aria-label="Primary navigation">
            {navItems.map((item, index) => {
              const isActive = item.id === activeId
              return (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => onSelectNav(item.id)}
                  className={`group flex w-full items-center justify-between rounded-2xl border px-4 py-3 text-left transition ${
                    isActive
                      ? 'border-cyan-300/40 bg-cyan-300/12 text-white shadow-lg shadow-cyan-950/30'
                      : 'border-white/5 bg-white/[0.03] text-slate-300 hover:border-white/15 hover:bg-white/[0.06] hover:text-white'
                  }`}
                >
                  <span>
                    <span className="block text-sm font-semibold">{item.label}</span>
                    <span className="block text-xs uppercase tracking-[0.22em] text-slate-400 group-hover:text-slate-300">{item.eyebrow}</span>
                  </span>
                  <span className="font-mono text-xs text-slate-400">{String(index + 1).padStart(2, '0')}</span>
                </button>
              )
            })}
          </nav>

          <div className="mt-8 rounded-2xl border border-white/10 bg-white/[0.04] p-4">
            <div className="flex items-center justify-between gap-3">
              <div className="min-w-0">
                <p className="text-xs uppercase tracking-[0.22em] text-slate-400">Signed in</p>
                <p className="mt-1 truncate text-sm font-semibold text-white">{currentUser.username}</p>
                <p className="mt-1 inline-flex rounded-full border border-cyan-300/30 bg-cyan-400/10 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-[0.22em] text-cyan-100">{currentUser.role}</p>
              </div>
              <button
                type="button"
                onClick={onLogout}
                className="rounded-full border border-rose-300/30 bg-rose-400/10 px-3 py-1.5 text-xs font-semibold uppercase tracking-[0.18em] text-rose-100 transition hover:border-rose-300/60 hover:bg-rose-400/20"
              >
                Logout
              </button>
            </div>
          </div>
        </aside>

        <main className="flex-1 lg:pl-6">
          <div className="rounded-[2rem] border border-white/10 bg-slate-950/55 p-6 shadow-2xl shadow-slate-950/40 backdrop-blur sm:p-8">
            <div className="flex flex-col gap-6 border-b border-white/10 pb-8 xl:flex-row xl:items-end xl:justify-between">
              <div className="max-w-2xl">
                <p className="text-sm font-semibold uppercase tracking-[0.35em] text-cyan-300">{activeItem.eyebrow}</p>
                <h2 className="mt-3 text-4xl font-semibold tracking-tight text-white sm:text-5xl">{activeItem.title}</h2>
                <p className="mt-4 max-w-xl text-base leading-7 text-slate-300 sm:text-lg">{activeItem.description}</p>
              </div>

              <div className="grid grid-cols-3 gap-3 sm:min-w-[24rem]">
                <Metric label="Status" value={brokerStatus} />
                <Metric label="Topics" value={String(uniqueTopicCount).padStart(2, '0')} />
                <Metric label="Logs" value={String(logCount).padStart(2, '0')} />
              </div>
            </div>

            {children}
          </div>
        </main>
      </div>
    </div>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-2xl border border-white/10 bg-white/[0.04] px-4 py-4">
      <div className="font-mono text-2xl font-semibold capitalize text-white">{value}</div>
      <div className="mt-2 text-xs uppercase tracking-[0.22em] text-slate-400">{label}</div>
    </div>
  )
}
