package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// TestPreviewNamesShadowedServiceAccountRemedy reproduces the issue #297
// service-account coherence gap: a legacy stored MQTT user sharing the
// configured service username made every preview fail with the generic
// "username duplicates another passwd entry", while the API treated that
// same row as the protected service account (disable/delete/reset all
// 409). The only escape — renaming the row away — was allowed but never
// explained to the operator.
func TestPreviewNamesShadowedServiceAccountRemedy(t *testing.T) {
	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "")
	hash, err := mosquitto.HashPassword("legacy-password", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("hash the legacy password: %v", err)
	}
	svc := newTestService(
		&fakeApplier{},
		&fakeACLStore{},
		&fakeMQTTUserLister{users: []storage.MQTTUser{{Username: "svc-mcm", PasswordHash: hash}}},
		newFakeDeploymentStore(),
		diagnostics.VerifierFunc(okVerifier),
		newFakePasswordLookup(),
		deployCfg,
	)
	svc.mosquittoCfg.Username = "svc-mcm"
	svc.mosquittoCfg.Password = "service-password"

	_, err = svc.Preview(context.Background(), "operator")
	if err == nil {
		t.Fatal("Preview must refuse to render while a stored user shadows the service account")
	}
	if !strings.Contains(err.Error(), "svc-mcm") {
		t.Errorf("error must name the shadowing user: %v", err)
	}
	if !strings.Contains(err.Error(), "rename") {
		t.Errorf("error must explain the remedy (rename the stored user): %v", err)
	}
}
