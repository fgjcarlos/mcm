package certs

import (
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// File names used inside a stored revision directory.
const (
	// CAFileName holds the certificate authority certificate.
	CAFileName = "ca.crt"
	// ChainFileName holds the intermediate certificate bundle.
	ChainFileName = "chain.crt"
	// ServerCertFileName holds the leaf certificate.
	ServerCertFileName = "server.crt"
	// ServerKeyFileName holds the private key.
	ServerKeyFileName = "server.key"
)

// Permissions of the stored material.
//
// The private key stays readable by its owner only; production deployments
// share the broker UID with MCM so Mosquitto can read it (see
// deploy/mosquitto/README.md).
const (
	keyFilePerm  os.FileMode = 0o600
	certFilePerm os.FileMode = 0o644
	dirPerm      os.FileMode = 0o755
)

var (
	// ErrInvalidName is returned when a profile or revision name is not a
	// safe path segment.
	ErrInvalidName = errors.New("invalid certificate profile name")
	// ErrNotFound is returned when a profile, revision or active link does
	// not exist.
	ErrNotFound = errors.New("certificate material not found")
	// ErrActiveRevision is returned when a mutation would touch the revision
	// that is currently active.
	ErrActiveRevision = errors.New("revision is active")
	// ErrRevisionExists is returned when a revision id is already stored.
	// Revisions are immutable.
	ErrRevisionExists = errors.New("certificate revision already exists")
	// ErrInvalidMaterial is returned when the material cannot be persisted.
	ErrInvalidMaterial = errors.New("invalid certificate material")
)

// namePattern accepts the profile and revision names that may become path
// segments.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Store persists broker certificate material on disk.
//
// The layout keeps every revision immutable and makes the active material a
// single atomic switch:
//
//	<root>/<profile>/revisions/<revision>/{ca.crt,chain.crt,server.crt,server.key}
//	<root>/<profile>/active -> revisions/<revision>
//
// The active link is relative so the same tree works on both sides of the
// shared configuration volume, and activation is a rename of a temporary
// link: there is no window where the active material is missing.
type Store struct {
	root string
}

// NewStore returns a store rooted at the given directory.
func NewStore(root string) *Store {
	return &Store{root: root}
}

// Root returns the root directory of the store.
func (s *Store) Root() string {
	return s.root
}

// Files describes where one material set lives on disk.
type Files struct {
	CAPath    string
	ChainPath string
	CertPath  string
	KeyPath   string
}

// SaveRevision writes the material of a new revision and returns its paths.
//
// Only validated material is persisted: the certificates and the private key
// are re-encoded from their parsed form, so stray PEM blocks supplied by an
// operator are never stored.
func (s *Store) SaveRevision(profile, revisionID string, in Input) (Files, error) {
	if err := validateName(profile); err != nil {
		return Files{}, err
	}
	if err := validateName(revisionID); err != nil {
		return Files{}, err
	}

	leaf, err := ParseCertificatesPEM(in.ServerCertPEM)
	if err != nil || len(leaf) == 0 {
		return Files{}, fmt.Errorf("%w: the server certificate is not a valid PEM certificate", ErrInvalidMaterial)
	}
	key, err := ParsePrivateKeyPEM(in.ServerKeyPEM)
	if err != nil {
		return Files{}, fmt.Errorf("%w: the server private key is not a valid PEM private key", ErrInvalidMaterial)
	}
	var caCerts []*x509.Certificate
	if strings.TrimSpace(in.CACertPEM) != "" {
		caCerts, err = ParseCertificatesPEM(in.CACertPEM)
		if err != nil || len(caCerts) == 0 {
			return Files{}, fmt.Errorf("%w: the certificate authority is not a valid PEM certificate", ErrInvalidMaterial)
		}
	}
	var chainCerts []*x509.Certificate
	if strings.TrimSpace(in.ChainPEM) != "" {
		chainCerts, err = ParseCertificatesPEM(in.ChainPEM)
		if err != nil || len(chainCerts) == 0 {
			return Files{}, fmt.Errorf("%w: the certificate chain is not a valid PEM certificate bundle", ErrInvalidMaterial)
		}
	}

	certPEM := encodeCertificates(leaf)
	keyPEM, err := encodePrivateKey(key)
	if err != nil {
		return Files{}, err
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return Files{}, fmt.Errorf("%w: the private key does not match the server certificate", ErrInvalidMaterial)
	}

	dir := s.revisionDir(profile, revisionID)
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return Files{}, fmt.Errorf("%w: revision %q of profile %q", ErrRevisionExists, revisionID, profile)
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return Files{}, fmt.Errorf("create revision directory: %w", err)
	}

	files := Files{
		CertPath: filepath.Join(dir, ServerCertFileName),
		KeyPath:  filepath.Join(dir, ServerKeyFileName),
	}
	if err := writeFileAtomic(files.CertPath, certPEM, certFilePerm); err != nil {
		return Files{}, err
	}
	if err := writeFileAtomic(files.KeyPath, keyPEM, keyFilePerm); err != nil {
		return Files{}, err
	}
	if len(caCerts) > 0 {
		files.CAPath = filepath.Join(dir, CAFileName)
		if err := writeFileAtomic(files.CAPath, encodeCertificates(caCerts), certFilePerm); err != nil {
			return Files{}, err
		}
	}
	if len(chainCerts) > 0 {
		files.ChainPath = filepath.Join(dir, ChainFileName)
		if err := writeFileAtomic(files.ChainPath, encodeCertificates(chainCerts), certFilePerm); err != nil {
			return Files{}, err
		}
	}
	return files, nil
}

// Activate points the active link of a profile at a stored revision.
//
// The switch is atomic: a temporary relative link is created next to the
// active link and renamed over it.
func (s *Store) Activate(profile, revisionID string) error {
	if err := validateName(profile); err != nil {
		return err
	}
	if err := validateName(revisionID); err != nil {
		return err
	}
	revisionDir := s.revisionDir(profile, revisionID)
	info, err := os.Stat(revisionDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%w: revision %q of profile %q", ErrNotFound, revisionID, profile)
	}

	profileDir := s.profileDir(profile)
	if err := os.MkdirAll(profileDir, dirPerm); err != nil {
		return fmt.Errorf("create profile directory: %w", err)
	}
	activePath := filepath.Join(profileDir, "active")
	tempPath := filepath.Join(profileDir, fmt.Sprintf(".active.%d.tmp", os.Getpid()))
	_ = os.Remove(tempPath)
	if err := os.Symlink(filepath.Join("revisions", revisionID), tempPath); err != nil {
		return fmt.Errorf("create active link: %w", err)
	}
	if err := os.Rename(tempPath, activePath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("activate revision: %w", err)
	}
	return nil
}

// ActiveRevision returns the revision the active link points at.
func (s *Store) ActiveRevision(profile string) (string, error) {
	if err := validateName(profile); err != nil {
		return "", err
	}
	activePath := filepath.Join(s.profileDir(profile), "active")
	target, err := os.Readlink(activePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: profile %q has no active revision", ErrNotFound, profile)
		}
		return "", fmt.Errorf("read active link: %w", err)
	}
	return filepath.Base(target), nil
}

// RevisionFiles returns the paths of a stored revision.
func (s *Store) RevisionFiles(profile, revisionID string) (Files, error) {
	if err := validateName(profile); err != nil {
		return Files{}, err
	}
	if err := validateName(revisionID); err != nil {
		return Files{}, err
	}
	dir := s.revisionDir(profile, revisionID)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return Files{}, fmt.Errorf("%w: revision %q of profile %q", ErrNotFound, revisionID, profile)
	}
	files := Files{
		CertPath: filepath.Join(dir, ServerCertFileName),
		KeyPath:  filepath.Join(dir, ServerKeyFileName),
	}
	if _, err := os.Stat(filepath.Join(dir, CAFileName)); err == nil {
		files.CAPath = filepath.Join(dir, CAFileName)
	}
	if _, err := os.Stat(filepath.Join(dir, ChainFileName)); err == nil {
		files.ChainPath = filepath.Join(dir, ChainFileName)
	}
	return files, nil
}

// ActiveFiles returns the paths of the active material of a profile.
func (s *Store) ActiveFiles(profile string) (Files, error) {
	revisionID, err := s.ActiveRevision(profile)
	if err != nil {
		return Files{}, err
	}
	return s.RevisionFiles(profile, revisionID)
}

// Revisions lists the stored revisions of a profile, newest first.
func (s *Store) Revisions(profile string) ([]string, error) {
	if err := validateName(profile); err != nil {
		return nil, err
	}
	dir := filepath.Join(s.profileDir(profile), "revisions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read revisions: %w", err)
	}
	type revision struct {
		id      string
		modTime time.Time
	}
	revisions := make([]revision, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		revisions = append(revisions, revision{id: entry.Name(), modTime: info.ModTime()})
	}
	sort.Slice(revisions, func(i, j int) bool {
		if revisions[i].modTime.Equal(revisions[j].modTime) {
			return revisions[i].id > revisions[j].id
		}
		return revisions[i].modTime.After(revisions[j].modTime)
	})
	ids := make([]string, 0, len(revisions))
	for _, item := range revisions {
		ids = append(ids, item.id)
	}
	return ids, nil
}

// RemoveRevision deletes a stored revision.
//
// The active revision is protected: it must be deactivated first.
func (s *Store) RemoveRevision(profile, revisionID string) error {
	if err := validateName(profile); err != nil {
		return err
	}
	if err := validateName(revisionID); err != nil {
		return err
	}
	active, err := s.ActiveRevision(profile)
	if err == nil && active == revisionID {
		return fmt.Errorf("%w: revision %q of profile %q", ErrActiveRevision, revisionID, profile)
	}
	dir := s.revisionDir(profile, revisionID)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("%w: revision %q of profile %q", ErrNotFound, revisionID, profile)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove revision: %w", err)
	}
	return nil
}

func (s *Store) profileDir(profile string) string {
	return filepath.Join(s.root, profile)
}

func (s *Store) revisionDir(profile, revisionID string) string {
	return filepath.Join(s.root, profile, "revisions", revisionID)
}

// validateName rejects every name that could escape the store root.
func validateName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	return nil
}

// encodeCertificates re-encodes certificates as a canonical PEM bundle.
func encodeCertificates(certificates []*x509.Certificate) []byte {
	var buffer []byte
	for _, certificate := range certificates {
		buffer = append(buffer, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})...)
	}
	return buffer
}

// encodePrivateKey re-encodes a private key as PKCS#8 PEM.
func encodePrivateKey(key crypto.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("%w: encode the private key", ErrInvalidMaterial)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// writeFileAtomic writes a file through a temporary file in the same
// directory and renames it over the target.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".mcm-cert-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(perm); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set permissions on %s: %w", filepath.Base(path), err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync %s: %w", filepath.Base(path), err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("install %s: %w", filepath.Base(path), err)
	}
	return nil
}
