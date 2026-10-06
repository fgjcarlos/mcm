package listener

import (
	"context"
	"errors"
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
