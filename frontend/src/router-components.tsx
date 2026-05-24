/**
 * Route component wrappers — bridge between TanStack Router routes and page components.
 * Kept in a separate file so router.tsx (which exports the router object) doesn't mix
 * component and non-component exports (react-refresh/only-export-components rule).
 */

import { useAuth } from './context/auth-context'
import { useBroker } from './context/broker-context'
import { AppLayout } from './components/layout/AppLayout'
import { DashboardPage } from './pages/DashboardPage'
import { TopicsPage } from './pages/TopicsPage'
import { LogsPage } from './pages/LogsPage'
import { SecurityPage } from './pages/SecurityPage'
import { AuditPage } from './pages/AuditPage'
import { ACLsPage } from './pages/ACLsPage'
import { UsersPage } from './pages/UsersPage'
import { DeployPage } from './pages/DeployPage'
import { SettingsPage } from './pages/SettingsPage'

export function RootComponent() {
  const { currentUser, logout } = useAuth()
  if (!currentUser) return null
  return <AppLayout currentUser={currentUser} onLogout={logout} />
}

export function DashboardRouteComponent() {
  const { liveTrafficMetrics, topics, latestTopic } = useBroker()
  return <DashboardPage metrics={liveTrafficMetrics} topics={topics} latestTopic={latestTopic} />
}

export function TopicsRouteComponent() {
  const { topics, latestTopic } = useBroker()
  return <TopicsPage topics={topics} latestTopic={latestTopic} />
}

export function LogsRouteComponent() {
  const { logs, streamState } = useBroker()
  return <LogsPage logs={logs} streamState={streamState} />
}

export function SecurityRouteComponent() {
  const { token, logout } = useAuth()
  return <SecurityPage token={token!} onLogout={logout} />
}

export function AuditRouteComponent() {
  const { token, logout } = useAuth()
  return <AuditPage token={token!} onLogout={logout} />
}

export function ACLsRouteComponent() {
  const { token } = useAuth()
  return <ACLsPage token={token!} />
}

export function UsersRouteComponent() {
  const { token } = useAuth()
  return <UsersPage token={token!} />
}

export function DeployRouteComponent() {
  const { token } = useAuth()
  return <DeployPage token={token!} />
}

export function SettingsRouteComponent() {
  const { token } = useAuth()
  return <SettingsPage token={token!} />
}
