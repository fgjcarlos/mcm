package certificate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/mosquitto/certs"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// countingRestarter records the restarts it performed.
type countingRestarter struct {
	calls int
	err   error
}

func (r *countingRestarter) Restart(context.Context) error {
	r.calls++
	if r.err != nil {
		return r.err
	}
	return nil
}

func TestCreateRevisionStoresTheProfileAndItsRevision(t *testing.T) {
	service, repo, _ := serviceFixture(t)

	view, err := service.CreateRevision(context.Background(), "operator", "production", "broker certificates", materialFixture(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour)), false)
	if err != nil {
		t.Fatalf("CreateRevision returned error: %v", err)
	}
	if view.State != StatePending {
		t.Fatalf("CreateRevision state = %q, want %q", view.State, StatePending)
	}
	if view.Subject == "" || view.FingerprintSHA256 == "" {
		t.Fatalf("CreateRevision = %#v, want the certificate metadata", view)
	}
	if view.ExpiryStatus != ExpiryValid {
		t.Fatalf("CreateRevision expiry status = %q, want %q", view.ExpiryStatus, ExpiryValid)
	}

	profiles, err := service.ListProfiles(context.Background())
	if err != nil {
		t.Fatalf("ListProfiles returned error: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("ListProfiles = %d profiles, want 1", len(profiles))
	}
	if profiles[0].ActiveRevision != "" {
		t.Fatalf("ListProfiles active revision = %q, want none before the activation", profiles[0].ActiveRevision)
	}
	if len(profiles[0].Revisions) != 1 {
		t.Fatalf("ListProfiles revisions = %d, want 1", len(profiles[0].Revisions))
	}

	profile, err := repo.GetCertificateProfileByName(context.Background(), "production")
	if err != nil {
		t.Fatalf("GetCertificateProfileByName returned error: %v", err)
	}
	revisions, err := repo.ListCertificateRevisions(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("ListCertificateRevisions returned error: %v", err)
	}
	if len(revisions) != 1 {
		t.Fatalf("ListCertificateRevisions = %d revisions, want 1", len(revisions))
	}
	if revisions[0].Actor != "operator" {
		t.Fatalf("ListCertificateRevisions actor = %q, want operator", revisions[0].Actor)
	}
}

func TestCreateRevisionRejectsInvalidMaterial(t *testing.T) {
	service, _, _ := serviceFixture(t)

	expired := materialFixture(t, "broker.example.com", time.Now().UTC().Add(-48*time.Hour), time.Now().UTC().Add(-time.Hour))
	_, err := service.CreateRevision(context.Background(), "operator", "production", "", expired, false)
	if err == nil {
		t.Fatal("CreateRevision accepted an expired certificate")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("CreateRevision error = %v, want a ValidationError", err)
	}
	if len(validation.Issues) == 0 {
		t.Fatalf("CreateRevision validation error = %#v, want at least one issue", validation)
	}
}

func TestCreateRevisionRejectsAnUnsafeProfileName(t *testing.T) {
	service, _, _ := serviceFixture(t)

	_, err := service.CreateRevision(context.Background(), "operator", "../escape", "", materialFixture(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour)), false)
	if !errors.Is(err, ErrInvalidProfileName) {
		t.Fatalf("CreateRevision error = %v, want ErrInvalidProfileName", err)
	}
}

func TestTheRevisionViewNeverCarriesPrivateKeyMaterial(t *testing.T) {
	service, _, _ := serviceFixture(t)

	input := materialFixture(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour))
	view, err := service.CreateRevision(context.Background(), "operator", "production", "", input, false)
	if err != nil {
		t.Fatalf("CreateRevision returned error: %v", err)
	}
	encoded := encodeJSON(t, view)
	for _, forbidden := range []string{"PRIVATE KEY", "BEGIN EC", input.ServerKeyPEM} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("the revision view leaked private key material: %s", forbidden)
		}
	}
}

func TestApplyActivatesTheRevision(t *testing.T) {
	service, repo, restarter := serviceFixture(t)
	ctx := context.Background()

	first, err := service.CreateRevision(ctx, "operator", "production", "", materialFixture(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour)), false)
	if err != nil {
		t.Fatalf("CreateRevision returned error: %v", err)
	}
	result, err := service.Apply(ctx, "admin", "production", first.RevisionID)
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if !result.Restarted || result.Recovered {
		t.Fatalf("Apply = %#v, want a clean activation", result)
	}
	if restarter.calls != 1 {
		t.Fatalf("Apply restarts = %d, want 1", restarter.calls)
	}

	profiles, err := service.ListProfiles(ctx)
	if err != nil {
		t.Fatalf("ListProfiles returned error: %v", err)
	}
	if profiles[0].ActiveRevision != first.RevisionID {
		t.Fatalf("ListProfiles active revision = %q, want %q", profiles[0].ActiveRevision, first.RevisionID)
	}

	profile, err := repo.GetCertificateProfileByName(ctx, "production")
	if err != nil {
		t.Fatalf("GetCertificateProfileByName returned error: %v", err)
	}
	revision, err := repo.GetCertificateRevision(ctx, profile.ID, first.RevisionID)
	if err != nil {
		t.Fatalf("GetCertificateRevision returned error: %v", err)
	}
	if revision.State != StateActive {
		t.Fatalf("GetCertificateRevision state = %q, want %q", revision.State, StateActive)
	}
	if revision.ActivatedAt.IsZero() {
		t.Fatal("GetCertificateRevision did not record the activation timestamp")
	}
}

func TestApplyMarksThePreviousRevisionSuperseded(t *testing.T) {
	service, repo, _ := serviceFixture(t)
	ctx := context.Background()

	first, err := service.CreateRevision(ctx, "operator", "production", "", materialFixture(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour)), false)
	if err != nil {
		t.Fatalf("CreateRevision returned error: %v", err)
	}
	if _, err := service.Apply(ctx, "admin", "production", first.RevisionID); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	second, err := service.CreateRevision(ctx, "operator", "production", "", materialFixture(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(120*24*time.Hour)), false)
	if err != nil {
		t.Fatalf("CreateRevision returned error: %v", err)
	}
	if _, err := service.Apply(ctx, "admin", "production", second.RevisionID); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	profile, err := repo.GetCertificateProfileByName(ctx, "production")
	if err != nil {
		t.Fatalf("GetCertificateProfileByName returned error: %v", err)
	}
	previous, err := repo.GetCertificateRevision(ctx, profile.ID, first.RevisionID)
	if err != nil {
		t.Fatalf("GetCertificateRevision returned error: %v", err)
	}
	if previous.State != StateSuperseded {
		t.Fatalf("GetCertificateRevision state = %q, want %q", previous.State, StateSuperseded)
	}
}

func TestApplyRefusesToReactivateTheActiveRevision(t *testing.T) {
	service, _, _ := serviceFixture(t)
	ctx := context.Background()

	revision, err := service.CreateRevision(ctx, "operator", "production", "", materialFixture(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour)), false)
	if err != nil {
		t.Fatalf("CreateRevision returned error: %v", err)
	}
	if _, err := service.Apply(ctx, "admin", "production", revision.RevisionID); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if _, err := service.Apply(ctx, "admin", "production", revision.RevisionID); !errors.Is(err, ErrRevisionActive) {
		t.Fatalf("Apply error = %v, want ErrRevisionActive", err)
	}
}

func TestApplyRestoresThePreviousMaterialWhenTheBrokerFails(t *testing.T) {
	service, repo, restarter := serviceFixture(t)
	ctx := context.Background()

	first, err := service.CreateRevision(ctx, "operator", "production", "", materialFixture(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour)), false)
	if err != nil {
		t.Fatalf("CreateRevision returned error: %v", err)
	}
	if _, err := service.Apply(ctx, "admin", "production", first.RevisionID); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	restarter.err = errors.New("compose restart failed")

	second, err := service.CreateRevision(ctx, "operator", "production", "", materialFixture(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(120*24*time.Hour)), false)
	if err != nil {
		t.Fatalf("CreateRevision returned error: %v", err)
	}
	result, err := service.Apply(ctx, "admin", "production", second.RevisionID)
	if err == nil {
		t.Fatal("Apply accepted a broker that does not restart")
	}
	if !result.Recovered {
		t.Fatalf("Apply = %#v, want a recovered activation", result)
	}

	active, err := service.store.ActiveRevision("production")
	if err != nil {
		t.Fatalf("ActiveRevision returned error: %v", err)
	}
	if active != first.RevisionID {
		t.Fatalf("ActiveRevision = %q, want the previous revision %q", active, first.RevisionID)
	}

	profile, err := repo.GetCertificateProfileByName(ctx, "production")
	if err != nil {
		t.Fatalf("GetCertificateProfileByName returned error: %v", err)
	}
	failed, err := repo.GetCertificateRevision(ctx, profile.ID, second.RevisionID)
	if err != nil {
		t.Fatalf("GetCertificateRevision returned error: %v", err)
	}
	if failed.State != StateFailed {
		t.Fatalf("GetCertificateRevision state = %q, want %q", failed.State, StateFailed)
	}
	if failed.FailureReason == "" {
		t.Fatal("GetCertificateRevision did not record the failure reason")
	}
}

func TestApplyRollsBackWhenTheListenerRejectsTheMaterial(t *testing.T) {
	service, repo, _ := serviceFixture(t)
	ctx := context.Background()

	firstMaterial, firstCA, firstCAKey := materialWithCA(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour))
	port := startUntrustedTLSListener(t, firstCA, firstCAKey, "broker.example.com")
	if _, err := repo.InsertListenerSpec(ctx, storage.ListenerSpecRow{
		ID:         "tls-listener",
		Port:       port,
		Bind:       "0.0.0.0",
		Protocols:  []string{"mqtt"},
		TLSProfile: "production",
	}); err != nil {
		t.Fatalf("InsertListenerSpec returned error: %v", err)
	}

	first, err := service.CreateRevision(ctx, "operator", "production", "", firstMaterial, false)
	if err != nil {
		t.Fatalf("CreateRevision returned error: %v", err)
	}
	if _, err := service.Apply(ctx, "admin", "production", first.RevisionID); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	second, err := service.CreateRevision(ctx, "operator", "production", "", materialFixture(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(120*24*time.Hour)), false)
	if err != nil {
		t.Fatalf("CreateRevision returned error: %v", err)
	}
	result, err := service.Apply(ctx, "admin", "production", second.RevisionID)
	if err == nil {
		t.Fatal("Apply accepted a listener that rejects the new material")
	}
	if !result.Recovered {
		t.Fatalf("Apply = %#v, want a recovered activation", result)
	}
	if len(result.Verification) != 1 || result.Verification[0].Status != TLSStatusFailed {
		t.Fatalf("Apply verification = %#v, want one failed check", result.Verification)
	}

	active, err := service.store.ActiveRevision("production")
	if err != nil {
		t.Fatalf("ActiveRevision returned error: %v", err)
	}
	if active != first.RevisionID {
		t.Fatalf("ActiveRevision = %q, want the previous revision %q", active, first.RevisionID)
	}

	profile, err := repo.GetCertificateProfileByName(ctx, "production")
	if err != nil {
		t.Fatalf("GetCertificateProfileByName returned error: %v", err)
	}
	failed, err := repo.GetCertificateRevision(ctx, profile.ID, second.RevisionID)
	if err != nil {
		t.Fatalf("GetCertificateRevision returned error: %v", err)
	}
	if failed.State != StateFailed {
		t.Fatalf("GetCertificateRevision state = %q, want %q", failed.State, StateFailed)
	}
}

// delayedBrokerRestarter starts the broker only after the restart returned.
//
// A real broker needs a moment to bind its ports after the container
// restart, so the verification must not give up on the first refused
// connection.
type delayedBrokerRestarter struct {
	tb          testing.TB
	port        int
	certificate tls.Certificate
	delay       time.Duration
}

func (r *delayedBrokerRestarter) Restart(context.Context) error {
	go func() {
		time.Sleep(r.delay)
		listener, err := tls.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", r.port), &tls.Config{
			Certificates: []tls.Certificate{r.certificate},
			MinVersion:   tls.VersionTLS12,
		})
		if err != nil {
			return
		}
		r.tb.Cleanup(func() { _ = listener.Close() })
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if tlsConn, ok := conn.(*tls.Conn); ok {
					_ = tlsConn.Handshake()
				}
			}()
		}
	}()
	return nil
}

func TestApplyWaitsForTheBrokerToAcceptConnectionsAfterTheRestart(t *testing.T) {
	service, repo, _ := serviceFixture(t)
	ctx := context.Background()

	material, ca, caKey := materialWithCA(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour))
	port := reservePort(t)
	service.restart = &delayedBrokerRestarter{
		tb:          t,
		port:        port,
		certificate: listenerCertificate(t, ca, caKey, "broker.example.com"),
		delay:       300 * time.Millisecond,
	}
	service.verifyReadyWindow = 5 * time.Second
	service.verifyRetryInterval = 50 * time.Millisecond

	if _, err := repo.InsertListenerSpec(ctx, storage.ListenerSpecRow{
		ID:         "tls-listener",
		Port:       port,
		Bind:       "0.0.0.0",
		Protocols:  []string{"mqtt"},
		TLSProfile: "production",
	}); err != nil {
		t.Fatalf("InsertListenerSpec returned error: %v", err)
	}

	revision, err := service.CreateRevision(ctx, "operator", "production", "", material, false)
	if err != nil {
		t.Fatalf("CreateRevision returned error: %v", err)
	}
	result, err := service.Apply(ctx, "admin", "production", revision.RevisionID)
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if len(result.Verification) != 1 {
		t.Fatalf("Apply verification = %#v, want one check", result.Verification)
	}
	if result.Verification[0].Status != TLSStatusVerified {
		t.Fatalf("Apply verification status = %q, want %q", result.Verification[0].Status, TLSStatusVerified)
	}
}

func TestApplyReportsAnUnreachableListenerAsUnverified(t *testing.T) {
	service, repo, _ := serviceFixture(t)
	ctx := context.Background()

	if _, err := repo.InsertListenerSpec(ctx, storage.ListenerSpecRow{
		ID:         "tls-listener",
		Port:       1,
		Bind:       "0.0.0.0",
		Protocols:  []string{"mqtt"},
		TLSProfile: "production",
	}); err != nil {
		t.Fatalf("InsertListenerSpec returned error: %v", err)
	}
	revision, err := service.CreateRevision(ctx, "operator", "production", "", materialFixture(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour)), false)
	if err != nil {
		t.Fatalf("CreateRevision returned error: %v", err)
	}
	result, err := service.Apply(ctx, "admin", "production", revision.RevisionID)
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if len(result.Verification) != 1 {
		t.Fatalf("Apply verification = %#v, want one check", result.Verification)
	}
	if result.Verification[0].Status != TLSStatusUnverified {
		t.Fatalf("Apply verification status = %q, want %q", result.Verification[0].Status, TLSStatusUnverified)
	}
}

func TestResolveProfileReturnsTheBrokerPaths(t *testing.T) {
	service, _, _ := serviceFixture(t)
	ctx := context.Background()

	if _, err := service.ResolveProfile(ctx, "production"); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("ResolveProfile error = %v, want ErrProfileNotFound", err)
	}

	revision, err := service.CreateRevision(ctx, "operator", "production", "", materialFixture(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour)), false)
	if err != nil {
		t.Fatalf("CreateRevision returned error: %v", err)
	}
	if _, err := service.Apply(ctx, "admin", "production", revision.RevisionID); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	resolved, err := service.ResolveProfile(ctx, "production")
	if err != nil {
		t.Fatalf("ResolveProfile returned error: %v", err)
	}
	if !strings.HasSuffix(resolved.CertPath, "/production/active/server.crt") {
		t.Fatalf("ResolveProfile cert path = %q, want the active symlink path", resolved.CertPath)
	}
	if !strings.HasSuffix(resolved.KeyPath, "/production/active/server.key") {
		t.Fatalf("ResolveProfile key path = %q, want the active symlink path", resolved.KeyPath)
	}
	if !resolved.HasCA {
		t.Fatal("ResolveProfile did not report the certificate authority")
	}
}

func TestPreviewDescribesTheActivation(t *testing.T) {
	service, _, _ := serviceFixture(t)
	ctx := context.Background()

	revision, err := service.CreateRevision(ctx, "operator", "production", "", materialFixture(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour)), false)
	if err != nil {
		t.Fatalf("CreateRevision returned error: %v", err)
	}
	preview, err := service.Preview(ctx, "operator", "production", revision.RevisionID)
	if err != nil {
		t.Fatalf("Preview returned error: %v", err)
	}
	if preview.RevisionID != revision.RevisionID {
		t.Fatalf("Preview revision = %q, want %q", preview.RevisionID, revision.RevisionID)
	}
	if !preview.RestartRequired {
		t.Fatal("Preview did not report the broker restart")
	}
	if preview.Files.CertPath == "" || preview.Files.KeyPath == "" {
		t.Fatalf("Preview files = %#v, want the broker paths", preview.Files)
	}
	if strings.Contains(encodeJSON(t, preview), "PRIVATE KEY") {
		t.Fatal("the preview leaked private key material")
	}
}

func TestTheAuditMetadataCarriesNoPrivateKeyMaterial(t *testing.T) {
	service, _, _ := serviceFixture(t)
	ctx := context.Background()

	metadata := ""
	service.audit = func(_ context.Context, _, _, _, _, _ string, payload []byte) {
		metadata = string(payload)
	}
	input := materialFixture(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour))
	if _, err := service.CreateRevision(ctx, "operator", "production", "", input, false); err != nil {
		t.Fatalf("CreateRevision returned error: %v", err)
	}
	if metadata == "" {
		t.Fatal("CreateRevision did not record an audit event")
	}
	if strings.Contains(metadata, "PRIVATE KEY") || strings.Contains(metadata, input.ServerKeyPEM) {
		t.Fatalf("the audit metadata leaked private key material: %s", metadata)
	}
}

func TestExpiryStatusReportsTheWarningWindow(t *testing.T) {
	service, _, _ := serviceFixture(t)
	ctx := context.Background()

	expiring, err := service.CreateRevision(ctx, "operator", "expiring", "", materialFixture(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(24*time.Hour)), false)
	if err != nil {
		t.Fatalf("CreateRevision returned error: %v", err)
	}
	if expiring.ExpiryStatus != ExpiryExpiring {
		t.Fatalf("CreateRevision expiry status = %q, want %q", expiring.ExpiryStatus, ExpiryExpiring)
	}
}

// serviceFixture returns a service backed by a real database and disk store.
func serviceFixture(t *testing.T) (*Service, *storage.Store, *countingRestarter) {
	t.Helper()

	root := t.TempDir()
	repo, err := storage.Open(filepath.Join(root, "mcm.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	restarter := &countingRestarter{}
	store := certs.NewStore(filepath.Join(root, "certs"))
	service := NewService(store, repo,
		WithCertificateClock(func() time.Time { return time.Now().UTC() }),
		WithCertificateRevisionID(revisionIDFixture(t)),
		WithCertificateRestarter(restarter),
		WithCertificateBindings(repo),
		WithBrokerHost("127.0.0.1"),
		WithBrokerCertsDir("/mosquitto/config/certs"),
		WithExpiryWarningWindow(30*24*time.Hour),
		WithVerifyTimeout(2*time.Second),
		WithVerifyReadyWindow(200*time.Millisecond),
		WithVerifyRetryInterval(50*time.Millisecond),
	)
	return service, repo, restarter
}

// revisionIDFixture returns deterministic revision identifiers.
func revisionIDFixture(t *testing.T) func() (string, error) {
	t.Helper()
	counter := 0
	return func() (string, error) {
		counter++
		return "rev-" + string(rune('a'+counter-1)), nil
	}
}

// materialFixture builds a certificate authority and a server certificate.
func materialFixture(t *testing.T, commonName string, notBefore time.Time, notAfter time.Time) certs.Input {
	t.Helper()
	input, _, _ := materialWithCA(t, commonName, notBefore, notAfter)
	return input
}

// materialWithCA builds a certificate authority and a server certificate and
// returns the certificate authority so a test can sign with it.
func materialWithCA(t *testing.T, commonName string, notBefore time.Time, notAfter time.Time) (certs.Input, *x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ca key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mcm-test-root"},
		NotBefore:             notBefore.Add(-time.Hour),
		NotAfter:              notAfter.Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create ca certificate: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse ca certificate: %v", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{commonName},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}
	leafKeyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}

	return certs.Input{
		CACertPEM:     string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})),
		ServerCertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})),
		ServerKeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: leafKeyDER})),
	}, caCert, caKey
}

// startUntrustedTLSListener starts a TLS listener that serves a certificate
// signed by the given certificate authority.
//
// A profile that trusts another authority cannot verify the listener, so the
// handshake fails exactly like a broker serving the wrong material.
func startUntrustedTLSListener(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, commonName string) int {
	t.Helper()

	listener, err := tlsListen(listenerCertificate(t, ca, caKey, commonName))
	if err != nil {
		t.Fatalf("start the tls listener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if tlsConn, ok := conn.(*tls.Conn); ok {
					_ = tlsConn.Handshake()
				}
			}()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

// listenerCertificate builds a server certificate signed by the given
// certificate authority.
func listenerCertificate(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, commonName string) tls.Certificate {
	t.Helper()

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate the listener key: %v", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(99),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{commonName},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create the listener certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse the listener certificate: %v", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{certificate.Raw},
		PrivateKey:  leafKey,
		Leaf:        certificate,
	}
}

// reservePort returns a port that is free at the time of the call.
func reservePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release the reserved port: %v", err)
	}
	return port
}
