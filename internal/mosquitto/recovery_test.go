package mosquitto

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// countingRunner counts Run calls and optionally fails each one. Used to
// prove the reload-error path restores files AND re-signals the broker
// exactly once more (issue #292, S17).
type countingRunner struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (c *countingRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return nil, c.err
}

func (c *countingRunner) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// setJournalPhase rewrites the on-disk journal phase so a test can simulate
// a process killed between phases.
func setJournalPhase(t *testing.T, path, phase string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	var journal recoveryJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		t.Fatalf("decode journal: %v", err)
	}
	journal.Phase = phase
	if err := persistJournal(path, journal); err != nil {
		t.Fatalf("rewrite journal phase: %v", err)
	}
}

func writeFileOrFatal(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRecoverInterruptedApplyRestoresBackups covers S19's process
// interruption: a journal left on disk in a non-committed phase by a killed
// process is restored by a FRESH applier instance at startup — always
// restore, never resume — and the journal is removed afterwards.
func TestRecoverInterruptedApplyRestoresBackups(t *testing.T) {
	t.Parallel()

	for _, phase := range []string{"prepared", "applying"} {
		t.Run("phase="+phase, func(t *testing.T) {
			dir := t.TempDir()
			aclPath := filepath.Join(dir, "acl")
			passwdPath := filepath.Join(dir, "passwd")
			// The killed process had already replaced both files.
			writeFileOrFatal(t, aclPath, "new acl")
			writeFileOrFatal(t, passwdPath, "new passwd")

			fa := FileApplier{ACLPath: aclPath, PasswdPath: passwdPath, ReloadCommand: []string{"reload"}, ReloadRunner: &fakeRunner{}}
			if err := fa.writeRecoveryJournalForTest([]recoveryEntry{
				{Path: aclPath, Snapshot: "old acl", Exists: true, Mode: 0o600},
				{Path: passwdPath, Snapshot: "old passwd", Exists: true, Mode: 0o600},
			}); err != nil {
				t.Fatalf("write interrupted journal: %v", err)
			}
			setJournalPhase(t, fa.recoveryJournalPath(), phase)

			fresh := FileApplier{ACLPath: aclPath, PasswdPath: passwdPath, ReloadCommand: []string{"reload"}, ReloadRunner: &fakeRunner{}}
			recovered, err := fresh.RecoverInterruptedApply()
			if err != nil {
				t.Fatalf("RecoverInterruptedApply: %v", err)
			}
			if !recovered {
				t.Fatal("recovered = false, want interrupted apply recovery")
			}
			for path, want := range map[string]string{aclPath: "old acl", passwdPath: "old passwd"} {
				got, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if string(got) != want {
					t.Errorf("%s = %q, want %q", path, got, want)
				}
			}
			if _, statErr := os.Stat(fresh.recoveryJournalPath()); !os.IsNotExist(statErr) {
				t.Errorf("journal still exists after recovery: %v", statErr)
			}
		})
	}
}

// TestRecoverInterruptedApplyNothingToDo: with no journal on disk,
// recovery is a no-op.
func TestRecoverInterruptedApplyNothingToDo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fa := FileApplier{
		ACLPath:       filepath.Join(dir, "acl"),
		PasswdPath:    filepath.Join(dir, "passwd"),
		ReloadCommand: []string{"reload"},
		ReloadRunner:  &fakeRunner{},
	}
	recovered, err := fa.RecoverInterruptedApply()
	if err != nil {
		t.Fatalf("RecoverInterruptedApply: %v", err)
	}
	if recovered {
		t.Error("recovered = true with no journal on disk")
	}
}

// TestFileApplierApplyWritesDurableJournalBeforeFirstRename locks S16: the
// backups and the journal are durably on disk BEFORE the first target is
// renamed, and both are cleaned up after a successful apply.
func TestFileApplierApplyWritesDurableJournalBeforeFirstRename(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aclPath := filepath.Join(dir, "acl")
	passwdPath := filepath.Join(dir, "passwd")
	writeFileOrFatal(t, aclPath, "old acl")
	writeFileOrFatal(t, passwdPath, "old passwd")

	var journalAtFirstRename string
	var backupsAtFirstRename int
	journalPath0 := filepath.Join(recoveryDir(aclPath, passwdPath), "journal.json")
	fa := FileApplier{
		ACLPath:       aclPath,
		PasswdPath:    passwdPath,
		ReloadCommand: []string{"reload"},
		ReloadRunner:  &fakeRunner{},
		afterReplace: func(index int) {
			if index != 0 || journalAtFirstRename != "" {
				return
			}
			if _, err := os.Stat(journalPath0); err != nil {
				t.Errorf("journal not durable before first rename: %v", err)
				return
			}
			journalAtFirstRename = journalPath0
			entries, err := os.ReadDir(filepath.Dir(journalPath0))
			if err != nil {
				t.Errorf("read recovery dir: %v", err)
				return
			}
			for _, entry := range entries {
				if entry.Name() != "journal.json" && entry.Name() != "journal.json.tmp" {
					backupsAtFirstRename++
				}
			}
		},
	}
	if err := fa.Apply(context.Background(), ApplyRequest{
		ACLBody: "new acl", ACLSnapshot: "old acl",
		PasswdBody: "new passwd", PasswdSnapshot: "old passwd",
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if journalAtFirstRename == "" {
		t.Fatal("afterReplace was never called")
	}
	if backupsAtFirstRename < 2 {
		t.Errorf("backups on disk at first rename = %d, want >= 2", backupsAtFirstRename)
	}
	for path, want := range map[string]string{aclPath: "new acl", passwdPath: "new passwd"} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if _, err := os.Stat(journalAtFirstRename); !os.IsNotExist(err) {
		t.Errorf("journal still exists after successful apply: %v", err)
	}
}

// TestFileApplierCancellationAfterReplacementRollsBack covers S18: a
// request cancelled AFTER the first file was replaced still gets a bounded
// rollback that does not depend on the dying request context.
func TestFileApplierCancellationAfterReplacementRollsBack(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aclPath := filepath.Join(dir, "acl")
	passwdPath := filepath.Join(dir, "passwd")
	writeFileOrFatal(t, aclPath, "old acl")
	writeFileOrFatal(t, passwdPath, "old passwd")

	ctx, cancel := context.WithCancel(context.Background())
	fa := FileApplier{
		ACLPath:       aclPath,
		PasswdPath:    passwdPath,
		ReloadCommand: []string{"reload"},
		ReloadRunner:  &fakeRunner{},
		afterReplace: func(index int) {
			if index == 0 {
				cancel()
			}
		},
	}
	err := fa.Apply(ctx, ApplyRequest{
		ACLBody: "new acl", ACLSnapshot: "old acl",
		PasswdBody: "new passwd", PasswdSnapshot: "old passwd",
	})
	cancel()
	if err == nil {
		t.Fatal("Apply returned nil after cancellation")
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrApplyRestored) {
		t.Errorf("error = %v, want context.Canceled or ErrApplyRestored in chain", err)
	}
	for path, want := range map[string]string{aclPath: "old acl", passwdPath: "old passwd"} {
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q (rollback did not restore)", path, got, want)
		}
	}
	if _, statErr := os.Stat(fa.recoveryJournalPath()); !os.IsNotExist(statErr) {
		t.Errorf("journal still exists after successful rollback: %v", statErr)
	}
}

// TestFileApplierStagingPermissionFailureLeavesTargetsUntouched covers S17's
// permission-failure class at the staging step: no target is replaced and
// no recovery journal is left behind.
func TestFileApplierStagingPermissionFailureLeavesTargetsUntouched(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not block writes")
	}
	t.Parallel()
	dir := t.TempDir()
	aclPath := filepath.Join(dir, "acl")
	passwdPath := filepath.Join(dir, "passwd")
	writeFileOrFatal(t, aclPath, "old acl")
	writeFileOrFatal(t, passwdPath, "old passwd")

	listenerDir := filepath.Join(dir, "listener")
	if err := os.Mkdir(listenerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	listenerACL := filepath.Join(listenerDir, "acl")
	writeFileOrFatal(t, listenerACL, "old listener acl")
	if err := os.Chmod(listenerDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(listenerDir, 0o700) })

	fa := FileApplier{
		ACLPath:       aclPath,
		PasswdPath:    passwdPath,
		ReloadCommand: []string{"reload"},
		ReloadRunner:  &fakeRunner{},
	}
	err := fa.Apply(context.Background(), ApplyRequest{
		ACLBody: "new acl", ACLSnapshot: "old acl",
		ListenerACLFiles: []ACLFile{{Path: listenerACL, Body: "new listener acl", Snapshot: "old listener acl"}},
		PasswdBody:       "new passwd", PasswdSnapshot: "old passwd",
	})
	if err == nil {
		t.Fatal("Apply succeeded despite unwritable listener dir")
	}
	if errors.Is(err, ErrApplyRestored) || errors.Is(err, ErrRollbackFailed) {
		t.Errorf("staging failure must not report a restore outcome: %v", err)
	}
	for path, want := range map[string]string{
		aclPath: "old acl", passwdPath: "old passwd", listenerACL: "old listener acl",
	} {
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q (staging failure replaced a target)", path, got, want)
		}
	}
	if _, statErr := os.Stat(fa.recoveryJournalPath()); !os.IsNotExist(statErr) {
		t.Errorf("journal left behind by a failed staging: %v", statErr)
	}
}

// TestFileApplierFailedRollbackKeepsJournalForStartupRecovery covers S19's
// failed-rollback path: when the in-request rollback cannot restore the
// files, the recovery journal STAYS on disk so the next process startup
// can restore from the backups.
func TestFileApplierFailedRollbackKeepsJournalForStartupRecovery(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not block writes")
	}
	t.Parallel()
	dir := t.TempDir()
	aclPath := filepath.Join(dir, "acl")
	passwdPath := filepath.Join(dir, "passwd")
	writeFileOrFatal(t, aclPath, "old acl")
	writeFileOrFatal(t, passwdPath, "old passwd")

	fa := FileApplier{
		ACLPath:       aclPath,
		PasswdPath:    passwdPath,
		ReloadCommand: []string{"reload"},
		ReloadRunner:  &fakeRunner{},
		afterReplace: func(index int) {
			if index == 0 {
				// The rollback's atomic writes need write access to the
				// directory; removing it makes the restore fail.
				if err := os.Chmod(dir, 0o500); err != nil {
					t.Errorf("chmod dir: %v", err)
				}
			}
		},
	}
	err := fa.Apply(context.Background(), ApplyRequest{
		ACLBody: "new acl", ACLSnapshot: "old acl",
		PasswdBody: "new passwd", PasswdSnapshot: "old passwd",
	})
	if err == nil {
		t.Fatal("Apply succeeded despite blocked rollback")
	}
	if !errors.Is(err, ErrRollbackFailed) {
		t.Fatalf("error = %v, want ErrRollbackFailed in chain", err)
	}
	if _, statErr := os.Stat(fa.recoveryJournalPath()); statErr != nil {
		t.Fatalf("recovery journal must survive a failed rollback: %v", statErr)
	}

	// The next process instance restores from the journal backups.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	fresh := FileApplier{ACLPath: aclPath, PasswdPath: passwdPath, ReloadCommand: []string{"reload"}, ReloadRunner: &fakeRunner{}}
	recovered, recErr := fresh.RecoverInterruptedApply()
	if recErr != nil {
		t.Fatalf("startup recovery after failed rollback: %v", recErr)
	}
	if !recovered {
		t.Fatal("startup recovery found nothing to restore")
	}
	for path, want := range map[string]string{aclPath: "old acl", passwdPath: "old passwd"} {
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q after startup recovery", path, got, want)
		}
	}
	if _, statErr := os.Stat(fresh.recoveryJournalPath()); !os.IsNotExist(statErr) {
		t.Errorf("journal still exists after startup recovery: %v", statErr)
	}
}

// TestFileApplierReloadFailureRestoresFilesAndResignals covers S17's
// reload-may-have-taken-effect case: a reload error after the files were
// replaced restores the previous content, then re-signals the broker once
// (bounded) so the live broker matches disk again.
func TestFileApplierReloadFailureRestoresFilesAndResignals(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aclPath := filepath.Join(dir, "acl")
	passwdPath := filepath.Join(dir, "passwd")
	writeFileOrFatal(t, aclPath, "old acl")
	writeFileOrFatal(t, passwdPath, "old passwd")

	runner := &countingRunner{err: errors.New("systemctl unavailable")}
	fa := FileApplier{
		ACLPath:       aclPath,
		PasswdPath:    passwdPath,
		ReloadCommand: []string{"systemctl", "reload", "mosquitto"},
		ReloadRunner:  runner,
	}
	err := fa.Apply(context.Background(), ApplyRequest{
		ACLBody: "new acl", ACLSnapshot: "old acl",
		PasswdBody: "new passwd", PasswdSnapshot: "old passwd",
	})
	if err == nil {
		t.Fatal("Apply succeeded despite failing reload")
	}
	if !errors.Is(err, ErrApplyRestored) {
		t.Errorf("error = %v, want ErrApplyRestored in chain", err)
	}
	if !errors.Is(err, ErrResignalFailed) {
		t.Errorf("error = %v, want ErrResignalFailed in chain (broker re-signal must be reported)", err)
	}
	if got := runner.count(); got != 2 {
		t.Errorf("runner calls = %d, want 2 (reload + one re-signal)", got)
	}
	for path, want := range map[string]string{aclPath: "old acl", passwdPath: "old passwd"} {
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q (reload failure must restore)", path, got, want)
		}
	}
	if _, statErr := os.Stat(fa.recoveryJournalPath()); !os.IsNotExist(statErr) {
		t.Errorf("journal still exists after restored reload failure: %v", statErr)
	}
}

// TestDockerApplierApplyCommitsAndRecoversJournal: the Docker applier uses
// the same durable journal; a successful apply cleans it, and a fresh
// instance restores an interrupted one.
func TestDockerApplierApplyCommitsAndRecoversJournal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aclPath := filepath.Join(dir, "acl")
	passwdPath := filepath.Join(dir, "passwd")
	writeFileOrFatal(t, aclPath, "old acl")
	writeFileOrFatal(t, passwdPath, "old passwd")

	da := DockerApplier{
		ACLPath:       aclPath,
		PasswdPath:    passwdPath,
		ContainerName: "mosquitto",
		Runner:        &fakeRunner{},
	}
	if err := da.Apply(context.Background(), ApplyRequest{
		ACLBody: "new acl", ACLSnapshot: "old acl",
		PasswdBody: "new passwd", PasswdSnapshot: "old passwd",
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(da.recoveryJournalPath()); !os.IsNotExist(err) {
		t.Errorf("journal still exists after successful docker apply: %v", err)
	}

	// Simulate a killed process: files already replaced, journal prepared.
	writeFileOrFatal(t, aclPath, "newer acl")
	writeFileOrFatal(t, passwdPath, "newer passwd")
	if err := da.writeRecoveryJournalForTest([]recoveryEntry{
		{Path: aclPath, Snapshot: "new acl", Exists: true, Mode: 0o600},
		{Path: passwdPath, Snapshot: "new passwd", Exists: true, Mode: 0o600},
	}); err != nil {
		t.Fatalf("write interrupted journal: %v", err)
	}
	fresh := DockerApplier{ACLPath: aclPath, PasswdPath: passwdPath, ContainerName: "mosquitto", Runner: &fakeRunner{}}
	recovered, err := fresh.RecoverInterruptedApply()
	if err != nil {
		t.Fatalf("RecoverInterruptedApply: %v", err)
	}
	if !recovered {
		t.Fatal("recovered = false, want docker apply recovery")
	}
	for path, want := range map[string]string{aclPath: "new acl", passwdPath: "new passwd"} {
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}
