package auth

import (
	"testing"
)

// TestRoleAtLeast verifies role hierarchy comparison.
func TestRoleAtLeast(t *testing.T) {
	tests := []struct {
		name        string
		have        Role
		required    Role
		wantAtLeast bool
	}{
		// Same role
		{name: "viewer >= viewer", have: RoleViewer, required: RoleViewer, wantAtLeast: true},
		{name: "operator >= operator", have: RoleOperator, required: RoleOperator, wantAtLeast: true},
		{name: "admin >= admin", have: RoleAdmin, required: RoleAdmin, wantAtLeast: true},

		// Higher role meets lower requirement
		{name: "operator >= viewer", have: RoleOperator, required: RoleViewer, wantAtLeast: true},
		{name: "admin >= viewer", have: RoleAdmin, required: RoleViewer, wantAtLeast: true},
		{name: "admin >= operator", have: RoleAdmin, required: RoleOperator, wantAtLeast: true},
		{name: "admin >= auditor", have: RoleAdmin, required: RoleAuditor, wantAtLeast: true},

		// Lower role does not meet higher requirement
		{name: "viewer < operator", have: RoleViewer, required: RoleOperator, wantAtLeast: false},
		{name: "viewer < admin", have: RoleViewer, required: RoleAdmin, wantAtLeast: false},
		{name: "auditor < operator", have: RoleAuditor, required: RoleOperator, wantAtLeast: false},
		{name: "auditor < admin", have: RoleAuditor, required: RoleAdmin, wantAtLeast: false},
		{name: "operator < admin", have: RoleOperator, required: RoleAdmin, wantAtLeast: false},

		// Invalid roles
		{name: "invalid role has", have: Role("invalid"), required: RoleViewer, wantAtLeast: false},
		{name: "invalid role required", have: RoleAdmin, required: Role("invalid"), wantAtLeast: false},
		{name: "both invalid", have: Role("invalid1"), required: Role("invalid2"), wantAtLeast: false},

		// Empty roles
		{name: "empty role have", have: Role(""), required: RoleViewer, wantAtLeast: false},
		{name: "empty role required", have: RoleAdmin, required: Role(""), wantAtLeast: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.have.AtLeast(tt.required)
			if result != tt.wantAtLeast {
				t.Errorf("AtLeast returned %v, want %v", result, tt.wantAtLeast)
			}
		})
	}
}

// TestRoleValid verifies role validation.
func TestRoleValid(t *testing.T) {
	tests := []struct {
		name      string
		role      Role
		wantValid bool
	}{
		{name: "viewer is valid", role: RoleViewer, wantValid: true},
		{name: "auditor is valid", role: RoleAuditor, wantValid: true},
		{name: "operator is valid", role: RoleOperator, wantValid: true},
		{name: "admin is valid", role: RoleAdmin, wantValid: true},
		{name: "empty string is invalid", role: Role(""), wantValid: false},
		{name: "unknown role is invalid", role: Role("superuser"), wantValid: false},
		{name: "unknown role is invalid", role: Role("guest"), wantValid: false},
		{name: "unknown role is invalid", role: Role("root"), wantValid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.role.Valid()
			if result != tt.wantValid {
				t.Errorf("Valid returned %v, want %v", result, tt.wantValid)
			}
		})
	}
}
