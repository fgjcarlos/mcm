package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/mosquitto"
)

// fakeRestartInner records what the wrapper delegates to the applier it
// wraps.
type fakeRestartInner struct {
	applied         bool
	reloaded        bool
	recovered       bool
	innerRestart    bool
	applyErr        error
	recoverErr      error
	recoverFound    bool
	reloadOnlyCalls int
}

func (f *fakeRestartInner) Apply(_ context.Context, _ mosquitto.ApplyRequest) error {
	f.applied = true
	return f.applyErr
}

func (f *fakeRestartInner) ReloadBrokerOnly() error {
	f.reloaded = true
	f.reloadOnlyCalls++
	return nil
}

func (f *fakeRestartInner) RecoverInterruptedApply() (bool, error) {
	f.recovered = true
	return f.recoverFound, f.recoverErr
}

func (f *fakeRestartInner) RestartBroker(_ context.Context) error {
	f.innerRestart = true
	return nil
}

func (f *fakeRestartInner) CanRestart() bool { return true }

// recordingRunner records the commands it runs.
type recordingRunner struct {
	commands [][]string
	err      error
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	cmd := append([]string{name}, args...)
	r.commands = append(r.commands, cmd)
	if r.err != nil {
		return nil, r.err
	}
	return []byte("ok"), nil
}

func TestRestartApplier(t *testing.T) {
	t.Parallel()

	t.Run("without a restart command the broker cannot be restarted", func(t *testing.T) {
		t.Parallel()
		inner := &fakeRestartInner{}
		applier := NewRestartApplier(inner, nil, &recordingRunner{})
		if applier.CanRestart() {
			t.Error("CanRestart() = true, want false without a restart command")
		}
		err := applier.RestartBroker(context.Background())
		if !errors.Is(err, mosquitto.ErrRestartNotSupported) {
			t.Errorf("RestartBroker() error = %v, want ErrRestartNotSupported", err)
		}
	})

	t.Run("the restart command is invoked with its argv", func(t *testing.T) {
		t.Parallel()
		runner := &recordingRunner{}
		applier := NewRestartApplier(&fakeRestartInner{}, []string{"systemctl", "restart", "mosquitto"}, runner)
		if !applier.CanRestart() {
			t.Error("CanRestart() = false, want true with a restart command")
		}
		if err := applier.RestartBroker(context.Background()); err != nil {
			t.Fatalf("RestartBroker: %v", err)
		}
		if len(runner.commands) != 1 {
			t.Fatalf("commands = %v, want exactly one", runner.commands)
		}
		want := []string{"systemctl", "restart", "mosquitto"}
		got := runner.commands[0]
		if len(got) != len(want) {
			t.Fatalf("command = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("command = %v, want %v", got, want)
			}
		}
	})

	t.Run("a failing restart command is reported", func(t *testing.T) {
		t.Parallel()
		runner := &recordingRunner{err: errors.New("boom")}
		applier := NewRestartApplier(&fakeRestartInner{}, []string{"/usr/local/bin/restart-mosquitto"}, runner)
		err := applier.RestartBroker(context.Background())
		if err == nil {
			t.Fatal("RestartBroker: want error when the restart command fails")
		}
		if !strings.Contains(err.Error(), "restart command") {
			t.Errorf("error = %v, want it to mention the restart command", err)
		}
	})

	t.Run("the inner applier is never restarted", func(t *testing.T) {
		t.Parallel()
		inner := &fakeRestartInner{}
		applier := NewRestartApplier(inner, []string{"/usr/local/bin/restart-mosquitto"}, &recordingRunner{})
		if err := applier.RestartBroker(context.Background()); err != nil {
			t.Fatalf("RestartBroker: %v", err)
		}
		if inner.innerRestart {
			t.Error("the inner applier restarted the broker; the restart command must be the only restart path")
		}
	})

	t.Run("apply, reload and recovery are delegated", func(t *testing.T) {
		t.Parallel()
		inner := &fakeRestartInner{recoverFound: true}
		applier := NewRestartApplier(inner, []string{"/usr/local/bin/restart-mosquitto"}, &recordingRunner{})
		if err := applier.Apply(context.Background(), mosquitto.ApplyRequest{}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if !inner.applied {
			t.Error("Apply was not delegated to the inner applier")
		}
		if err := applier.ReloadBrokerOnly(); err != nil {
			t.Fatalf("ReloadBrokerOnly: %v", err)
		}
		if !inner.reloaded {
			t.Error("ReloadBrokerOnly was not delegated to the inner applier")
		}
		found, err := applier.RecoverInterruptedApply()
		if err != nil {
			t.Fatalf("RecoverInterruptedApply: %v", err)
		}
		if !found || !inner.recovered {
			t.Error("RecoverInterruptedApply was not delegated to the inner applier")
		}
	})
}
