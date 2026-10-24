package writer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/mosquitto/listeners"
)

// TestWriteEnablesPerListenerSettingsForAnACLFile pins that a listener
// that declares its own ACL file turns the per-listener access control
// settings on.
//
// Mosquitto ignores an `acl_file` declared inside a listener block while
// `per_listener_settings` is off, so the scoped rules of that listener
// would never apply.
func TestWriteEnablesPerListenerSettingsForAnACLFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	if err := os.WriteFile(confPath, []byte("allow_anonymous false\n"), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}

	w := New(confPath)
	specs := []listeners.ListenerSpec{{
		ID:        "tls",
		Port:      8883,
		Bind:      "0.0.0.0",
		Protocols: []listeners.Protocol{listeners.ProtocolMQTT},
		Options:   map[string]string{"acl_file": "/mosquitto/config/acl.tls"},
	}}

	rendered, err := w.Write(specs)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if !strings.Contains(rendered.Full, "per_listener_settings true") {
		t.Fatalf("the written config does not enable the per-listener settings:\n%s", rendered.Full)
	}
	if !strings.Contains(rendered.Full, "acl_file /mosquitto/config/acl.tls") {
		t.Fatalf("the written config does not declare the acl_file:\n%s", rendered.Full)
	}
}

// TestWriteKeepsPerListenerSettingsEnabled pins that an already enabled
// directive is not duplicated.
func TestWriteKeepsPerListenerSettingsEnabled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	if err := os.WriteFile(confPath, []byte("per_listener_settings true\nallow_anonymous false\n"), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}

	w := New(confPath)
	specs := []listeners.ListenerSpec{{
		ID:        "tls",
		Port:      8883,
		Bind:      "0.0.0.0",
		Protocols: []listeners.Protocol{listeners.ProtocolMQTT},
		Options:   map[string]string{"acl_file": "/mosquitto/config/acl.tls"},
	}}

	rendered, err := w.Write(specs)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if strings.Count(rendered.Full, "per_listener_settings") != 1 {
		t.Fatalf("the written config repeats the per-listener settings:\n%s", rendered.Full)
	}
}

// TestWriteInheritsTheGlobalAccessControl pins that the listener blocks
// receive the global password file and the global ACL file.
//
// Mosquitto resolves the authentication and the access control per
// listener once the per-listener settings are enabled: a listener block
// that declares neither a password file nor an ACL file has no access
// control at all, and a client that authenticates is refused.
func TestWriteInheritsTheGlobalAccessControl(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	body := "allow_anonymous false\npassword_file /mosquitto/config/passwd\nacl_file /mosquitto/config/acl\n"
	if err := os.WriteFile(confPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}

	w := New(confPath)
	specs := []listeners.ListenerSpec{{
		ID:        "plain",
		Port:      1883,
		Bind:      "0.0.0.0",
		Protocols: []listeners.Protocol{listeners.ProtocolMQTT},
		Options:   map[string]string{},
	}, {
		ID:        "tls",
		Port:      8883,
		Bind:      "0.0.0.0",
		Protocols: []listeners.Protocol{listeners.ProtocolMQTT},
		Options:   map[string]string{"acl_file": "/mosquitto/config/acl.tls"},
	}}

	rendered, err := w.Write(specs)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	// The listener without a file of its own keeps the broker-wide one.
	if !strings.Contains(rendered.Full, "acl_file /mosquitto/config/acl\n") &&
		!strings.Contains(rendered.Full, "acl_file /mosquitto/config/acl\r\n") {
		t.Fatalf("the listener blocks do not carry the broker-wide acl file:\n%s", rendered.Full)
	}
	if !strings.Contains(rendered.Full, "password_file /mosquitto/config/passwd") {
		t.Fatalf("the listener blocks do not carry the broker-wide password file:\n%s", rendered.Full)
	}
	// The listener with a file of its own keeps it.
	if !strings.Contains(rendered.Full, "acl_file /mosquitto/config/acl.tls") {
		t.Fatalf("the scoped listener lost its own acl file:\n%s", rendered.Full)
	}
}

// TestWriteRefusesDisabledPerListenerSettings pins that MCM refuses a
// configuration that turns the per-listener settings off while a listener
// declares its own ACL file.
//
// The alternative would be a successful apply whose scoped rules never
// reach the broker.
func TestWriteRefusesDisabledPerListenerSettings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	if err := os.WriteFile(confPath, []byte("per_listener_settings false\n"), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}

	w := New(confPath)
	specs := []listeners.ListenerSpec{{
		ID:        "tls",
		Port:      8883,
		Bind:      "0.0.0.0",
		Protocols: []listeners.Protocol{listeners.ProtocolMQTT},
		Options:   map[string]string{"acl_file": "/mosquitto/config/acl.tls"},
	}}

	if _, err := w.Write(specs); err == nil {
		t.Fatal("Write() accepted a configuration that disables the per-listener settings")
	}
}

// TestWriteLeavesGlobalConfigAloneWithoutAnACLFile pins that a
// configuration without a listener-local ACL file is not touched: the
// global access control keeps its meaning.
func TestWriteLeavesGlobalConfigAloneWithoutAnACLFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	if err := os.WriteFile(confPath, []byte("allow_anonymous false\n"), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}

	w := New(confPath)
	specs := []listeners.ListenerSpec{{
		ID:        "plain",
		Port:      1883,
		Bind:      "0.0.0.0",
		Protocols: []listeners.Protocol{listeners.ProtocolMQTT},
	}}

	rendered, err := w.Write(specs)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if strings.Contains(rendered.Full, "per_listener_settings") {
		t.Fatalf("the written config enables the per-listener settings although no listener declares an acl_file:\n%s", rendered.Full)
	}
}
