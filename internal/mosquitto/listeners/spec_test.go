package listeners

import (
	"errors"
	"strings"
	"testing"
)

func TestRender_TCP_Dev(t *testing.T) {
	spec := ListenerSpec{Port: 1883, Bind: "0.0.0.0", Protocols: []Protocol{ProtocolMQTT}}

	got, err := Render(spec)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	const want = "listener 1883 0.0.0.0\n    protocol mqtt\n"
	if got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

func TestRender_WebSocket_Dev(t *testing.T) {
	spec := ListenerSpec{Port: 9001, Bind: "0.0.0.0", Protocols: []Protocol{ProtocolWebSocket}}

	got, err := Render(spec)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	const want = "listener 9001 0.0.0.0\n    protocol websockets\n"
	if got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

func TestRender_TLS_Prod(t *testing.T) {
	spec := tlsProdSpec()

	got, err := Render(spec)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	for _, want := range []string{
		"listener 8883 0.0.0.0\n",
		"    protocol mqtt\n",
		"    certfile /mosquitto/certs/server.crt\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Render() = %q, missing %q", got, want)
		}
	}
}

func TestRenderAll_Order(t *testing.T) {
	specs := []ListenerSpec{
		{Port: 1883, Bind: "127.0.0.1", Protocols: []Protocol{ProtocolMQTT}},
		{Port: 9001, Bind: "0.0.0.0", Protocols: []Protocol{ProtocolWebSocket}},
	}

	got, err := RenderAll(specs)
	if err != nil {
		t.Fatalf("RenderAll() error = %v", err)
	}
	const want = "listener 9001 0.0.0.0\n    protocol websockets\n\nlistener 1883 127.0.0.1\n    protocol mqtt\n"
	if got != want {
		t.Errorf("RenderAll() = %q, want %q", got, want)
	}
}

func TestRenderAll_Empty(t *testing.T) {
	got, err := RenderAll(nil)
	if err != nil {
		t.Fatalf("RenderAll() error = %v", err)
	}
	const want = "# no listeners configured\n"
	if got != want {
		t.Errorf("RenderAll() = %q, want %q", got, want)
	}
}

func TestParseSpec_RoundTrip(t *testing.T) {
	want, err := Render(tlsProdSpec())
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	parsed, err := ParseSpec(want)
	if err != nil {
		t.Fatalf("ParseSpec() error = %v", err)
	}
	got, err := Render(parsed)
	if err != nil {
		t.Fatalf("Render(parsed) error = %v", err)
	}
	if got != want {
		t.Errorf("Render(ParseSpec(Render(spec))) = %q, want %q", got, want)
	}
}

func TestNormalize_DefaultsBind(t *testing.T) {
	spec := ListenerSpec{Port: 1883, Protocols: []Protocol{ProtocolMQTT}}

	if err := Normalize(&spec); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if spec.Bind != "0.0.0.0" {
		t.Errorf("Bind = %q, want 0.0.0.0", spec.Bind)
	}
}

func TestNormalize_RejectsUnknownProtocol(t *testing.T) {
	spec := ListenerSpec{Protocols: []Protocol{"bogus"}}

	err := Normalize(&spec)
	if !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("Normalize() error = %v, want ErrInvalidSpec", err)
	}
}

func tlsProdSpec() ListenerSpec {
	return ListenerSpec{
		ID:        "tls-prod",
		Port:      8883,
		Bind:      "0.0.0.0",
		Protocols: []Protocol{ProtocolMQTT},
		Options: map[string]string{
			"cafile":   "/mosquitto/certs/ca.crt",
			"certfile": "/mosquitto/certs/server.crt",
			"keyfile":  "/mosquitto/certs/server.key",
		},
	}
}
