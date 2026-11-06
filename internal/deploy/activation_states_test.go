package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/storage"
)

// TestApplyDynSecConfigurationStaysPendingUntilTheControlChannelConfirms
// pins that the dynamic-security configuration is not declared active
// before the plugin control channel confirmed the applied state (issue
// #293, S21). The configuration was written and the broker was restarted,
// but nothing proved the plugin is loaded, so the deployment stays in
// pending_activation.
func TestApplyDynSecConfigurationStaysPendingUntilTheControlChannelConfirms(t *testing.T) {
	t.Parallel()

	svc, _, _ := settingsServiceWith(t, &fakeApplier{}, settingsConf)
	preview, err := svc.PreviewBrokerConfig(context.Background(), "operator")
	if err != nil {
		t.Fatalf("PreviewBrokerConfig: %v", err)
	}

	dep, err := svc.applyBrokerConfigRevision(context.Background(), "admin", preview.RevisionID, true, true, "dynsec")
	if err != nil {
		t.Fatalf("applyBrokerConfigRevision: %v", err)
	}
	if dep.Status != "pending_activation" {
		t.Errorf("status = %q, want pending_activation", dep.Status)
	}
	if dep.Message == "" {
		t.Error("the deployment does not say why the activation is pending")
	}
}

// TestConfirmDynSecActivationDeclaresTheDeploymentActive pins that the
// control channel confirmation is what turns the dynamic-security
// deployment into a verified activation (issue #293, S21).
func TestConfirmDynSecActivationDeclaresTheDeploymentActive(t *testing.T) {
	t.Parallel()

	svc, _, _ := settingsServiceWith(t, &fakeApplier{}, settingsConf)
	store, ok := svc.deployStore.(*fakeDeploymentStore)
	if !ok {
		t.Fatalf("the deployment store is %T", svc.deployStore)
	}
	dep := &storage.Deployment{Actor: "admin", Status: "pending_activation", Kind: "dynsec"}
	if err := store.InsertDeployment(context.Background(), dep); err != nil {
		t.Fatalf("insert deployment: %v", err)
	}

	confirmed, err := svc.confirmDynSecActivation(context.Background(), "admin", *dep)
	if err != nil {
		t.Fatalf("confirmDynSecActivation: %v", err)
	}
	if confirmed.Status != "active_verified" {
		t.Errorf("status = %q, want active_verified", confirmed.Status)
	}
	if !strings.Contains(confirmed.Message, "control channel") {
		t.Errorf("message = %q, want it to name the control channel", confirmed.Message)
	}
}
