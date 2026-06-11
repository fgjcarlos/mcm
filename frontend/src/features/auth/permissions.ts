// Role-based access control helpers — mirrors backend RBAC floors in internal/auth/role.go.
// Role ranks: viewer=1 < auditor=2 < operator=3 < admin=4.

export type Role = 'viewer' | 'auditor' | 'operator' | 'admin'

export const ROLE_RANK: Record<Role, number> = {
  viewer: 1,
  auditor: 2,
  operator: 3,
  admin: 4,
}

// Actions that require a minimum role to perform.
export type Action = 'acl.write' | 'mqttUser.write' | 'deploy.preview' | 'deploy.apply' | 'adminUser.write'

export const ACTION_MIN_ROLE: Record<Action, Role> = {
  'acl.write': 'operator',
  'mqttUser.write': 'operator',
  'deploy.preview': 'operator',
  'deploy.apply': 'admin',
  'adminUser.write': 'admin',
}

/**
 * Returns true if the given role is allowed to perform the action.
 * An unknown or empty role string ranks 0 and therefore always returns false (safe default deny).
 */
export function can(role: string, action: Action): boolean {
  const userRank = ROLE_RANK[role as Role] ?? 0
  const minRank = ROLE_RANK[ACTION_MIN_ROLE[action]]
  return userRank >= minRank
}

/** Returns the minimum Role required for the given action. */
export function requiredRoleFor(action: Action): Role {
  return ACTION_MIN_ROLE[action]
}

/** Builds a tooltip string explaining the required role for an action. */
export function permissionTitle(action: Action): string {
  const role = requiredRoleFor(action)
  const isTopTier = ROLE_RANK[role] === Math.max(...Object.values(ROLE_RANK))
  return isTopTier ? `Requires ${role} role` : `Requires ${role} role or higher`
}
