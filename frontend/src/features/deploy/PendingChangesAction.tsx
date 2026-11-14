import { can, requiredRoleFor } from '../auth/permissions'

// PendingChangesAction offers the next step the current role can actually
// take for the changes that are saved in MCM but not applied to Mosquitto
// yet: reviewing and applying for an admin, previewing for an operator, and
// an explanation without an action for the roles that may do neither.
export function PendingChangesAction({ role, onReview }: { role: string; onReview: () => void }) {
  if (can(role, 'deploy.apply')) {
    return (
      <button type="button" onClick={onReview} className="mt-4 rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 focus-visible:outline focus-visible:outline-2 focus-visible:outline-cyan-300">
        Review and apply
      </button>
    )
  }
  if (can(role, 'deploy.preview')) {
    return (
      <>
        <p className="mt-2 text-sm leading-6 text-slate-300">
          Applying them needs the {requiredRoleFor('deploy.apply')} role and MFA.
        </p>
        <button type="button" onClick={onReview} className="mt-4 rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-white transition hover:bg-cyan-400 focus-visible:outline focus-visible:outline-2 focus-visible:outline-cyan-300">
          Preview changes
        </button>
      </>
    )
  }
  return (
    <p className="mt-2 text-sm leading-6 text-slate-300">
      Previewing them needs the {requiredRoleFor('deploy.preview')} role or higher; applying them needs the {requiredRoleFor('deploy.apply')} role and MFA.
    </p>
  )
}
