package acl

import (
	"errors"
	"strings"
	"testing"
)

// TestValidateRuleAcceptsDenyPermission pins that the native "deny"
// permission — introduced in Mosquitto 2.0 — is a valid managed
// permission.
func TestValidateRuleAcceptsDenyPermission(t *testing.T) {
	t.Parallel()

	err := ValidateRule(Rule{
		Principal:   "alice",
		TopicFilter: "sensors/#",
		Permission:  PermissionDeny,
	})
	if err != nil {
		t.Fatalf("ValidateRule(deny) returned error: %v", err)
	}
}

// TestValidateRuleAcceptsEverySupportedPermission pins that the supported
// Mosquitto profile accepts the three legacy permissions together with the
// native "deny" permission.
func TestValidateRuleAcceptsEverySupportedPermission(t *testing.T) {
	t.Parallel()

	permissions := []Permission{
		PermissionRead,
		PermissionWrite,
		PermissionReadWrite,
		PermissionDeny,
	}

	for _, permission := range permissions {
		err := ValidateRule(Rule{
			Principal:   "alice",
			TopicFilter: "sensors/+/temperature",
			Permission:  permission,
		})
		if err != nil {
			t.Fatalf("ValidateRule(%q) returned error: %v", permission, err)
		}
	}
}

// TestValidateRuleForMosquittoVersion pins the version seam: "deny" needs
// Mosquitto 2.0 or later, and an unknown version never satisfies it. The
// legacy permissions stay valid on every version, so an unsupported broker
// only blocks the native permission.
func TestValidateRuleForMosquittoVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		version     string
		permission  Permission
		wantProblem string
	}{
		{
			name:       "deny accepted on 2.0",
			version:    "2.0",
			permission: PermissionDeny,
		},
		{
			name:       "deny accepted on a later 2.x",
			version:    "2.0.22",
			permission: PermissionDeny,
		},
		{
			name:        "deny rejected before 2.0",
			version:     "1.6",
			permission:  PermissionDeny,
			wantProblem: `permission "deny" requires mosquitto 2.0 or later`,
		},
		{
			name:        "deny rejected on an unknown version",
			version:     "unknown",
			permission:  PermissionDeny,
			wantProblem: `permission "deny" requires mosquitto 2.0 or later`,
		},
		{
			name:        "deny rejected on an empty version",
			version:     "",
			permission:  PermissionDeny,
			wantProblem: `permission "deny" requires mosquitto 2.0 or later`,
		},
		{
			name:       "read stays valid on 1.6",
			version:    "1.6",
			permission: PermissionRead,
		},
		{
			name:       "write stays valid on an unknown version",
			version:    "unknown",
			permission: PermissionWrite,
		},
		{
			name:       "readwrite stays valid on an empty version",
			version:    "",
			permission: PermissionReadWrite,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateRuleForMosquittoVersion(Rule{
				Principal:   "alice",
				TopicFilter: "sensors/#",
				Permission:  tt.permission,
			}, tt.version)

			if tt.wantProblem == "" {
				if err != nil {
					t.Fatalf("ValidateRuleForMosquittoVersion(%q, %q) returned error: %v", tt.version, tt.permission, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantProblem) {
				t.Fatalf("ValidateRuleForMosquittoVersion(%q, %q) error = %v, want %q",
					tt.version, tt.permission, err, tt.wantProblem)
			}
		})
	}
}

// TestValidateRuleRejectsUnknownPermission pins that the permission list
// still rejects values outside the supported set.
func TestValidateRuleRejectsUnknownPermission(t *testing.T) {
	t.Parallel()

	err := ValidateRule(Rule{
		Principal:   "alice",
		TopicFilter: "sensors/#",
		Permission:  Permission("allow"),
	})
	if err == nil {
		t.Fatal("ValidateRule accepted an unknown permission")
	}

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if !strings.Contains(err.Error(), `permission must be one of: "read", "write", "readwrite", "deny"`) {
		t.Fatalf("unexpected validation message: %v", err)
	}
}

// TestDenyRuleMosquittoACL renders the deny permission as a native
// Mosquitto ACL line.
func TestDenyRuleMosquittoACL(t *testing.T) {
	t.Parallel()

	rule := Rule{
		Principal:   "alice",
		TopicFilter: "sensors/+/temperature",
		Permission:  PermissionDeny,
	}

	if got, want := rule.MosquittoACL(), "topic deny sensors/+/temperature"; got != want {
		t.Fatalf("MosquittoACL() = %q, want %q", got, want)
	}
}

// TestSupportedMosquittoVersion pins the supported execution profile: the
// native deny permission is validated against Mosquitto 2.0.
func TestSupportedMosquittoVersion(t *testing.T) {
	t.Parallel()

	if SupportedMosquittoVersion != "2.0" {
		t.Fatalf("SupportedMosquittoVersion = %q, want %q", SupportedMosquittoVersion, "2.0")
	}
	if MinimumDenyPermissionVersion != "2.0" {
		t.Fatalf("MinimumDenyPermissionVersion = %q, want %q", MinimumDenyPermissionVersion, "2.0")
	}
}
