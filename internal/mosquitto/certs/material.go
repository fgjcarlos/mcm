// Package certs validates broker TLS material and extracts the non-secret
// metadata the web UI needs to describe a certificate profile.
//
// The package never logs and never returns PEM material inside an Issue
// message or a Metadata field: the private key is write-only material that
// exists only between the upload request and the encrypted-free disk store.
package certs

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Input carries the PEM material submitted for one certificate profile.
//
// CACertPEM is optional for plain TLS and mandatory for mutual TLS.
// ChainPEM may hold several intermediate certificates.
type Input struct {
	CACertPEM     string
	ServerCertPEM string
	ServerKeyPEM  string
	ChainPEM      string
	ExpectedNames []string
}

// Metadata describes the public part of a certificate profile.
//
// It never contains private key material: FingerprintSHA256 and MaterialHash
// are computed over public DER bytes only.
type Metadata struct {
	Subject           string
	Issuer            string
	DNSNames          []string
	IPAddresses       []string
	NotBefore         time.Time
	NotAfter          time.Time
	FingerprintSHA256 string
	KeyAlgorithm      string
	IsCA              bool
	SerialNumber      string
	HasCA             bool
	HasChain          bool
	MaterialHash      string
}

// Issue describes one validation finding.
//
// Kind is a stable machine-readable identifier; Field points at the material
// that caused it. Blocking problems are reported in Result.Issues and
// non-blocking concerns in Result.Warnings.
type Issue struct {
	Kind    string
	Field   string
	Message string
}

// Options tunes the validation rules.
type Options struct {
	// ExpiryWarningWindow is the horizon used to raise the expiring_soon and
	// ca_expiring_soon warnings. It defaults to 30 days.
	ExpiryWarningWindow time.Duration
	// RequireCA demands a certificate authority, which is what mutual TLS
	// needs to verify client certificates.
	RequireCA bool
}

// Result is the outcome of one validation run.
type Result struct {
	Metadata Metadata
	Issues   []Issue
	Warnings []Issue
}

// defaultExpiryWarningWindow is the horizon used when the caller does not
// configure one.
const defaultExpiryWarningWindow = 30 * 24 * time.Hour

// Validate parses and validates the submitted material.
//
// Invalid user input is never returned as an error: it is reported as
// blocking Issues so the API can render every problem at once. An error is
// reserved for programming errors such as an unusable clock.
func Validate(in Input, now time.Time, opts Options) Result {
	if now.IsZero() {
		now = time.Now()
	}
	if opts.ExpiryWarningWindow <= 0 {
		opts.ExpiryWarningWindow = defaultExpiryWarningWindow
	}

	result := Result{}

	leaf := parseLeaf(in.ServerCertPEM, &result)
	key := parseKey(in.ServerKeyPEM, &result)
	ca := parseCA(in.CACertPEM, now, opts, &result)
	chain := parseChain(in.ChainPEM, now, &result)

	if leaf != nil && key != nil && !publicKeysMatch(leaf.PublicKey, key) {
		result.Issues = append(result.Issues, Issue{
			Kind:    "key_mismatch",
			Field:   "server_key",
			Message: "the private key does not match the server certificate public key",
		})
	}

	if leaf != nil {
		if !now.Before(leaf.NotAfter) {
			result.Issues = append(result.Issues, Issue{
				Kind:    "expired",
				Field:   "server_cert",
				Message: fmt.Sprintf("the server certificate expired on %s", leaf.NotAfter.UTC().Format(time.RFC3339)),
			})
		}
		if now.Before(leaf.NotBefore) {
			result.Issues = append(result.Issues, Issue{
				Kind:    "not_yet_valid",
				Field:   "server_cert",
				Message: fmt.Sprintf("the server certificate is not valid before %s", leaf.NotBefore.UTC().Format(time.RFC3339)),
			})
		}
	}

	if opts.RequireCA && ca == nil && !hasKind(result.Issues, "ca_invalid") {
		result.Issues = append(result.Issues, Issue{
			Kind:    "ca_required",
			Field:   "ca",
			Message: "a certificate authority is required for mutual TLS",
		})
	}

	verifyChain(leaf, ca, chain, now, &result)
	checkNames(leaf, in.ExpectedNames, &result)
	warnExpiry(leaf, ca, now, opts.ExpiryWarningWindow, &result)

	result.Metadata = buildMetadata(leaf, ca, chain, key)
	return result
}

// ParseCertificatesPEM decodes every CERTIFICATE block of a PEM bundle.
//
// It returns an error when the bundle holds no certificate.
func ParseCertificatesPEM(bundle string) ([]*x509.Certificate, error) {
	rest := []byte(bundle)
	certificates := make([]*x509.Certificate, 0, 2)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse certificate: %w", err)
		}
		certificates = append(certificates, certificate)
	}
	if len(certificates) == 0 {
		return nil, errors.New("no PEM certificate found")
	}
	return certificates, nil
}

// ParsePrivateKeyPEM decodes a single PEM private key block.
//
// PKCS#8, PKCS#1 and SEC1 encodings are supported.
func ParsePrivateKeyPEM(pemText string) (crypto.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	switch block.Type {
	case "PRIVATE KEY":
		return x509.ParsePKCS8PrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("unsupported PEM block type %q", block.Type)
	}
}

func parseLeaf(certPEM string, result *Result) *x509.Certificate {
	if strings.TrimSpace(certPEM) == "" {
		result.Issues = append(result.Issues, Issue{
			Kind:    "server_cert_invalid",
			Field:   "server_cert",
			Message: "the server certificate is required",
		})
		return nil
	}
	certificates, err := ParseCertificatesPEM(certPEM)
	if err != nil {
		result.Issues = append(result.Issues, Issue{
			Kind:    "server_cert_invalid",
			Field:   "server_cert",
			Message: "the server certificate is not a valid PEM certificate",
		})
		return nil
	}
	return certificates[0]
}

func parseKey(keyPEM string, result *Result) crypto.PrivateKey {
	if strings.TrimSpace(keyPEM) == "" {
		result.Issues = append(result.Issues, Issue{
			Kind:    "server_key_invalid",
			Field:   "server_key",
			Message: "the server private key is required",
		})
		return nil
	}
	key, err := ParsePrivateKeyPEM(keyPEM)
	if err != nil {
		result.Issues = append(result.Issues, Issue{
			Kind:    "server_key_invalid",
			Field:   "server_key",
			Message: "the server private key is not a valid PEM private key",
		})
		return nil
	}
	return key
}

func parseCA(caPEM string, now time.Time, opts Options, result *Result) *x509.Certificate {
	if strings.TrimSpace(caPEM) == "" {
		return nil
	}
	certificates, err := ParseCertificatesPEM(caPEM)
	if err != nil || len(certificates) == 0 {
		result.Issues = append(result.Issues, Issue{
			Kind:    "ca_invalid",
			Field:   "ca",
			Message: "the certificate authority is not a valid PEM certificate",
		})
		return nil
	}
	ca := certificates[0]
	if !ca.IsCA {
		result.Issues = append(result.Issues, Issue{
			Kind:    "ca_not_ca",
			Field:   "ca",
			Message: "the certificate authority is not a CA certificate",
		})
	}
	if ca.KeyUsage != 0 && ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		result.Warnings = append(result.Warnings, Issue{
			Kind:    "ca_missing_cert_sign",
			Field:   "ca",
			Message: "the certificate authority does not allow certificate signing",
		})
	}
	if !now.Before(ca.NotAfter) {
		result.Issues = append(result.Issues, Issue{
			Kind:    "ca_expired",
			Field:   "ca",
			Message: fmt.Sprintf("the certificate authority expired on %s", ca.NotAfter.UTC().Format(time.RFC3339)),
		})
	}
	if now.Before(ca.NotBefore) {
		result.Issues = append(result.Issues, Issue{
			Kind:    "ca_not_yet_valid",
			Field:   "ca",
			Message: fmt.Sprintf("the certificate authority is not valid before %s", ca.NotBefore.UTC().Format(time.RFC3339)),
		})
	}
	return ca
}

func parseChain(chainPEM string, now time.Time, result *Result) []*x509.Certificate {
	if strings.TrimSpace(chainPEM) == "" {
		return nil
	}
	certificates, err := ParseCertificatesPEM(chainPEM)
	if err != nil || len(certificates) == 0 {
		result.Issues = append(result.Issues, Issue{
			Kind:    "chain_invalid",
			Field:   "chain",
			Message: "the certificate chain is not a valid PEM certificate bundle",
		})
		return nil
	}
	for _, certificate := range certificates {
		if !now.Before(certificate.NotAfter) {
			result.Issues = append(result.Issues, Issue{
				Kind:    "chain_expired",
				Field:   "chain",
				Message: fmt.Sprintf("a chain certificate expired on %s", certificate.NotAfter.UTC().Format(time.RFC3339)),
			})
		}
	}
	return certificates
}

// verifyChain checks that the leaf certificate chains up to the supplied CA
// through the supplied intermediates.
func verifyChain(leaf *x509.Certificate, ca *x509.Certificate, chain []*x509.Certificate, now time.Time, result *Result) {
	if leaf == nil || ca == nil || len(chain) == 0 {
		return
	}
	if hasKind(result.Issues, "expired") || hasKind(result.Issues, "not_yet_valid") ||
		hasKind(result.Issues, "ca_expired") || hasKind(result.Issues, "ca_not_yet_valid") {
		return
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	intermediates := x509.NewCertPool()
	for _, certificate := range chain {
		intermediates.AddCert(certificate)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		result.Issues = append(result.Issues, Issue{
			Kind:    "chain_unverified",
			Field:   "chain",
			Message: fmt.Sprintf("the certificate chain does not verify against the certificate authority: %v", err),
		})
	}
}

// checkNames verifies that the leaf certificate covers every expected name.
//
// Wildcards never cover a name: the comparison is an exact, case-insensitive
// match against the SAN entries.
func checkNames(leaf *x509.Certificate, expectedNames []string, result *Result) {
	if leaf == nil {
		return
	}
	seen := make(map[string]bool, len(expectedNames))
	usedCommonName := false
	for _, raw := range expectedNames {
		name := strings.TrimSpace(raw)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true

		if ip := net.ParseIP(name); ip != nil {
			if !containsIP(leaf.IPAddresses, ip) {
				result.Issues = append(result.Issues, Issue{
					Kind:    "name_not_covered",
					Field:   "names",
					Message: fmt.Sprintf("the server certificate does not cover the IP address %q", name),
				})
			}
			continue
		}

		if containsDNS(leaf.DNSNames, name) {
			continue
		}
		if len(leaf.DNSNames) == 0 && len(leaf.IPAddresses) == 0 &&
			strings.EqualFold(leaf.Subject.CommonName, name) {
			usedCommonName = true
			continue
		}
		result.Issues = append(result.Issues, Issue{
			Kind:    "name_not_covered",
			Field:   "names",
			Message: fmt.Sprintf("the server certificate does not cover the name %q", name),
		})
	}
	if usedCommonName {
		result.Warnings = append(result.Warnings, Issue{
			Kind:    "name_from_common_name",
			Field:   "names",
			Message: "the server certificate has no subject alternative names; the common name was used instead",
		})
	}
}

func warnExpiry(leaf *x509.Certificate, ca *x509.Certificate, now time.Time, window time.Duration, result *Result) {
	if leaf != nil && leaf.NotAfter.After(now) && leaf.NotAfter.Sub(now) <= window {
		days := int(leaf.NotAfter.Sub(now).Hours() / 24)
		result.Warnings = append(result.Warnings, Issue{
			Kind:    "expiring_soon",
			Field:   "server_cert",
			Message: fmt.Sprintf("the server certificate expires in %d days", days),
		})
	}
	if ca != nil && ca.NotAfter.After(now) && ca.NotAfter.Sub(now) <= window {
		days := int(ca.NotAfter.Sub(now).Hours() / 24)
		result.Warnings = append(result.Warnings, Issue{
			Kind:    "ca_expiring_soon",
			Field:   "ca",
			Message: fmt.Sprintf("the certificate authority expires in %d days", days),
		})
	}
}

func buildMetadata(leaf *x509.Certificate, ca *x509.Certificate, chain []*x509.Certificate, key crypto.PrivateKey) Metadata {
	metadata := Metadata{HasCA: ca != nil, HasChain: len(chain) > 0}
	if leaf == nil {
		return metadata
	}
	metadata.Subject = leaf.Subject.String()
	metadata.Issuer = leaf.Issuer.String()
	metadata.DNSNames = append([]string{}, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		metadata.IPAddresses = append(metadata.IPAddresses, ip.String())
	}
	metadata.NotBefore = leaf.NotBefore.UTC()
	metadata.NotAfter = leaf.NotAfter.UTC()
	metadata.FingerprintSHA256 = fingerprintSHA256(leaf.Raw)
	metadata.KeyAlgorithm = keyAlgorithm(leaf.PublicKey)
	metadata.IsCA = leaf.IsCA
	metadata.SerialNumber = leaf.SerialNumber.String()
	metadata.MaterialHash = materialHash(leaf, ca, chain)
	return metadata
}

// fingerprintSHA256 returns the lowercase hex SHA-256 of the given DER bytes.
func fingerprintSHA256(der []byte) string {
	digest := sha256.Sum256(der)
	return hex.EncodeToString(digest[:])
}

// materialHash returns the lowercase hex SHA-256 of the public material.
//
// The private key is never part of the digest.
func materialHash(leaf *x509.Certificate, ca *x509.Certificate, chain []*x509.Certificate) string {
	digest := sha256.New()
	if leaf != nil {
		digest.Write(leaf.Raw)
	}
	if ca != nil {
		digest.Write(ca.Raw)
	}
	for _, certificate := range chain {
		digest.Write(certificate.Raw)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// keyAlgorithm names the public key algorithm of a certificate.
func keyAlgorithm(publicKey crypto.PublicKey) string {
	switch key := publicKey.(type) {
	case *rsa.PublicKey:
		return fmt.Sprintf("RSA-%d", key.N.BitLen())
	case *ecdsa.PublicKey:
		size := key.Curve.Params().BitSize
		switch size {
		case 256, 384, 521:
			return fmt.Sprintf("ECDSA-P%d", size)
		default:
			return fmt.Sprintf("ECDSA-P%d", size)
		}
	case ed25519.PublicKey:
		return "Ed25519"
	default:
		return "unknown"
	}
}

// publicKeysMatch reports whether a private key holds the public key of a
// certificate.
func publicKeysMatch(publicKey crypto.PublicKey, privateKey crypto.PrivateKey) bool {
	signer, ok := privateKey.(crypto.Signer)
	if !ok {
		return false
	}
	candidate, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return false
	}
	expected, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return false
	}
	return string(candidate) == string(expected)
}

func containsDNS(names []string, name string) bool {
	for _, candidate := range names {
		if strings.EqualFold(candidate, name) {
			return true
		}
	}
	return false
}

func containsIP(ips []net.IP, ip net.IP) bool {
	for _, candidate := range ips {
		if candidate.Equal(ip) {
			return true
		}
	}
	return false
}

func hasKind(issues []Issue, kind string) bool {
	for _, issue := range issues {
		if issue.Kind == kind {
			return true
		}
	}
	return false
}
