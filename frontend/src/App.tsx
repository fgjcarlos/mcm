import { RouterProvider } from '@tanstack/react-router'
import { AuthProvider, useAuth } from './context/auth-context'
import { BrokerProvider } from './context/broker-context'
import { LoginScreen } from './components/LoginScreen'
import { router } from './router'

function AuthenticatedApp() {
  const { token, currentUser, login: handleLogin } = useAuth()

  if (!token) {
    return <LoginScreen onLogin={handleLogin} />
  }

  if (!currentUser) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[radial-gradient(circle_at_top,#1d4e89_0%,#0f172a_38%,#020617_100%)] text-slate-100">
        <p className="text-sm uppercase tracking-[0.3em] text-slate-400">Restoring session…</p>
      </div>
    )
  }

  return (
    <BrokerProvider token={token}>
      <RouterProvider router={router} />
    </BrokerProvider>
  )
}

function AppWithProviders() {
  return (
    <AuthProvider>
      <AuthenticatedApp />
    </AuthProvider>
  )
}

export default AppWithProviders
