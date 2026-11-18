import { can, requiredRoleFor } from '../auth/permissions'
import { bodyText, leverSecondary } from '../ui/styles'

// PendingChangesAction offers the next step the current role can actually
// take for the changes that are saved in MCM but not applied to Mosquitto
// yet: reviewing and applying for an admin, previewing for an operator, and
// an explanation without an action for the roles that may do neither.
// A small control, not the primary lever: the apply itself is the lever, on the Deploy view.
export function PendingChangesAction({ role, onReview }: { role: string; onReview: () => void }) {
  if (can(role, 'deploy.apply')) {
    return (
      <button type="button" onClick={onReview} className={`mt-4 ${leverSecondary}`}>
        Review and apply
      </button>
    )
  }
  if (can(role, 'deploy.preview')) {
    return (
      <>
        <p className={`mt-2 ${bodyText}`}>
          Applying them needs the {requiredRoleFor('deploy.apply')} role and MFA.
        </p>
        <button type="button" onClick={onReview} className={`mt-4 ${leverSecondary}`}>
          Preview changes
        </button>
      </>
    )
  }
  return (
    <p className={`mt-2 ${bodyText}`}>
      Previewing them needs the {requiredRoleFor('deploy.preview')} role or higher; applying them needs the {requiredRoleFor('deploy.apply')} role and MFA.
    </p>
  )
}
