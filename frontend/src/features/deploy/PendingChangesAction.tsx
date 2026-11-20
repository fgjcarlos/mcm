import { can, requiredRoleFor } from '../auth/permissions'
import { bodyText, leverPrimary, leverSecondary } from '../ui/styles'

// PendingChangesAction offers the next step the current role can actually
// take for the changes that are saved in MCM but not applied to Mosquitto
// yet: reviewing and applying for an admin, previewing for an operator, and
// an explanation without an action for the roles that may do neither.
// A small control by default; the dashboard home makes it its one primary lever (#359)
// when pending changes are the next thing to do.
export function PendingChangesAction({ role, onReview, primary = false }: { role: string; onReview: () => void; primary?: boolean }) {
  const lever = `mt-4 ${primary ? leverPrimary : leverSecondary}`
  if (can(role, 'deploy.apply')) {
    return (
      <button type="button" onClick={onReview} className={lever}>
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
        <button type="button" onClick={onReview} className={lever}>
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
