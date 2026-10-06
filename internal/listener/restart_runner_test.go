package listener

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestNoopRestartRunner_Records(t *testing.T) {
	runner := &NoopRestartRunner{}
	if err := runner.Restart(context.Background(), "mosquitto"); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	if got, want := runner.Targets, []string{"mosquitto"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Targets = %v, want %v", got, want)
	}
}

func TestDockerComposeRestartRunner_DefaultProject(t *testing.T) {
	var got []string
	runner := DockerComposeRestartRunner{
		ComposePath: "compose.yaml",
		ServiceName: "mosquitto",
		Executor: func(_ context.Context, argv []string) error {
			got = append([]string(nil), argv...)
			return nil
		},
	}
	if err := runner.Restart(context.Background(), ""); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	want := []string{"docker", "compose", "-f", "compose.yaml", "-p", "mcm", "restart", "mosquitto"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
}

func TestDockerComposeRestartRunner_NonZeroExit(t *testing.T) {
	exitErr := errors.New("exit status 1")
	runner := DockerComposeRestartRunner{
		ComposePath: "compose.yaml", ServiceName: "mosquitto",
		Executor: func(context.Context, []string) error { return exitErr },
	}
	if err := runner.Restart(context.Background(), ""); !errors.Is(err, exitErr) {
		t.Fatalf("Restart() error = %v, want wrapped %v", err, exitErr)
	}
}

func TestDockerComposeRestartRunner_MissingConfig(t *testing.T) {
	runner := DockerComposeRestartRunner{}
	if err := runner.Restart(context.Background(), ""); !errors.Is(err, ErrListenerRestartConfigMissing) {
		t.Fatalf("Restart() error = %v, want ErrListenerRestartConfigMissing", err)
	}
}
