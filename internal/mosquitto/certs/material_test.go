package certs

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

type certOptions struct {
	commonName string
	dnsNames   []string
	ips        []net.IP
	notBefore  time.Time
	notAfter   time.Time
	isCA       bool
	certSign   bool
	serverAuth bool
}

func defaultOptions() certOptions {
	return certOptions{
		commonName: "broker.example.com",
		dnsNames:   []string{"broker.example.com"},
		notBefore:  testNow.Add(-24 * time.Hour),
		notAfter:   testNow.Add(90 * 24 * time.Hour),
		serverAuth: true,
	}
}

var testNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

func encodeCertPEM(t *testing.T, der []byte) string {
	t.Helper()
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func encodeKeyPEM(t *testing.T, key crypto.Signer) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func newSelfSigned(t *testing.T, opts certOptions) (*x509.Certificate, crypto.Signer, string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := templateFor(opts)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert, key, encodeCertPEM(t, der), encodeKeyPEM(t, key)
}

func newSigned(t *testing.T, opts certOptions, parent *x509.Certificate, parentKey crypto.Signer) (*x509.Certificate, crypto.Signer, string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := templateFor(opts)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, key.Public(), parentKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert, key, encodeCertPEM(t, der), encodeKeyPEM(t, key)
}

func templateFor(opts certOptions) *x509.Certificate {
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: opts.commonName},
		NotBefore:             opts.notBefore,
		NotAfter:              opts.notAfter,
		DNSNames:              opts.dnsNames,
		IPAddresses:           opts.ips,
		BasicConstraintsValid: true,
		IsCA:                  opts.isCA,
	}
	if opts.isCA {
		tmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
		if !opts.certSign {
			tmpl.KeyUsage = x509.KeyUsageCRLSign
		}
	} else {
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	}
	if opts.serverAuth {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	return tmpl
}

func kindCount(issues []Issue, kind string) int {
	count := 0
	for _, issue := range issues {
		if issue.Kind == kind {
			count++
		}
	}
	return count
}

func TestValidateAcceptsPlainServerMaterial(t *testing.T) {
	opts := defaultOptions()
	_, _, certPEM, keyPEM := newSelfSigned(t, opts)

	result := Validate(Input{
		ServerCertPEM: certPEM,
		ServerKeyPEM:  keyPEM,
		ExpectedNames: []string{"broker.example.com"},
	}, testNow, Options{})

	if len(result.Issues) != 0 {
		t.Fatalf("expected no blocking issues, got %+v", result.Issues)
	}
	if result.Metadata.Subject != "CN=broker.example.com" {
		t.Fatalf("unexpected subject %q", result.Metadata.Subject)
	}
	if result.Metadata.KeyAlgorithm != "ECDSA-P256" {
		t.Fatalf("unexpected key algorithm %q", result.Metadata.KeyAlgorithm)
	}
	if result.Metadata.HasCA || result.Metadata.HasChain {
		t.Fatal("expected no CA and no chain")
	}
	if result.Metadata.NotAfter.IsZero() || result.Metadata.NotBefore.IsZero() {
		t.Fatal("expected validity window in metadata")
	}
	if result.Metadata.FingerprintSHA256 == "" || result.Metadata.MaterialHash == "" {
		t.Fatal("expected fingerprint and material hash")
	}
}

func TestValidateAcceptsMutualTLSMaterial(t *testing.T) {
	caOpts := defaultOptions()
	caOpts.commonName = "mcm-root"
	caOpts.isCA = true
	caOpts.certSign = true
	caOpts.serverAuth = false
	caCert, caKey, caPEM, _ := newSelfSigned(t, caOpts)

	leafOpts := defaultOptions()
	leafCert, _, leafPEM, leafKeyPEM := newSigned(t, leafOpts, caCert, caKey)

	result := Validate(Input{
		CACertPEM:     caPEM,
		ServerCertPEM: leafPEM,
		ServerKeyPEM:  leafKeyPEM,
		ExpectedNames: []string{"broker.example.com"},
	}, testNow, Options{RequireCA: true})

	if len(result.Issues) != 0 {
		t.Fatalf("expected no blocking issues, got %+v", result.Issues)
	}
	if !result.Metadata.HasCA {
		t.Fatal("expected HasCA")
	}
	if result.Metadata.Issuer != caCert.Subject.String() {
		t.Fatalf("unexpected issuer %q", result.Metadata.Issuer)
	}
	if result.Metadata.SerialNumber == "" {
		t.Fatal("expected serial number")
	}
	_ = leafCert
}

func TestValidateRejectsInvalidServerCertificate(t *testing.T) {
	result := Validate(Input{ServerCertPEM: "not a pem"}, testNow, Options{})
	if !hasKind(result.Issues, "server_cert_invalid") {
		t.Fatalf("expected server_cert_invalid, got %+v", result.Issues)
	}
}

func TestValidateRejectsInvalidServerKey(t *testing.T) {
	opts := defaultOptions()
	_, _, certPEM, _ := newSelfSigned(t, opts)

	result := Validate(Input{ServerCertPEM: certPEM, ServerKeyPEM: "-----BEGIN PRIVATE KEY-----\nnope\n-----END PRIVATE KEY-----"}, testNow, Options{})
	if !hasKind(result.Issues, "server_key_invalid") {
		t.Fatalf("expected server_key_invalid, got %+v", result.Issues)
	}
}

func TestValidateRejectsKeyMismatch(t *testing.T) {
	opts := defaultOptions()
	_, _, certPEM, _ := newSelfSigned(t, opts)
	_, _, _, otherKeyPEM := newSelfSigned(t, opts)

	result := Validate(Input{ServerCertPEM: certPEM, ServerKeyPEM: otherKeyPEM}, testNow, Options{})
	if !hasKind(result.Issues, "key_mismatch") {
		t.Fatalf("expected key_mismatch, got %+v", result.Issues)
	}
}

func TestValidateRejectsExpiredCertificate(t *testing.T) {
	opts := defaultOptions()
	opts.notAfter = testNow.Add(-time.Hour)
	_, _, certPEM, keyPEM := newSelfSigned(t, opts)

	result := Validate(Input{ServerCertPEM: certPEM, ServerKeyPEM: keyPEM}, testNow, Options{})
	if !hasKind(result.Issues, "expired") {
		t.Fatalf("expected expired, got %+v", result.Issues)
	}
}

func TestValidateRejectsNotYetValidCertificate(t *testing.T) {
	opts := defaultOptions()
	opts.notBefore = testNow.Add(time.Hour)
	_, _, certPEM, keyPEM := newSelfSigned(t, opts)

	result := Validate(Input{ServerCertPEM: certPEM, ServerKeyPEM: keyPEM}, testNow, Options{})
	if !hasKind(result.Issues, "not_yet_valid") {
		t.Fatalf("expected not_yet_valid, got %+v", result.Issues)
	}
}

func TestValidateRejectsInvalidCA(t *testing.T) {
	opts := defaultOptions()
	_, _, certPEM, keyPEM := newSelfSigned(t, opts)

	result := Validate(Input{CACertPEM: "garbage", ServerCertPEM: certPEM, ServerKeyPEM: keyPEM}, testNow, Options{})
	if !hasKind(result.Issues, "ca_invalid") {
		t.Fatalf("expected ca_invalid, got %+v", result.Issues)
	}
}

func TestValidateRejectsNonCACertificate(t *testing.T) {
	caOpts := defaultOptions()
	caOpts.commonName = "not-a-ca"
	_, _, caPEM, _ := newSelfSigned(t, caOpts)

	opts := defaultOptions()
	_, _, certPEM, keyPEM := newSelfSigned(t, opts)

	result := Validate(Input{CACertPEM: caPEM, ServerCertPEM: certPEM, ServerKeyPEM: keyPEM}, testNow, Options{})
	if !hasKind(result.Issues, "ca_not_ca") {
		t.Fatalf("expected ca_not_ca, got %+v", result.Issues)
	}
}

func TestValidateRejectsExpiredCA(t *testing.T) {
	caOpts := defaultOptions()
	caOpts.commonName = "mcm-root"
	caOpts.isCA = true
	caOpts.certSign = true
	caOpts.serverAuth = false
	caOpts.notAfter = testNow.Add(-time.Hour)
	_, _, caPEM, _ := newSelfSigned(t, caOpts)

	opts := defaultOptions()
	_, _, certPEM, keyPEM := newSelfSigned(t, opts)

	result := Validate(Input{CACertPEM: caPEM, ServerCertPEM: certPEM, ServerKeyPEM: keyPEM}, testNow, Options{})
	if !hasKind(result.Issues, "ca_expired") {
		t.Fatalf("expected ca_expired, got %+v", result.Issues)
	}
}

func TestValidateRejectsInvalidChain(t *testing.T) {
	opts := defaultOptions()
	_, _, certPEM, keyPEM := newSelfSigned(t, opts)

	result := Validate(Input{ChainPEM: "not a pem", ServerCertPEM: certPEM, ServerKeyPEM: keyPEM}, testNow, Options{})
	if !hasKind(result.Issues, "chain_invalid") {
		t.Fatalf("expected chain_invalid, got %+v", result.Issues)
	}
}

func TestValidateRejectsExpiredChain(t *testing.T) {
	caOpts := defaultOptions()
	caOpts.commonName = "mcm-root"
	caOpts.isCA = true
	caOpts.certSign = true
	caOpts.serverAuth = false
	caCert, caKey, _, _ := newSelfSigned(t, caOpts)

	interOpts := defaultOptions()
	interOpts.commonName = "mcm-intermediate"
	interOpts.isCA = true
	interOpts.certSign = true
	interOpts.serverAuth = false
	interOpts.notAfter = testNow.Add(-time.Hour)
	_, _, interPEM, _ := newSigned(t, interOpts, caCert, caKey)

	leafOpts := defaultOptions()
	_, _, leafPEM, leafKeyPEM := newSigned(t, leafOpts, caCert, caKey)

	result := Validate(Input{
		CACertPEM:     encodeCertPEM(t, caCert.Raw),
		ChainPEM:      interPEM,
		ServerCertPEM: leafPEM,
		ServerKeyPEM:  leafKeyPEM,
	}, testNow, Options{})
	if !hasKind(result.Issues, "chain_expired") {
		t.Fatalf("expected chain_expired, got %+v", result.Issues)
	}
}

func TestValidateRejectsUnverifiedChain(t *testing.T) {
	caOpts := defaultOptions()
	caOpts.commonName = "mcm-root"
	caOpts.isCA = true
	caOpts.certSign = true
	caOpts.serverAuth = false
	_, _, caPEM, _ := newSelfSigned(t, caOpts)

	otherCAOpts := caOpts
	otherCAOpts.commonName = "other-root"
	otherCACert, otherCAKey, _, _ := newSelfSigned(t, otherCAOpts)

	interOpts := defaultOptions()
	interOpts.commonName = "mcm-intermediate"
	interOpts.isCA = true
	interOpts.certSign = true
	interOpts.serverAuth = false
	interCert, interKey, interPEM, _ := newSigned(t, interOpts, otherCACert, otherCAKey)

	leafOpts := defaultOptions()
	_, _, leafPEM, leafKeyPEM := newSigned(t, leafOpts, interCert, interKey)

	result := Validate(Input{
		CACertPEM:     caPEM,
		ChainPEM:      interPEM,
		ServerCertPEM: leafPEM,
		ServerKeyPEM:  leafKeyPEM,
	}, testNow, Options{})
	if !hasKind(result.Issues, "chain_unverified") {
		t.Fatalf("expected chain_unverified, got %+v", result.Issues)
	}
}

func TestValidateAcceptsVerifiedChain(t *testing.T) {
	caOpts := defaultOptions()
	caOpts.commonName = "mcm-root"
	caOpts.isCA = true
	caOpts.certSign = true
	caOpts.serverAuth = false
	caCert, caKey, caPEM, _ := newSelfSigned(t, caOpts)

	interOpts := defaultOptions()
	interOpts.commonName = "mcm-intermediate"
	interOpts.isCA = true
	interOpts.certSign = true
	interOpts.serverAuth = false
	interCert, interKey, interPEM, _ := newSigned(t, interOpts, caCert, caKey)

	leafOpts := defaultOptions()
	_, _, leafPEM, leafKeyPEM := newSigned(t, leafOpts, interCert, interKey)

	result := Validate(Input{
		CACertPEM:     caPEM,
		ChainPEM:      interPEM,
		ServerCertPEM: leafPEM,
		ServerKeyPEM:  leafKeyPEM,
		ExpectedNames: []string{"broker.example.com"},
	}, testNow, Options{RequireCA: true})
	if len(result.Issues) != 0 {
		t.Fatalf("expected no blocking issues, got %+v", result.Issues)
	}
	if !result.Metadata.HasChain {
		t.Fatal("expected HasChain")
	}
}

func TestValidateRejectsUncoveredName(t *testing.T) {
	opts := defaultOptions()
	opts.dnsNames = []string{"broker.example.com"}
	_, _, certPEM, keyPEM := newSelfSigned(t, opts)

	result := Validate(Input{
		ServerCertPEM: certPEM,
		ServerKeyPEM:  keyPEM,
		ExpectedNames: []string{"other.example.com"},
	}, testNow, Options{})
	if kindCount(result.Issues, "name_not_covered") != 1 {
		t.Fatalf("expected one name_not_covered, got %+v", result.Issues)
	}
	if !strings.Contains(result.Issues[0].Message, "other.example.com") {
		t.Fatalf("expected the message to name the missing entry, got %q", result.Issues[0].Message)
	}
}

func TestValidateWildcardDoesNotCoverName(t *testing.T) {
	opts := defaultOptions()
	opts.dnsNames = []string{"*.example.com"}
	_, _, certPEM, keyPEM := newSelfSigned(t, opts)

	result := Validate(Input{
		ServerCertPEM: certPEM,
		ServerKeyPEM:  keyPEM,
		ExpectedNames: []string{"broker.example.com", "*.example.com"},
	}, testNow, Options{})
	if kindCount(result.Issues, "name_not_covered") != 1 {
		t.Fatalf("expected one name_not_covered, got %+v", result.Issues)
	}
}

func TestValidateCoversIPAddressName(t *testing.T) {
	opts := defaultOptions()
	opts.dnsNames = nil
	opts.ips = []net.IP{net.ParseIP("10.0.0.10")}
	_, _, certPEM, keyPEM := newSelfSigned(t, opts)

	result := Validate(Input{
		ServerCertPEM: certPEM,
		ServerKeyPEM:  keyPEM,
		ExpectedNames: []string{"10.0.0.10"},
	}, testNow, Options{})
	if len(result.Issues) != 0 {
		t.Fatalf("expected no blocking issues, got %+v", result.Issues)
	}
	if len(result.Metadata.IPAddresses) != 1 || result.Metadata.IPAddresses[0] != "10.0.0.10" {
		t.Fatalf("unexpected ip addresses %+v", result.Metadata.IPAddresses)
	}
}

func TestValidateRequiresCAWhenRequested(t *testing.T) {
	opts := defaultOptions()
	_, _, certPEM, keyPEM := newSelfSigned(t, opts)

	result := Validate(Input{ServerCertPEM: certPEM, ServerKeyPEM: keyPEM}, testNow, Options{RequireCA: true})
	if !hasKind(result.Issues, "ca_required") {
		t.Fatalf("expected ca_required, got %+v", result.Issues)
	}
}

func TestValidateWarnsOnCAWithoutCertSign(t *testing.T) {
	caOpts := defaultOptions()
	caOpts.commonName = "mcm-root"
	caOpts.isCA = true
	caOpts.certSign = false
	caOpts.serverAuth = false
	_, _, caPEM, _ := newSelfSigned(t, caOpts)

	opts := defaultOptions()
	_, _, certPEM, keyPEM := newSelfSigned(t, opts)

	result := Validate(Input{CACertPEM: caPEM, ServerCertPEM: certPEM, ServerKeyPEM: keyPEM}, testNow, Options{})
	if !hasKind(result.Warnings, "ca_missing_cert_sign") {
		t.Fatalf("expected ca_missing_cert_sign warning, got %+v", result.Warnings)
	}
}

func TestValidateWarnsOnCommonNameOnly(t *testing.T) {
	opts := defaultOptions()
	opts.dnsNames = nil
	opts.commonName = "broker.example.com"
	_, _, certPEM, keyPEM := newSelfSigned(t, opts)

	result := Validate(Input{
		ServerCertPEM: certPEM,
		ServerKeyPEM:  keyPEM,
		ExpectedNames: []string{"broker.example.com"},
	}, testNow, Options{})
	if len(result.Issues) != 0 {
		t.Fatalf("expected no blocking issues, got %+v", result.Issues)
	}
	if !hasKind(result.Warnings, "name_from_common_name") {
		t.Fatalf("expected name_from_common_name warning, got %+v", result.Warnings)
	}
}

func TestValidateWarnsOnExpiringSoon(t *testing.T) {
	opts := defaultOptions()
	opts.notAfter = testNow.Add(10 * 24 * time.Hour)
	_, _, certPEM, keyPEM := newSelfSigned(t, opts)

	result := Validate(Input{ServerCertPEM: certPEM, ServerKeyPEM: keyPEM}, testNow, Options{})
	if !hasKind(result.Warnings, "expiring_soon") {
		t.Fatalf("expected expiring_soon warning, got %+v", result.Warnings)
	}
	if !strings.Contains(result.Warnings[0].Message, "10") {
		t.Fatalf("expected the warning to state the days left, got %q", result.Warnings[0].Message)
	}
}

func TestValidateWarnsOnCAExpiringSoon(t *testing.T) {
	caOpts := defaultOptions()
	caOpts.commonName = "mcm-root"
	caOpts.isCA = true
	caOpts.certSign = true
	caOpts.serverAuth = false
	caOpts.notAfter = testNow.Add(5 * 24 * time.Hour)
	_, _, caPEM, _ := newSelfSigned(t, caOpts)

	opts := defaultOptions()
	_, _, certPEM, keyPEM := newSelfSigned(t, opts)

	result := Validate(Input{CACertPEM: caPEM, ServerCertPEM: certPEM, ServerKeyPEM: keyPEM}, testNow, Options{})
	if !hasKind(result.Warnings, "ca_expiring_soon") {
		t.Fatalf("expected ca_expiring_soon warning, got %+v", result.Warnings)
	}
}

func TestValidateMaterialHashCoversPublicMaterialOnly(t *testing.T) {
	caOpts := defaultOptions()
	caOpts.commonName = "mcm-root"
	caOpts.isCA = true
	caOpts.certSign = true
	caOpts.serverAuth = false
	caCert, caKey, caPEM, _ := newSelfSigned(t, caOpts)

	leafOpts := defaultOptions()
	leafCert, _, leafPEM, leafKeyPEM := newSigned(t, leafOpts, caCert, caKey)

	result := Validate(Input{
		CACertPEM:     caPEM,
		ServerCertPEM: leafPEM,
		ServerKeyPEM:  leafKeyPEM,
	}, testNow, Options{})

	digest := sha256.New()
	digest.Write(leafCert.Raw)
	digest.Write(caCert.Raw)
	expected := hex.EncodeToString(digest.Sum(nil))
	if result.Metadata.MaterialHash != expected {
		t.Fatalf("expected material hash %s, got %s", expected, result.Metadata.MaterialHash)
	}
}

func TestValidateNeverLeaksKeyMaterial(t *testing.T) {
	opts := defaultOptions()
	_, _, certPEM, keyPEM := newSelfSigned(t, opts)

	result := Validate(Input{
		ServerCertPEM: certPEM,
		ServerKeyPEM:  keyPEM,
		ExpectedNames: []string{"broker.example.com"},
	}, testNow, Options{})

	blob := strings.Join([]string{
		result.Metadata.Subject,
		result.Metadata.Issuer,
		result.Metadata.FingerprintSHA256,
		result.Metadata.MaterialHash,
		result.Metadata.KeyAlgorithm,
		result.Metadata.SerialNumber,
	}, " ")
	for _, issue := range append(append([]Issue{}, result.Issues...), result.Warnings...) {
		blob += " " + issue.Kind + " " + issue.Field + " " + issue.Message
	}
	for _, marker := range []string{"PRIVATE KEY", "BEGIN", "MII", keyPEM} {
		if strings.Contains(blob, marker) {
			t.Fatalf("result leaked key material marker %q", marker)
		}
	}
}

func TestParseCertificatesPEMRejectsGarbage(t *testing.T) {
	if _, err := ParseCertificatesPEM("nope"); err == nil {
		t.Fatal("expected an error for a bundle without certificates")
	}
}

func TestParseCertificatesPEMReturnsEveryCertificate(t *testing.T) {
	caOpts := defaultOptions()
	caOpts.isCA = true
	caOpts.certSign = true
	caOpts.serverAuth = false
	caCert, caKey, caPEM, _ := newSelfSigned(t, caOpts)

	interOpts := defaultOptions()
	interOpts.isCA = true
	interOpts.certSign = true
	interOpts.serverAuth = false
	interCert, interKey, interPEM, _ := newSigned(t, interOpts, caCert, caKey)

	leafOpts := defaultOptions()
	_, _, leafPEM, _ := newSigned(t, leafOpts, interCert, interKey)

	certs, err := ParseCertificatesPEM(caPEM + interPEM + leafPEM)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(certs) != 3 {
		t.Fatalf("expected 3 certificates, got %d", len(certs))
	}
}

func TestParsePrivateKeyPEMSupportsCommonEncodings(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pkcs8 := encodeKeyPEM(t, key)
	if _, err := ParsePrivateKeyPEM(pkcs8); err != nil {
		t.Fatalf("expected PKCS#8 key to parse: %v", err)
	}

	pkcs1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	if _, err := ParsePrivateKeyPEM(pkcs1); err != nil {
		t.Fatalf("expected PKCS#1 key to parse: %v", err)
	}

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ec key: %v", err)
	}
	sec1, err := x509.MarshalECPrivateKey(ecKey)
	if err != nil {
		t.Fatalf("marshal ec key: %v", err)
	}
	sec1PEM := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sec1}))
	if _, err := ParsePrivateKeyPEM(sec1PEM); err != nil {
		t.Fatalf("expected SEC1 key to parse: %v", err)
	}

	if _, err := ParsePrivateKeyPEM("not a pem"); err == nil {
		t.Fatal("expected an error for a non PEM key")
	}
}

func TestKeyAlgorithmNamesRSA(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if got := keyAlgorithm(&key.PublicKey); got != "RSA-2048" {
		t.Fatalf("expected RSA-2048, got %q", got)
	}
}
