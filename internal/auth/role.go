package auth

import "fmt"

// Role identifies the authorization level granted to an admin user.
type Role string

const (
	// RoleViewer is the lowest privilege: dashboard, status, and event stream only.
	RoleViewer Role = "viewer"
	// RoleAuditor adds read access to audit and security event panels.
	RoleAuditor Role = "auditor"
	// RoleOperator adds CRUD on MQTT operational entities (ACLs, schemas).
	RoleOperator Role = "operator"
	// RoleAdmin adds management of admin users.
	RoleAdmin Role = "admin"
)

var roleRanks = map[Role]int{
	RoleViewer:   1,
	RoleAuditor:  2,
	RoleOperator: 3,
	RoleAdmin:    4,
}

// ParseRole canonicalizes role input. Empty input falls back to fallback.
func ParseRole(value string, fallback Role) (Role, error) {
	if value == "" {
		return fallback, nil
	}
	role := Role(value)
	if _, ok := roleRanks[role]; !ok {
		return "", fmt.Errorf("unsupported role %q", value)
	}
	return role, nil
}

// AtLeast reports whether the holder role meets or exceeds the required role.
func (r Role) AtLeast(required Role) bool {
	have, ok := roleRanks[r]
	if !ok {
		return false
	}
	want, ok := roleRanks[required]
	if !ok {
		return false
	}
	return have >= want
}

// Valid reports whether the role string maps to a known role.
func (r Role) Valid() bool {
	_, ok := roleRanks[r]
	return ok
}
