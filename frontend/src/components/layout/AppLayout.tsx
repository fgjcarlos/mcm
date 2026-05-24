import { Outlet, useRouterState } from '@tanstack/react-router'
import type { AdminUser } from '../../types'
import { Sidebar } from './Sidebar'
import { navItems } from '../../lib/nav-items'
import { useBroker } from '../../context/broker-context'
import { Metric } from '../Metric'

type Props = {
  currentUser: AdminUser
  onLogout: () => void
}

export function AppLayout({ currentUser, onLogout }: Props) {
  const routerState = useRouterState()
  const { brokerStatus, logs, uniqueTopicCount } = useBroker()

  const currentPath = routerState.location.pathname
  const activeItem = navItems.find((item) => `/${item.id}` === currentPath) ?? navItems[0]

  return (
    <div className="min-h-screen bg-[radial-gradient(circle_at_top,#1d4e89_0%,#0f172a_38%,#020617_100%)] text-slate-100">
      <div className="mx-auto flex min-h-screen w-full max-w-7xl flex-col px-4 py-4 sm:px-6 lg:flex-row lg:px-8 lg:py-8">
        <Sidebar currentUser={currentUser} onLogout={onLogout} />

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
                <Metric label="Logs" value={String(logs.length).padStart(2, '0')} />
              </div>
            </div>

            <Outlet />
          </div>
        </main>
      </div>
    </div>
  )
}
