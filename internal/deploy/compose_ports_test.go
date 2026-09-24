package deploy

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseComposePorts_ShortForm(t *testing.T) {
	ports, err := parseComposePorts("services:\n  mosquitto:\n    ports:\n      - \"1883:1883\"\n", "mosquitto")
	if err != nil {
		t.Fatalf("parseComposePorts: %v", err)
	}
	if want := []int{1883}; !reflect.DeepEqual(ports, want) {
		t.Errorf("ports = %v, want %v", ports, want)
	}
}

func TestParseComposePorts_BarePort(t *testing.T) {
	ports, err := parseComposePorts("services:\n  mosquitto:\n    ports:\n      - \"9001\"\n", "mosquitto")
	if err != nil {
		t.Fatalf("parseComposePorts: %v", err)
	}
	if want := []int{9001}; !reflect.DeepEqual(ports, want) {
		t.Errorf("ports = %v, want %v", ports, want)
	}
}

func TestParseComposePorts_LongForm(t *testing.T) {
	ports, err := parseComposePorts("services:\n  mosquitto:\n    ports:\n      - {target: 8883, published: 8883, protocol: tcp, mode: host}\n", "mosquitto")
	if err != nil {
		t.Fatalf("parseComposePorts: %v", err)
	}
	if want := []int{8883}; !reflect.DeepEqual(ports, want) {
		t.Errorf("ports = %v, want %v", ports, want)
	}
}

func TestParseComposePorts_Mixed(t *testing.T) {
	text := "services:\n  mosquitto:\n    ports:\n      - \"1883:1883\"\n      - target: 8883\n        published: 8883\n      - {target: 9001, published: 9001}\n"
	ports, err := parseComposePorts(text, "mosquitto")
	if err != nil {
		t.Fatalf("parseComposePorts: %v", err)
	}
	if want := []int{1883, 8883, 9001}; !reflect.DeepEqual(ports, want) {
		t.Errorf("ports = %v, want %v", ports, want)
	}
}

func TestParseComposePorts_WrongService(t *testing.T) {
	ports, err := parseComposePorts("services:\n  other:\n    ports:\n      - \"1883:1883\"\n", "mosquitto")
	if err != nil {
		t.Fatalf("parseComposePorts: %v", err)
	}
	if len(ports) != 0 {
		t.Errorf("ports = %v, want empty", ports)
	}
}

func TestComposePorts_Disabled(t *testing.T) {
	reader := NewComposePortsReader(func(string) ([]byte, error) {
		t.Fatal("disabled reader must not read a file")
		return nil, nil
	}, time.Now)
	reader.Configure("", "mosquitto")
	if !reader.Disabled() {
		t.Error("Disabled() = false, want true")
	}
	ports, err := reader.HostPorts(context.Background())
	if err != nil {
		t.Fatalf("HostPorts: %v", err)
	}
	if ports != nil {
		t.Errorf("ports = %v, want nil", ports)
	}
}

func TestComposePorts_Caching(t *testing.T) {
	now := time.Unix(100, 0)
	reads := 0
	reader := NewComposePortsReader(func(string) ([]byte, error) {
		reads++
		return []byte("services:\n  mosquitto:\n    ports:\n      - \"1883:1883\"\n"), nil
	}, func() time.Time { return now })
	reader.Configure("compose.yml", "mosquitto")

	for _, advance := range []time.Duration{0, 4 * time.Second, 2 * time.Second} {
		now = now.Add(advance)
		if _, err := reader.HostPorts(context.Background()); err != nil {
			t.Fatalf("HostPorts: %v", err)
		}
	}
	if reads != 2 {
		t.Errorf("reads = %d, want 2", reads)
	}
}

func TestComposePorts_FileReadError(t *testing.T) {
	reader := NewComposePortsReader(func(string) ([]byte, error) {
		return nil, errors.New("broken reader")
	}, time.Now)
	reader.Configure("compose.yml", "mosquitto")
	_, err := reader.HostPorts(context.Background())
	if err == nil || !strings.Contains(err.Error(), "compose.yml") {
		t.Errorf("HostPorts error = %v, want path-wrapping error", err)
	}
}

func TestComposePorts_DeterministicOrder(t *testing.T) {
	ports, err := parseComposePorts("services:\n  mosquitto:\n    ports:\n      - \"9001:9001\"\n      - \"1883:1883\"\n      - \"9001:9001\"\n", "mosquitto")
	if err != nil {
		t.Fatalf("parseComposePorts: %v", err)
	}
	if want := []int{1883, 9001}; !reflect.DeepEqual(ports, want) {
		t.Errorf("ports = %v, want %v", ports, want)
	}
}

func TestComposePorts_DevComposeFile_Real(t *testing.T) {
	const path = "/home/composedof2/Dev/Codex/mcm/.worktrees/feat-299-listeners/docker-compose.yml"
	reader := NewComposePortsReader(os.ReadFile, time.Now)
	reader.Configure(path, "mosquitto")
	ports, err := reader.HostPorts(context.Background())
	if err != nil {
		t.Fatalf("HostPorts: %v", err)
	}
	if want := []int{1883, 9001}; !reflect.DeepEqual(ports, want) {
		t.Errorf("ports = %v, want %v", ports, want)
	}
}
