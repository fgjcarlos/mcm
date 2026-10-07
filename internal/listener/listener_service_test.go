package listener

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/mosquitto/listeners"
	"github.com/fgjcarlos/mcm/internal/storage"
)

type inMemoryListenerStore struct {
	mu   sync.Mutex
	rows []storage.ListenerSpecRow
	err  error
}

func newInMemoryListenerStore(rows []storage.ListenerSpecRow) *inMemoryListenerStore {
	return &inMemoryListenerStore{rows: append([]storage.ListenerSpecRow(nil), rows...)}
}

func (s *inMemoryListenerStore) ListListenerSpecs(context.Context) ([]storage.ListenerSpecRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storage.ListenerSpecRow(nil), s.rows...), nil
}

func (s *inMemoryListenerStore) ReplaceAllListenerSpecs(_ context.Context, rows []storage.ListenerSpecRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.rows = append([]storage.ListenerSpecRow(nil), rows...)
	return nil
}

type fakeComposeReader struct {
	configured bool
	disabled   bool
	ports      []int
	err        error
}

func (r fakeComposeReader) IsConfigured() bool { return r.configured }
func (r fakeComposeReader) Disabled() bool     { return r.disabled }
func (r fakeComposeReader) HostPorts(context.Context) ([]int, error) {
	return append([]int(nil), r.ports...), r.err
}

func listenerSpec(id string, port int) listeners.ListenerSpec {
	return listeners.ListenerSpec{ID: id, Port: port, Bind: "0.0.0.0", Protocols: []listeners.Protocol{listeners.ProtocolMQTT}}
}

func listenerRow(id string, port int) storage.ListenerSpecRow {
	return storage.ListenerSpecRow{ID: id, Port: port, Bind: "0.0.0.0", Protocols: []string{"mqtt"}}
}

func newTestService(store *inMemoryListenerStore, opts ...ListenerServiceOption) *ListenerService {
	return NewListenerService(store, nil, &NoopRestartRunner{}, nil, opts...)
}

func TestListenerService_ComposeAllHostPorts_Disabled(t *testing.T) {
	svc := NewListenerService(nil, fakeComposeReader{configured: true, disabled: true, ports: []int{1883}}, &NoopRestartRunner{}, nil)

	ports, err := svc.ComposeAllHostPorts(context.Background())
	if err != nil {
		t.Fatalf("ComposeAllHostPorts() error = %v", err)
	}
	if ports != nil {
		t.Fatalf("ComposeAllHostPorts() = %v, want nil", ports)
	}
}

func TestListenerService_ComposeAllHostPorts_Sorted(t *testing.T) {
	svc := NewListenerService(nil, fakeComposeReader{configured: true, ports: []int{8883, 1883, 8083}}, &NoopRestartRunner{}, nil)

	ports, err := svc.ComposeAllHostPorts(context.Background())
	if err != nil {
		t.Fatalf("ComposeAllHostPorts() error = %v", err)
	}
	want := []int{1883, 8083, 8883}
	if !reflect.DeepEqual(ports, want) {
		t.Fatalf("ComposeAllHostPorts() = %v, want %v", ports, want)
	}
}

func TestListenerService_Preview_NoDriftSameList(t *testing.T) {
	svc := newTestService(newInMemoryListenerStore([]storage.ListenerSpecRow{listenerRow("one", 1883)}), WithListenerRevisionID(func() (string, error) { return "one", nil }))
	preview, err := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 1883)}, PreviewOptions{})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if preview.NeedsRestart {
		t.Fatal("NeedsRestart = true, want false")
	}
}

func TestListenerService_Preview_DriftTriggers(t *testing.T) {
	svc := newTestService(newInMemoryListenerStore(nil), WithListenerRevisionID(func() (string, error) { return "one", nil }))
	preview, err := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 1883)}, PreviewOptions{})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if !preview.NeedsRestart || preview.Diff == "" {
		t.Fatalf("preview = %+v, want restart and diff", preview)
	}
}

func TestListenerService_Preview_ValidationBlocks(t *testing.T) {
	svc := newTestService(newInMemoryListenerStore(nil))
	preview, err := svc.Preview(context.Background(), []listeners.ListenerSpec{{ID: "bad", Port: 0}}, PreviewOptions{})
	if err == nil || len(preview.Issues) == 0 {
		t.Fatalf("Preview() = (%+v, %v), want validation error and issues", preview, err)
	}
}

func TestListenerService_Preview_ComposeUnmapped(t *testing.T) {
	svc := NewListenerService(newInMemoryListenerStore(nil), fakeComposeReader{configured: true, ports: []int{1883}}, &NoopRestartRunner{}, nil)
	_, err := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 9001)}, PreviewOptions{})
	if !errors.Is(err, ErrListenerComposeUnmapped) {
		t.Fatalf("Preview() error = %v, want ErrListenerComposeUnmapped", err)
	}
}

func TestListenerService_Preview_ComposeUnmapped_ConfirmBypasses(t *testing.T) {
	svc := NewListenerService(newInMemoryListenerStore(nil), fakeComposeReader{configured: true, ports: []int{1883}}, &NoopRestartRunner{}, nil)
	preview, err := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 9001)}, PreviewOptions{Confirm: true})
	if err != nil || len(preview.ComposeStatus.UnmappedPorts) != 1 {
		t.Fatalf("Preview() = (%+v, %v), want unmapped result without error", preview, err)
	}
}

func TestListenerService_Preview_RevisionIDDistinct(t *testing.T) {
	ids := []string{"one", "two"}
	svc := newTestService(newInMemoryListenerStore(nil), WithListenerRevisionID(func() (string, error) { id := ids[0]; ids = ids[1:]; return id, nil }))
	first, err := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 1883)}, PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 1883)}, PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.RevisionID == second.RevisionID {
		t.Fatal("revision IDs must differ")
	}
}

func TestListenerService_Apply_HappyPath_ConfirmRestart(t *testing.T) {
	store := newInMemoryListenerStore(nil)
	svc := newTestService(store, WithListenerRevisionID(func() (string, error) { return "one", nil }))
	preview, err := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 1883)}, PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(context.Background(), preview.RevisionID, true); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if rows, _ := store.ListListenerSpecs(context.Background()); len(rows) != 1 {
		t.Fatalf("stored rows = %d, want 1", len(rows))
	}
}

func TestListenerService_Apply_NotConfirmed(t *testing.T) {
	svc := newTestService(newInMemoryListenerStore(nil), WithListenerRevisionID(func() (string, error) { return "one", nil }))
	preview, _ := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 1883)}, PreviewOptions{})
	if err := svc.Apply(context.Background(), preview.RevisionID, false); !errors.Is(err, ErrListenerRestartNotConfirmed) {
		t.Fatalf("Apply() error = %v", err)
	}
}

func TestListenerService_Apply_RestartFailure(t *testing.T) {
	store := newInMemoryListenerStore(nil)
	svc := NewListenerService(store, nil, failingRestartRunner{}, nil, WithListenerRevisionID(func() (string, error) { return "one", nil }))
	preview, _ := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 1883)}, PreviewOptions{})
	if err := svc.Apply(context.Background(), preview.RevisionID, true); !errors.Is(err, ErrListenerRestartFailed) {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := svc.Apply(context.Background(), preview.RevisionID, true); !errors.Is(err, ErrListenerRestartFailed) {
		t.Fatalf("failed preview must remain reusable; got %v", err)
	}
}

type failingRestartRunner struct{}

func (failingRestartRunner) Restart(context.Context, string) error {
	return errors.New("restart failed")
}

func TestListenerService_Apply_AlreadyConsumed(t *testing.T) {
	svc := newTestService(newInMemoryListenerStore(nil), WithListenerRevisionID(func() (string, error) { return "one", nil }))
	preview, _ := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 1883)}, PreviewOptions{})
	if err := svc.Apply(context.Background(), preview.RevisionID, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(context.Background(), preview.RevisionID, true); !errors.Is(err, ErrListenerPreviewConsumed) {
		t.Fatalf("Apply() error = %v", err)
	}
}

func TestListenerService_Apply_RevisionExpired(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := newTestService(newInMemoryListenerStore(nil), WithListenerClock(func() time.Time { return now }), WithListenerRevisionID(func() (string, error) { return "one", nil }))
	preview, _ := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 1883)}, PreviewOptions{})
	now = now.Add(ListenerPreviewTTL + time.Second)
	if err := svc.Apply(context.Background(), preview.RevisionID, true); !errors.Is(err, ErrListenerPreviewExpired) {
		t.Fatalf("Apply() error = %v", err)
	}
}

func TestListenerService_AuditOnApply(t *testing.T) {
	var calls int
	svc := NewListenerService(newInMemoryListenerStore(nil), nil, &NoopRestartRunner{}, func(_ context.Context, actor, action, resourceType, resourceID, result string, _ []byte) {
		if actor == "system" && action == "listener.apply" && resourceType == "listener_config" && resourceID == "one" && result == "applied" {
			calls++
		}
	}, WithListenerRevisionID(func() (string, error) { return "one", nil }))
	preview, _ := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 1883)}, PreviewOptions{})
	if err := svc.Apply(context.Background(), preview.RevisionID, true); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("audit calls = %d, want 1", calls)
	}
}

// fakeConfWriter captures the calls made by Apply against a
// listener.ListenerConfWriter. Snapshot/Restore operate on an in-memory
// byte slice so tests can assert the rollback path without touching
// the filesystem.
type fakeConfWriter struct {
	mu           sync.Mutex
	snapshot     []byte
	written      [][]byte
	writeErr     error
	snapshotErr  error
	restoreErr   error
	restoreCalls int
	writeCalls   int
}

func (f *fakeConfWriter) Write(specs []listeners.ListenerSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writeCalls++
	if f.writeErr != nil {
		return f.writeErr
	}
	// Encode the applied spec count as a sentinel so tests can assert
	// which topology was rendered without coupling to listener.RenderAll.
	encoded := []byte(fmt.Sprintf("listeners=%d", len(specs)))
	f.written = append(f.written, encoded)
	return nil
}

func (f *fakeConfWriter) Snapshot() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snapshotErr != nil {
		return nil, f.snapshotErr
	}
	return append([]byte(nil), f.snapshot...), nil
}

func (f *fakeConfWriter) Restore(snapshot []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restoreCalls++
	if f.restoreErr != nil {
		return f.restoreErr
	}
	f.snapshot = append([]byte(nil), snapshot...)
	return nil
}

func TestListenerService_Apply_HappyPath_WritesConf(t *testing.T) {
	store := newInMemoryListenerStore(nil)
	w := &fakeConfWriter{snapshot: []byte("original-body")}
	svc := NewListenerService(store, nil, &NoopRestartRunner{}, nil,
		WithListenerRevisionID(func() (string, error) { return "one", nil }),
		WithListenerConfWriter(w),
	)
	preview, err := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 1883)}, PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(context.Background(), preview.RevisionID, true); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if w.writeCalls != 1 {
		t.Fatalf("Write calls = %d, want 1", w.writeCalls)
	}
	if w.restoreCalls != 0 {
		t.Fatalf("Restore calls = %d, want 0 on happy path", w.restoreCalls)
	}
	if rows, _ := store.ListListenerSpecs(context.Background()); len(rows) != 1 {
		t.Fatalf("stored rows = %d, want 1", len(rows))
	}
}

func TestListenerService_Apply_RestartFailure_RollsBackConf(t *testing.T) {
	store := newInMemoryListenerStore(nil)
	w := &fakeConfWriter{snapshot: []byte("original-body")}
	svc := NewListenerService(store, nil, failingRestartRunner{}, nil,
		WithListenerRevisionID(func() (string, error) { return "one", nil }),
		WithListenerConfWriter(w),
	)
	preview, _ := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 1883)}, PreviewOptions{})
	if err := svc.Apply(context.Background(), preview.RevisionID, true); !errors.Is(err, ErrListenerRestartFailed) {
		t.Fatalf("Apply() error = %v, want ErrListenerRestartFailed", err)
	}
	if w.writeCalls != 1 {
		t.Fatalf("Write calls = %d, want 1", w.writeCalls)
	}
	if w.restoreCalls != 1 {
		t.Fatalf("Restore calls = %d, want 1 on restart failure", w.restoreCalls)
	}
	if string(w.snapshot) != "original-body" {
		t.Fatalf("snapshot after restore = %q, want original-body", string(w.snapshot))
	}
	if rows, _ := store.ListListenerSpecs(context.Background()); len(rows) != 0 {
		t.Fatalf("stored rows = %d, want 0 on rollback", len(rows))
	}
}

func TestListenerService_Apply_WriteFailure_NoSQLitePersist(t *testing.T) {
	store := newInMemoryListenerStore(nil)
	w := &fakeConfWriter{snapshot: []byte("original-body"), writeErr: errors.New("disk full")}
	svc := NewListenerService(store, nil, &NoopRestartRunner{}, nil,
		WithListenerRevisionID(func() (string, error) { return "one", nil }),
		WithListenerConfWriter(w),
	)
	preview, _ := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 1883)}, PreviewOptions{})
	if err := svc.Apply(context.Background(), preview.RevisionID, true); !errors.Is(err, ErrListenerRestartFailed) {
		t.Fatalf("Apply() error = %v, want ErrListenerRestartFailed", err)
	}
	if w.restoreCalls != 1 {
		t.Fatalf("Restore calls = %d, want 1 on write failure", w.restoreCalls)
	}
	if rows, _ := store.ListListenerSpecs(context.Background()); len(rows) != 0 {
		t.Fatalf("stored rows = %d, want 0 when writer rejects", len(rows))
	}
}

func TestListenerService_Apply_NoConfWriter_PreservesLegacyBehaviour(t *testing.T) {
	store := newInMemoryListenerStore(nil)
	svc := NewListenerService(store, nil, &NoopRestartRunner{}, nil,
		WithListenerRevisionID(func() (string, error) { return "one", nil }),
	)
	preview, err := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 1883)}, PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(context.Background(), preview.RevisionID, true); err != nil {
		t.Fatalf("Apply() error = %v, want nil without conf writer", err)
	}
	if rows, _ := store.ListListenerSpecs(context.Background()); len(rows) != 1 {
		t.Fatalf("stored rows = %d, want 1", len(rows))
	}
}

func TestListenerService_Apply_SnapshotFailure_NoSQLitePersist(t *testing.T) {
	store := newInMemoryListenerStore(nil)
	w := &fakeConfWriter{snapshotErr: errors.New("read-only fs")}
	svc := NewListenerService(store, nil, &NoopRestartRunner{}, nil,
		WithListenerRevisionID(func() (string, error) { return "one", nil }),
		WithListenerConfWriter(w),
	)
	preview, _ := svc.Preview(context.Background(), []listeners.ListenerSpec{listenerSpec("one", 1883)}, PreviewOptions{})
	if err := svc.Apply(context.Background(), preview.RevisionID, true); !errors.Is(err, ErrListenerRestartFailed) {
		t.Fatalf("Apply() error = %v, want ErrListenerRestartFailed", err)
	}
	if w.writeCalls != 0 {
		t.Fatalf("Write calls = %d, want 0 when snapshot fails", w.writeCalls)
	}
	if rows, _ := store.ListListenerSpecs(context.Background()); len(rows) != 0 {
		t.Fatalf("stored rows = %d, want 0 when snapshot fails", len(rows))
	}
}
