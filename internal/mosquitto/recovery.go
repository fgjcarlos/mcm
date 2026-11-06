package mosquitto

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxRecoveryEntries = 1024
	maxRecoveryBytes   = 64 << 20
	maxJournalBytes    = 16 << 20
)

type recoveryEntry struct {
	Path     string      `json:"path"`
	Backup   string      `json:"backup"`
	Exists   bool        `json:"exists"`
	Mode     os.FileMode `json:"mode"`
	Remove   bool        `json:"remove,omitempty"`
	Snapshot string      `json:"-"`
	Body     string      `json:"-"`
}

type recoveryJournal struct {
	Phase   string          `json:"phase"`
	Entries []recoveryEntry `json:"entries"`
}

func recoveryDir(aclPath, passwdPath string) string {
	// The configured ACL and passwd files are the primary deployment pair;
	// their common parent is the stable location shared by fresh appliers.
	return filepath.Join(filepath.Dir(passwdPath), ".mcm-recovery")
}

func (f FileApplier) recoveryJournalPath() string {
	return filepath.Join(recoveryDir(f.ACLPath, f.PasswdPath), "journal.json")
}
func (d DockerApplier) recoveryJournalPath() string {
	return filepath.Join(recoveryDir(d.ACLPath, d.PasswdPath), "journal.json")
}

func syncDirectory(path string) {
	dir, err := os.Open(path)
	if err == nil {
		_ = dir.Sync() // directory fsync is not supported on every platform
		_ = dir.Close()
	}
}

func writeSynced(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return closeErr
	}
	return nil
}

func persistJournal(path string, journal recoveryJournal) error {
	data, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	if len(data) > maxJournalBytes {
		return fmt.Errorf("recovery journal exceeds %d bytes", maxJournalBytes)
	}
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	syncDirectory(filepath.Dir(path))
	return nil
}

func (f FileApplier) writeRecoveryJournalForTest(entries []recoveryEntry) error {
	return createRecoveryJournal(f.recoveryJournalPath(), entries)
}

func createRecoveryJournal(path string, entries []recoveryEntry) error {
	if len(entries) == 0 || len(entries) > maxRecoveryEntries {
		return fmt.Errorf("recovery entry count must be between 1 and %d", maxRecoveryEntries)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	syncDirectory(filepath.Dir(dir))
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	prefix := "backup-" + hex.EncodeToString(nonce[:]) + "-"
	for i := range entries {
		entry := &entries[i]
		if len(entry.Snapshot) > maxRecoveryBytes || len(entry.Body) > maxRecoveryBytes {
			return fmt.Errorf("recovery target %q exceeds %d bytes", entry.Path, maxRecoveryBytes)
		}
		if entry.Exists {
			entry.Backup = filepath.Join(dir, fmt.Sprintf("%s%d", prefix, i))
			if err := writeSynced(entry.Backup, []byte(entry.Snapshot), 0o600); err != nil {
				cleanupRecovery(entries)
				return fmt.Errorf("write recovery backup: %w", err)
			}
		}
	}
	syncDirectory(dir)
	if err := persistJournal(path, recoveryJournal{Phase: "prepared", Entries: entries}); err != nil {
		cleanupRecovery(entries)
		return fmt.Errorf("persist recovery journal: %w", err)
	}
	return nil
}

func cleanupRecovery(entries []recoveryEntry) {
	for _, entry := range entries {
		if entry.Backup != "" {
			_ = os.Remove(entry.Backup)
		}
	}
}

func recoveryEntries(paths []ACLFile, passwdPath string, req ApplyRequest) ([]recoveryEntry, error) {
	entries := make([]recoveryEntry, 0, len(paths)+len(req.RemovedACLFiles)+1)
	add := func(path, body, snapshot string, remove bool) error {
		if len(entries) >= maxRecoveryEntries {
			return fmt.Errorf("recovery entry count exceeds %d", maxRecoveryEntries)
		}
		entry := recoveryEntry{Path: path, Body: body, Snapshot: snapshot, Remove: remove, Mode: 0o600}
		info, err := os.Stat(path)
		if err == nil {
			entry.Exists = true
			entry.Mode = info.Mode().Perm()
			data, readErr := readBounded(path, maxRecoveryBytes)
			if readErr != nil {
				return fmt.Errorf("snapshot target %q: %w", path, readErr)
			}
			entry.Snapshot = string(data)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		entries = append(entries, entry)
		return nil
	}
	for _, file := range paths {
		if err := add(file.Path, file.Body, file.Snapshot, false); err != nil {
			return nil, err
		}
	}
	for _, file := range req.RemovedACLFiles {
		if err := add(file.Path, "", file.Snapshot, true); err != nil {
			return nil, err
		}
	}
	if err := add(passwdPath, req.PasswdBody, req.PasswdSnapshot, false); err != nil {
		return nil, err
	}
	return entries, nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return data, nil
}

func stageTransaction(ctx context.Context, aclPath, passwdPath string, req ApplyRequest) ([]string, []recoveryEntry, string, error) {
	files := append([]ACLFile{{Path: aclPath, Body: req.ACLBody, Snapshot: req.ACLSnapshot}}, req.ListenerACLFiles...)
	entries, err := recoveryEntries(files, passwdPath, req)
	if err != nil {
		return nil, nil, "", err
	}
	staged := make([]string, 0, len(files)+1)
	cleanupStaged := func() {
		for _, name := range staged {
			cleanupTempFile(nil, name)
		}
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			cleanupStaged()
			return nil, nil, "", err
		}
		tmp, err := writeStage(file.Path, file.Body, 0o600)
		if err != nil {
			cleanupStaged()
			return nil, nil, "", err
		}
		staged = append(staged, tmp)
	}
	passwdTmp, err := writeStage(passwdPath, req.PasswdBody, 0o600)
	if err != nil {
		cleanupStaged()
		return nil, nil, "", err
	}
	staged = append(staged, passwdTmp)
	journalPath := filepath.Join(recoveryDir(aclPath, passwdPath), "journal.json")
	if err := createRecoveryJournal(journalPath, entries); err != nil {
		cleanupStaged()
		return nil, nil, "", err
	}
	return staged, entries, journalPath, nil
}

func restoreJournal(path string, journal recoveryJournal) error {
	for _, entry := range journal.Entries {
		if entry.Exists {
			data, err := readBounded(entry.Backup, maxRecoveryBytes)
			if err != nil {
				return fmt.Errorf("read backup for %q: %w", entry.Path, err)
			}
			tmp, err := writeStage(entry.Path, string(data), entry.Mode)
			if err != nil {
				return fmt.Errorf("stage restore %q: %w", entry.Path, err)
			}
			if err := commitStage(tmp, entry.Path); err != nil {
				return fmt.Errorf("restore %q: %w", entry.Path, err)
			}
		} else if err := os.Remove(entry.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove new target %q: %w", entry.Path, err)
		}
	}
	for _, entry := range journal.Entries {
		if entry.Backup != "" {
			if err := os.Remove(entry.Backup); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	syncDirectory(filepath.Dir(path))
	return nil
}

func recoverApply(path string, resignal func() error) (bool, error) {
	data, err := readBounded(path, maxJournalBytes)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read recovery journal: %w", err)
	}
	var journal recoveryJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return false, fmt.Errorf("decode recovery journal: %w", err)
	}
	if !journal.valid() {
		return false, fmt.Errorf("invalid recovery journal %q: empty phase or entries out of bounds", path)
	}
	if journal.Phase == "committed" {
		cleanupRecovery(journal.Entries)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		syncDirectory(filepath.Dir(path))
		return false, nil
	}
	if journal.Phase != "prepared" && journal.Phase != "applying" {
		return false, fmt.Errorf("unknown recovery journal phase %q", journal.Phase)
	}
	if err := restoreJournal(path, journal); err != nil {
		return true, err
	}
	if resignal != nil {
		if err := resignal(); err != nil {
			return true, fmt.Errorf("files restored but broker re-signal failed: %w", err)
		}
	}
	return true, nil
}

func (f FileApplier) RecoverInterruptedApply() (bool, error) {
	return recoverApply(f.recoveryJournalPath(), f.reloadBroker)
}
func (d DockerApplier) RecoverInterruptedApply() (bool, error) {
	return recoverApply(d.recoveryJournalPath(), func() error {
		runner := d.Runner
		if runner == nil {
			runner = ExecRunner{}
		}
		_, err := runner.Run(context.Background(), "docker", "exec", d.ContainerName, "kill", "-HUP", "1")
		return err
	})
}
func (d DockerApplier) writeRecoveryJournalForTest(entries []recoveryEntry) error {
	return createRecoveryJournal(d.recoveryJournalPath(), entries)
}

// InterruptedApplyRecoverer is implemented by appliers that keep a durable
// recovery journal and can restore an apply interrupted by process death
// (issue #292, S19). RecoverInterruptedApply reports whether a journal was
// found and restored; it never resumes the interrupted apply.
type InterruptedApplyRecoverer interface {
	RecoverInterruptedApply() (bool, error)
}

// completeJournal marks the journal committed (fsynced) and then removes it
// together with the backups. The committed phase must be durable BEFORE the
// apply is reported successful: a crash between the reload and the cleanup
// must not make the next startup restore a fully successful apply.
func completeJournal(path string, entries []recoveryEntry) error {
	if err := persistJournal(path, recoveryJournal{Phase: "committed", Entries: entries}); err != nil {
		return err
	}
	cleanupRecovery(entries)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	syncDirectory(filepath.Dir(path))
	return nil
}

// cleanupJournal drops the journal and its backups after a successful
// in-request restore: the backups describe the same pre-apply content that
// was just restored, so keeping them would only trigger a redundant startup
// restore. A cleanup failure is logged, not fatal.
func cleanupJournal(path string, entries []recoveryEntry) {
	cleanupRecovery(entries)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("mosquitto: recovery journal cleanup failed", "path", path, "error", err)
	}
	syncDirectory(filepath.Dir(path))
}

func (j recoveryJournal) valid() bool {
	return len(j.Entries) > 0 && len(j.Entries) <= maxRecoveryEntries && strings.TrimSpace(j.Phase) != ""
}
