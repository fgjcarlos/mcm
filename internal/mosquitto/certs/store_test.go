package certs

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func storeFixture(t *testing.T) (*Store, Input) {
	t.Helper()
	root := t.TempDir()

	caOpts := defaultOptions()
	caOpts.commonName = "mcm-root"
	caOpts.isCA = true
	caOpts.certSign = true
	caOpts.serverAuth = false
	caCert, caKey, caPEM, _ := newSelfSigned(t, caOpts)

	leafOpts := defaultOptions()
	_, _, leafPEM, leafKeyPEM := newSigned(t, leafOpts, caCert, caKey)

	_ = caKey
	return NewStore(root), Input{
		CACertPEM:     caPEM,
		ServerCertPEM: leafPEM,
		ServerKeyPEM:  leafKeyPEM,
	}
}

func TestSaveRevisionWritesCanonicalMaterial(t *testing.T) {
	store, input := storeFixture(t)

	files, err := store.SaveRevision("production", "rev-1", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if files.CertPath == "" || files.KeyPath == "" || files.CAPath == "" {
		t.Fatalf("expected cert, key and ca paths, got %+v", files)
	}
	if files.ChainPath != "" {
		t.Fatalf("expected no chain path, got %q", files.ChainPath)
	}

	keyInfo, err := os.Stat(files.KeyPath)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if keyInfo.Mode().Perm() != keyFilePerm {
		t.Fatalf("expected key mode %v, got %v", keyFilePerm, keyInfo.Mode().Perm())
	}
	certInfo, err := os.Stat(files.CertPath)
	if err != nil {
		t.Fatalf("stat cert: %v", err)
	}
	if certInfo.Mode().Perm() != certFilePerm {
		t.Fatalf("expected cert mode %v, got %v", certFilePerm, certInfo.Mode().Perm())
	}

	keyBytes, err := os.ReadFile(files.KeyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	if !strings.Contains(string(keyBytes), "BEGIN PRIVATE KEY") {
		t.Fatalf("expected a PKCS#8 key, got %q", string(keyBytes))
	}

	certBytes, err := os.ReadFile(files.CertPath)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	if strings.Count(string(certBytes), "BEGIN CERTIFICATE") != 1 {
		t.Fatalf("expected exactly one certificate block, got %q", string(certBytes))
	}
}

func TestSaveRevisionDropsStrayPEMBlocks(t *testing.T) {
	store, input := storeFixture(t)
	input.ServerCertPEM = input.ServerCertPEM + "\n" + strings.Repeat("not a certificate\n", 3)

	files, err := store.SaveRevision("production", "rev-1", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	certBytes, err := os.ReadFile(files.CertPath)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	if strings.Count(string(certBytes), "BEGIN CERTIFICATE") != 1 {
		t.Fatalf("expected only the parsed certificate to be stored, got %q", string(certBytes))
	}
}

func TestSaveRevisionRejectsUnsafeNames(t *testing.T) {
	store, input := storeFixture(t)

	for _, profile := range []string{"../escape", "nested/profile", "..", "", "profile/../.."} {
		if _, err := store.SaveRevision(profile, "rev-1", input); err == nil {
			t.Fatalf("expected an error for profile %q", profile)
		}
	}
	for _, revision := range []string{"../escape", "revisions/../../etc", "", "."} {
		if _, err := store.SaveRevision("production", revision, input); err == nil {
			t.Fatalf("expected an error for revision %q", revision)
		}
	}
}

func TestSaveRevisionKeepsRevisionsImmutable(t *testing.T) {
	store, input := storeFixture(t)
	if _, err := store.SaveRevision("production", "rev-1", input); err != nil {
		t.Fatalf("save rev-1: %v", err)
	}
	if _, err := store.SaveRevision("production", "rev-1", input); err == nil {
		t.Fatal("expected an error when a revision id is reused")
	}
}

func TestSaveRevisionRejectsMismatchedKey(t *testing.T) {
	store, input := storeFixture(t)
	otherOpts := defaultOptions()
	_, _, _, otherKey := newSelfSigned(t, otherOpts)
	input.ServerKeyPEM = otherKey

	if _, err := store.SaveRevision("production", "rev-1", input); err == nil {
		t.Fatal("expected an error for a mismatched key")
	}
}

func TestActivateSwitchesTheActiveMaterial(t *testing.T) {
	store, input := storeFixture(t)
	if _, err := store.SaveRevision("production", "rev-1", input); err != nil {
		t.Fatalf("save rev-1: %v", err)
	}
	if _, err := store.SaveRevision("production", "rev-2", input); err != nil {
		t.Fatalf("save rev-2: %v", err)
	}

	if err := store.Activate("production", "rev-1"); err != nil {
		t.Fatalf("activate rev-1: %v", err)
	}
	active, err := store.ActiveRevision("production")
	if err != nil {
		t.Fatalf("active revision: %v", err)
	}
	if active != "rev-1" {
		t.Fatalf("expected rev-1, got %q", active)
	}

	if err := store.Activate("production", "rev-2"); err != nil {
		t.Fatalf("activate rev-2: %v", err)
	}
	active, err = store.ActiveRevision("production")
	if err != nil {
		t.Fatalf("active revision: %v", err)
	}
	if active != "rev-2" {
		t.Fatalf("expected rev-2, got %q", active)
	}

	files, err := store.ActiveFiles("production")
	if err != nil {
		t.Fatalf("active files: %v", err)
	}
	if !strings.Contains(files.CertPath, string(filepath.Separator)+"rev-2"+string(filepath.Separator)) {
		t.Fatalf("expected the active files to point at rev-2, got %q", files.CertPath)
	}
}

func TestActivateUsesARelativeLink(t *testing.T) {
	store, input := storeFixture(t)
	if _, err := store.SaveRevision("production", "rev-1", input); err != nil {
		t.Fatalf("save rev-1: %v", err)
	}
	if err := store.Activate("production", "rev-1"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	target, err := os.Readlink(filepath.Join(store.Root(), "production", "active"))
	if err != nil {
		t.Fatalf("read link: %v", err)
	}
	if filepath.IsAbs(target) {
		t.Fatalf("expected a relative link, got %q", target)
	}
	if target != filepath.Join("revisions", "rev-1") {
		t.Fatalf("unexpected link target %q", target)
	}
}

func TestActivateRejectsUnknownRevision(t *testing.T) {
	store, _ := storeFixture(t)
	if err := store.Activate("production", "rev-404"); err == nil {
		t.Fatal("expected an error for an unknown revision")
	}
}

func TestDeactivateClearsTheActiveLink(t *testing.T) {
	store, input := storeFixture(t)
	if _, err := store.SaveRevision("production", "rev-1", input); err != nil {
		t.Fatalf("save rev-1: %v", err)
	}
	if err := store.Activate("production", "rev-1"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := store.Deactivate("production"); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if _, err := store.ActiveRevision("production"); err == nil {
		t.Fatal("expected no active revision after the deactivation")
	}
	if err := store.Deactivate("production"); err != nil {
		t.Fatalf("expected the deactivation of a profile without active material to succeed: %v", err)
	}
}

func TestActiveRevisionReportsMissingProfile(t *testing.T) {
	store, _ := storeFixture(t)
	if _, err := store.ActiveRevision("production"); err == nil {
		t.Fatal("expected an error for a profile without active material")
	}
}

func TestRevisionsListsNewestFirst(t *testing.T) {
	store, input := storeFixture(t)
	if _, err := store.SaveRevision("production", "rev-1", input); err != nil {
		t.Fatalf("save rev-1: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := store.SaveRevision("production", "rev-2", input); err != nil {
		t.Fatalf("save rev-2: %v", err)
	}

	revisions, err := store.Revisions("production")
	if err != nil {
		t.Fatalf("revisions: %v", err)
	}
	if len(revisions) != 2 || revisions[0] != "rev-2" || revisions[1] != "rev-1" {
		t.Fatalf("expected the newest revision first, got %+v", revisions)
	}
}

func TestRevisionsOfUnknownProfileIsEmpty(t *testing.T) {
	store, _ := storeFixture(t)
	revisions, err := store.Revisions("production")
	if err != nil {
		t.Fatalf("revisions: %v", err)
	}
	if len(revisions) != 0 {
		t.Fatalf("expected no revisions, got %+v", revisions)
	}
}

func TestRemoveRevisionDeletesStoredMaterial(t *testing.T) {
	store, input := storeFixture(t)
	if _, err := store.SaveRevision("production", "rev-1", input); err != nil {
		t.Fatalf("save rev-1: %v", err)
	}
	if _, err := store.SaveRevision("production", "rev-2", input); err != nil {
		t.Fatalf("save rev-2: %v", err)
	}
	if err := store.Activate("production", "rev-1"); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if err := store.RemoveRevision("production", "rev-2"); err != nil {
		t.Fatalf("remove rev-2: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), "production", "revisions", "rev-2")); !os.IsNotExist(err) {
		t.Fatal("expected rev-2 to be removed")
	}
	if err := store.RemoveRevision("production", "rev-1"); err == nil {
		t.Fatal("expected an error when removing the active revision")
	}
}

func TestStoredKeyIsNotReadableByOthers(t *testing.T) {
	store, input := storeFixture(t)
	files, err := store.SaveRevision("production", "rev-1", input)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(files.KeyPath)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("expected a private key mode, got %v", info.Mode().Perm())
	}
}

func TestSavedCertificateMatchesTheParsedMaterial(t *testing.T) {
	store, input := storeFixture(t)
	files, err := store.SaveRevision("production", "rev-1", input)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	parsed, err := ParseCertificatesPEM(input.ServerCertPEM)
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}
	stored, err := os.ReadFile(files.CertPath)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	reloaded, err := ParseCertificatesPEM(string(stored))
	if err != nil {
		t.Fatalf("parse stored: %v", err)
	}
	if !reloaded[0].Equal(parsed[0]) {
		t.Fatal("expected the stored certificate to match the parsed source")
	}
	if _, err := x509.ParseCertificate(reloaded[0].Raw); err != nil {
		t.Fatalf("parse stored DER: %v", err)
	}
}
