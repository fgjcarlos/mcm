package listener

import (
	"context"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/certificate"
	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
	"github.com/fgjcarlos/mcm/internal/mosquitto/listeners"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// fakeCertificateResolver resolves one profile to fixed broker-side paths.
type fakeCertificateResolver struct {
	profiles map[string]certificate.ResolvedProfile
	err      error
}

func (r fakeCertificateResolver) ResolveProfile(_ context.Context, profileName string) (certificate.ResolvedProfile, error) {
	if r.err != nil {
		return certificate.ResolvedProfile{}, r.err
	}
	material, ok := r.profiles[profileName]
	if !ok {
		return certificate.ResolvedProfile{}, certificate.ErrProfileNotFound
	}
	return material, nil
}

// testCatalog returns the catalog of the supported broker version.
func testCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat, err := catalog.LoadVersion("2.0")
	if err != nil {
		t.Fatalf("catalog.LoadVersion() error = %v", err)
	}
	return cat
}

// productionMaterial is the resolved material of the "production" profile.
func productionMaterial() certificate.ResolvedProfile {
	return certificate.ResolvedProfile{
		Profile:  "production",
		CAPath:   "/mosquitto/config/certs/production/active/ca.crt",
		CertPath: "/mosquitto/config/certs/production/active/server.crt",
		KeyPath:  "/mosquitto/config/certs/production/active/server.key",
		HasCA:    true,
	}
}

func tlsListenerSpec(id string, port int, profile string) listeners.ListenerSpec {
	return listeners.ListenerSpec{
		ID:         id,
		Port:       port,
		Bind:       "0.0.0.0",
		Protocols:  []listeners.Protocol{listeners.ProtocolMQTT},
		TLSProfile: profile,
	}
}

func TestListenerService_Preview_InjectsTheProfilePaths(t *testing.T) {
	store := newInMemoryListenerStore(nil)
	svc := newTestService(store,
		WithListenerCatalog(testCatalog(t)),
		WithListenerCertificateResolver(fakeCertificateResolver{profiles: map[string]certificate.ResolvedProfile{"production": productionMaterial()}}),
	)

	preview, err := svc.Preview(context.Background(), []listeners.ListenerSpec{tlsListenerSpec("tls", 8883, "production")}, PreviewOptions{})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	for _, want := range []string{
		"certfile /mosquitto/config/certs/production/active/server.crt\n",
		"keyfile /mosquitto/config/certs/production/active/server.key\n",
		"cafile /mosquitto/config/certs/production/active/ca.crt\n",
	} {
		if !strings.Contains(preview.Rendered, want) {
			t.Fatalf("Preview() rendered = %q, missing %q", preview.Rendered, want)
		}
	}
}

func TestListenerService_Apply_PersistsTheProfileNameAndNotThePaths(t *testing.T) {
	store := newInMemoryListenerStore(nil)
	writer := &recordingConfWriter{}
	svc := newTestService(store,
		WithListenerCatalog(testCatalog(t)),
		WithListenerCertificateResolver(fakeCertificateResolver{profiles: map[string]certificate.ResolvedProfile{"production": productionMaterial()}}),
		WithListenerConfWriter(writer),
		WithListenerRevisionID(func() (string, error) { return "revision", nil }),
	)

	if _, err := svc.Preview(context.Background(), []listeners.ListenerSpec{tlsListenerSpec("tls", 8883, "production")}, PreviewOptions{Confirm: true}); err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if err := svc.Apply(context.Background(), "revision", true); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(writer.specs) != 1 {
		t.Fatalf("writer specs = %#v, want one spec", writer.specs)
	}
	if writer.specs[0].Options["certfile"] != productionMaterial().CertPath {
		t.Fatalf("written certfile = %q, want %q", writer.specs[0].Options["certfile"], productionMaterial().CertPath)
	}
	if len(store.rows) != 1 {
		t.Fatalf("rows = %#v, want one row", store.rows)
	}
	if store.rows[0].TLSProfile != "production" {
		t.Fatalf("row tls_profile = %q, want production", store.rows[0].TLSProfile)
	}
	if _, exists := store.rows[0].Options["certfile"]; exists {
		t.Fatalf("the persisted row must not carry the resolved paths: %#v", store.rows[0].Options)
	}
}

func TestListenerService_Preview_RefusesAManagedTLSOption(t *testing.T) {
	svc := newTestService(newInMemoryListenerStore(nil),
		WithListenerCatalog(testCatalog(t)),
		WithListenerCertificateResolver(fakeCertificateResolver{profiles: map[string]certificate.ResolvedProfile{"production": productionMaterial()}}),
	)
	spec := tlsListenerSpec("tls", 8883, "production")
	spec.Options = map[string]string{"certfile": "/etc/ssl/other.crt"}

	preview, err := svc.Preview(context.Background(), []listeners.ListenerSpec{spec}, PreviewOptions{})
	if err == nil {
		t.Fatal("Preview() error = nil, want a conflict issue")
	}
	if !hasIssueKind(preview.Issues, "tls_conflict") {
		t.Fatalf("issues = %#v, want a tls_conflict issue", preview.Issues)
	}
}

func TestListenerService_Preview_RefusesAnUnknownProfile(t *testing.T) {
	svc := newTestService(newInMemoryListenerStore(nil),
		WithListenerCatalog(testCatalog(t)),
		WithListenerCertificateResolver(fakeCertificateResolver{profiles: map[string]certificate.ResolvedProfile{}}),
	)

	preview, err := svc.Preview(context.Background(), []listeners.ListenerSpec{tlsListenerSpec("tls", 8883, "production")}, PreviewOptions{})
	if err == nil {
		t.Fatal("Preview() error = nil, want an unavailable profile issue")
	}
	if !hasIssueKind(preview.Issues, "tls_profile_unavailable") {
		t.Fatalf("issues = %#v, want a tls_profile_unavailable issue", preview.Issues)
	}
}

func TestListenerService_Preview_RefusesMutualTLSWithoutACA(t *testing.T) {
	material := productionMaterial()
	material.HasCA = false
	svc := newTestService(newInMemoryListenerStore(nil),
		WithListenerCatalog(testCatalog(t)),
		WithListenerCertificateResolver(fakeCertificateResolver{profiles: map[string]certificate.ResolvedProfile{"production": material}}),
	)
	spec := tlsListenerSpec("tls", 8883, "production")
	spec.Options = map[string]string{"require_certificate": "true"}

	preview, err := svc.Preview(context.Background(), []listeners.ListenerSpec{spec}, PreviewOptions{})
	if err == nil {
		t.Fatal("Preview() error = nil, want a mutual TLS issue")
	}
	if !hasIssueKind(preview.Issues, "tls_ca_required") {
		t.Fatalf("issues = %#v, want a tls_ca_required issue", preview.Issues)
	}
}

func TestListenerService_Preview_AcceptsMutualTLSWithACA(t *testing.T) {
	svc := newTestService(newInMemoryListenerStore(nil),
		WithListenerCatalog(testCatalog(t)),
		WithListenerCertificateResolver(fakeCertificateResolver{profiles: map[string]certificate.ResolvedProfile{"production": productionMaterial()}}),
	)
	spec := tlsListenerSpec("tls", 8883, "production")
	spec.Options = map[string]string{"require_certificate": "true"}

	preview, err := svc.Preview(context.Background(), []listeners.ListenerSpec{spec}, PreviewOptions{})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if !strings.Contains(preview.Rendered, "require_certificate true\n") {
		t.Fatalf("Preview() rendered = %q, missing the require_certificate directive", preview.Rendered)
	}
}

func TestListenerService_Preview_RefusesTheBindingWithoutACatalog(t *testing.T) {
	svc := newTestService(newInMemoryListenerStore(nil),
		WithListenerCertificateResolver(fakeCertificateResolver{profiles: map[string]certificate.ResolvedProfile{"production": productionMaterial()}}),
	)

	preview, err := svc.Preview(context.Background(), []listeners.ListenerSpec{tlsListenerSpec("tls", 8883, "production")}, PreviewOptions{})
	if err == nil {
		t.Fatal("Preview() error = nil, want an unsupported issue")
	}
	if !hasIssueKind(preview.Issues, "tls_unsupported") {
		t.Fatalf("issues = %#v, want a tls_unsupported issue", preview.Issues)
	}
}

func TestListenerService_Preview_RefusesTheBindingWithoutAResolver(t *testing.T) {
	svc := newTestService(newInMemoryListenerStore(nil), WithListenerCatalog(testCatalog(t)))

	preview, err := svc.Preview(context.Background(), []listeners.ListenerSpec{tlsListenerSpec("tls", 8883, "production")}, PreviewOptions{})
	if err == nil {
		t.Fatal("Preview() error = nil, want an unavailable issue")
	}
	if !hasIssueKind(preview.Issues, "tls_unavailable") {
		t.Fatalf("issues = %#v, want a tls_unavailable issue", preview.Issues)
	}
}

func TestListenerService_Preview_KeepsTheCurrentBindingRepairable(t *testing.T) {
	// The current listener references a profile that no longer exists. The
	// change must not be blocked by it: the operator has to be able to
	// rebind the listener.
	store := newInMemoryListenerStore([]storage.ListenerSpecRow{{ID: "tls", Port: 8883, Bind: "0.0.0.0", Protocols: []string{"mqtt"}, TLSProfile: "deleted"}})
	svc := newTestService(store,
		WithListenerCatalog(testCatalog(t)),
		WithListenerCertificateResolver(fakeCertificateResolver{profiles: map[string]certificate.ResolvedProfile{"production": productionMaterial()}}),
	)

	preview, err := svc.Preview(context.Background(), []listeners.ListenerSpec{tlsListenerSpec("tls", 8883, "production")}, PreviewOptions{})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if len(preview.Issues) != 0 {
		t.Fatalf("issues = %#v, want none", preview.Issues)
	}
	if len(preview.Warnings) == 0 {
		t.Fatal("warnings = none, want a warning about the broken current binding")
	}
}

func TestListenerService_Apply_RefusesAnUnresolvableProfile(t *testing.T) {
	store := newInMemoryListenerStore(nil)
	writer := &recordingConfWriter{}
	resolver := &switchingResolver{profiles: map[string]certificate.ResolvedProfile{"production": productionMaterial()}}
	svc := newTestService(store,
		WithListenerCatalog(testCatalog(t)),
		WithListenerCertificateResolver(resolver),
		WithListenerConfWriter(writer),
		WithListenerRevisionID(func() (string, error) { return "revision", nil }),
	)

	if _, err := svc.Preview(context.Background(), []listeners.ListenerSpec{tlsListenerSpec("tls", 8883, "production")}, PreviewOptions{Confirm: true}); err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	resolver.err = certificate.ErrProfileNotFound
	err := svc.Apply(context.Background(), "revision", true)
	if err == nil {
		t.Fatal("Apply() error = nil, want a resolution error")
	}
	if len(writer.specs) != 0 {
		t.Fatalf("writer specs = %#v, want none", writer.specs)
	}
}

// switchingResolver fails on demand.
type switchingResolver struct {
	profiles map[string]certificate.ResolvedProfile
	err      error
}

func (r *switchingResolver) ResolveProfile(_ context.Context, profileName string) (certificate.ResolvedProfile, error) {
	if r.err != nil {
		return certificate.ResolvedProfile{}, r.err
	}
	material, ok := r.profiles[profileName]
	if !ok {
		return certificate.ResolvedProfile{}, certificate.ErrProfileNotFound
	}
	return material, nil
}

// recordingConfWriter captures the specs it receives.
type recordingConfWriter struct {
	specs []listeners.ListenerSpec
}

func (w *recordingConfWriter) Write(specs []listeners.ListenerSpec) error {
	w.specs = append(w.specs, specs...)
	return nil
}

func (w *recordingConfWriter) Snapshot() ([]byte, error) { return nil, nil }

func (w *recordingConfWriter) Restore([]byte) error { return nil }

// hasIssueKind reports whether the issues contain the kind.
func hasIssueKind(issues []listeners.Issue, kind string) bool {
	for _, issue := range issues {
		if issue.Kind == kind {
			return true
		}
	}
	return false
}
