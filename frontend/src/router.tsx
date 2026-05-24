import { createRouter, createRoute, createRootRoute, redirect } from '@tanstack/react-router'
import {
  RootComponent,
  DashboardRouteComponent,
  TopicsRouteComponent,
  LogsRouteComponent,
  SecurityRouteComponent,
  AuditRouteComponent,
  ACLsRouteComponent,
  UsersRouteComponent,
  DeployRouteComponent,
  SettingsRouteComponent,
} from './router-components'

const rootRoute = createRootRoute({ component: RootComponent })

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  beforeLoad: () => { throw redirect({ to: '/dashboard' }) },
})

const dashboardRoute = createRoute({ getParentRoute: () => rootRoute, path: '/dashboard', component: DashboardRouteComponent })
const topicsRoute = createRoute({ getParentRoute: () => rootRoute, path: '/topics', component: TopicsRouteComponent })
const logsRoute = createRoute({ getParentRoute: () => rootRoute, path: '/logs', component: LogsRouteComponent })
const securityRoute = createRoute({ getParentRoute: () => rootRoute, path: '/security', component: SecurityRouteComponent })
const auditRoute = createRoute({ getParentRoute: () => rootRoute, path: '/audit', component: AuditRouteComponent })
const aclsRoute = createRoute({ getParentRoute: () => rootRoute, path: '/acls', component: ACLsRouteComponent })
const usersRoute = createRoute({ getParentRoute: () => rootRoute, path: '/users', component: UsersRouteComponent })
const deployRoute = createRoute({ getParentRoute: () => rootRoute, path: '/deploy', component: DeployRouteComponent })
const settingsRoute = createRoute({ getParentRoute: () => rootRoute, path: '/settings', component: SettingsRouteComponent })

const routeTree = rootRoute.addChildren([
  indexRoute,
  dashboardRoute,
  topicsRoute,
  logsRoute,
  securityRoute,
  auditRoute,
  aclsRoute,
  usersRoute,
  deployRoute,
  settingsRoute,
])

export const router = createRouter({ routeTree })

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
