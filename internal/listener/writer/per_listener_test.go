package writer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/mosquitto"
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
		Options:   map[string]string{"acl_file": writtenACLFile(t, dir, "tls")},
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

// TestWritePlacesPerListenerSettingsBeforeTheSecuritySettings pins the
// position of the directive.
//
// Mosquitto only honours `per_listener_settings` when it is set before the
// first security setting: with the directive behind `allow_anonymous`,
// `password_file` or `acl_file` the broker refuses to start with
// "per_listener_settings must be set before any other security settings."
func TestWritePlacesPerListenerSettingsBeforeTheSecuritySettings(t *testing.T) {
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
		Port:      1886,
		Bind:      "0.0.0.0",
		Protocols: []listeners.Protocol{listeners.ProtocolMQTT},
		Options:   map[string]string{"acl_file": writtenACLFile(t, dir, "plain")},
	}}

	rendered, err := w.Write(specs)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	directive := strings.Index(rendered.Full, "per_listener_settings")
	if directive < 0 {
		t.Fatalf("the written config does not enable the per-listener settings:\n%s", rendered.Full)
	}
	for _, directive := range []string{"allow_anonymous", "password_file", "acl_file"} {
		at := strings.Index(rendered.Full, directive)
		if at < 0 {
			continue
		}
		if at < strings.Index(rendered.Full, "per_listener_settings") {
			t.Fatalf("the security setting %q precedes the per-listener settings:\n%s", directive, rendered.Full)
		}
	}
}

// TestWriteMovesAMisplacedPerListenerSettingsDirective pins that a
// directive that is enabled but sits behind a security setting is moved.
//
// Mosquitto ignores a directive it meets after a security setting and
// refuses to start, so leaving it in place would break the broker.
func TestWriteMovesAMisplacedPerListenerSettingsDirective(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	body := "allow_anonymous false\npassword_file /mosquitto/config/passwd\nper_listener_settings true\nacl_file /mosquitto/config/acl\n"
	if err := os.WriteFile(confPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}

	w := New(confPath)
	specs := []listeners.ListenerSpec{{
		ID:        "plain",
		Port:      1886,
		Bind:      "0.0.0.0",
		Protocols: []listeners.Protocol{listeners.ProtocolMQTT},
		Options:   map[string]string{"acl_file": writtenACLFile(t, dir, "plain")},
	}}

	rendered, err := w.Write(specs)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if strings.Count(rendered.Full, "per_listener_settings") != 1 {
		t.Fatalf("the written config repeats the per-listener settings:\n%s", rendered.Full)
	}
	if strings.Index(rendered.Full, "per_listener_settings") > strings.Index(rendered.Full, "allow_anonymous") {
		t.Fatalf("the per-listener settings still follow a security setting:\n%s", rendered.Full)
	}
}

// TestWriteResolvesTheListenerACLFileIntoTheBrokerPath pins that the
// ACL file of a listener is declared with the path the broker reads.
//
// MCM writes the file next to the broker-wide ACL file, but under the path
// of MCM's own filesystem: in a container deployment the shared volume is
// mounted under a different path in each container. The broker reads the
// file under its own path, so the configuration has to name that one.
func TestWriteResolvesTheListenerACLFileIntoTheBrokerPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	body := "allow_anonymous false\npassword_file /mosquitto/config/passwd\nacl_file /mosquitto/config/acl\n"
	if err := os.WriteFile(confPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}

	w := New(confPath)
	specs := []listeners.ListenerSpec{{
		ID:        "scoped",
		Port:      1887,
		Bind:      "0.0.0.0",
		Protocols: []listeners.Protocol{listeners.ProtocolMQTT},
		// The path MCM writes the file to.
		Options: map[string]string{"acl_file": writtenACLFile(t, dir, "scoped")},
	}}

	rendered, err := w.Write(specs)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if !strings.Contains(rendered.Full, "acl_file /mosquitto/config/acl.scoped") {
		t.Fatalf("the listener does not declare the acl file the broker reads:\n%s", rendered.Full)
	}
	if strings.Contains(rendered.Full, "/var/lib/mosquitto-config/acl.scoped") {
		t.Fatalf("the listener declares the path of MCM's filesystem:\n%s", rendered.Full)
	}
}

// TestWriteKeepsAnOperatorProvidedACLFile pins that a file MCM does not
// manage keeps the path the operator gave it.
func TestWriteKeepsAnOperatorProvidedACLFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	body := "allow_anonymous false\npassword_file /mosquitto/config/passwd\nacl_file /mosquitto/config/acl\n"
	if err := os.WriteFile(confPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}

	w := New(confPath)
	specs := []listeners.ListenerSpec{{
		ID:        "custom",
		Port:      1887,
		Bind:      "0.0.0.0",
		Protocols: []listeners.Protocol{listeners.ProtocolMQTT},
		Options:   map[string]string{"acl_file": "/etc/mosquitto/operator.acl"},
	}}

	rendered, err := w.Write(specs)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if !strings.Contains(rendered.Full, "acl_file /etc/mosquitto/operator.acl") {
		t.Fatalf("the operator-provided acl file was rewritten:\n%s", rendered.Full)
	}
}

// writtenACLFile creates the ACL file MCM writes for a listener and returns
// its path. The listener specs carry the path MCM writes to: the broker
// reads the file under its own path, which the writer resolves from the
// broker-wide ACL file of the configuration.
func writtenACLFile(t *testing.T, dir, listenerID string) string {
	t.Helper()
	path := filepath.Join(dir, mosquitto.ListenerACLFileName(listenerID))
	if err := os.WriteFile(path, []byte("user service\n"), 0o600); err != nil {
		t.Fatalf("write acl file: %v", err)
	}
	return path
}

// TestWriteRefusesAnUnwrittenACLFile pins that MCM does not write a
// configuration that declares an ACL file it has not written.
//
// Mosquitto refuses to start when the ACL file of a listener is missing, so
// applying the listener configuration before the deployment would take the
// broker down. The deployment writes the file, so the operator has to apply
// it first.
func TestWriteRefusesAnUnwrittenACLFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	body := "allow_anonymous false\npassword_file /mosquitto/config/passwd\nacl_file /mosquitto/config/acl\n"
	if err := os.WriteFile(confPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}

	w := New(confPath)
	specs := []listeners.ListenerSpec{{
		ID:        "scoped",
		Port:      1887,
		Bind:      "0.0.0.0",
		Protocols: []listeners.Protocol{listeners.ProtocolMQTT},
		// The file the deploy would write, but it is not there yet.
		Options: map[string]string{"acl_file": filepath.Join(dir, mosquitto.ListenerACLFileName("scoped"))},
	}}

	_, err := w.Write(specs)
	if err == nil {
		t.Fatal("Write() accepted a listener whose acl file has not been written")
	}
	if !errors.Is(err, ErrACLFileNotWritten) {
		t.Fatalf("Write() error = %v, want ErrACLFileNotWritten", err)
	}
}

// TestWriteDropsTheListenerOnTheDefaultPort pins that the writer does not
// declare the listener on the default port once the per-listener settings
// are enabled.
//
// Mosquitto creates that listener itself — with the global authentication
// and access control — so a listener of the configuration on the same port
// would collide with it and the broker would refuse to start.
func TestWriteDropsTheListenerOnTheDefaultPort(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	body := "allow_anonymous false\npassword_file /mosquitto/config/passwd\nacl_file /mosquitto/config/acl\n"
	if err := os.WriteFile(confPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}

	w := New(confPath)
	specs := []listeners.ListenerSpec{{
		ID:        "default",
		Port:      1883,
		Bind:      "0.0.0.0",
		Protocols: []listeners.Protocol{listeners.ProtocolMQTT},
	}, {
		ID:        "scoped",
		Port:      1886,
		Bind:      "0.0.0.0",
		Protocols: []listeners.Protocol{listeners.ProtocolMQTT},
		Options:   map[string]string{"acl_file": writtenACLFile(t, dir, "scoped")},
	}}

	rendered, err := w.Write(specs)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if strings.Contains(rendered.Full, "listener 1883") {
		t.Fatalf("the configuration declares the listener on the default port:\n%s", rendered.Full)
	}
	if !strings.Contains(rendered.Full, "listener 1886") {
		t.Fatalf("the configuration lost the listener of the scoped rules:\n%s", rendered.Full)
	}
}

// TestWriteRefusesAScopedRuleOnTheDefaultPort pins that MCM refuses a
// listener on the default port that owns a file of its own.
//
// Mosquitto creates that listener itself with the global access control, so
// the file of the listener would never be read.
func TestWriteRefusesAScopedRuleOnTheDefaultPort(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	body := "allow_anonymous false\npassword_file /mosquitto/config/passwd\nacl_file /mosquitto/config/acl\n"
	if err := os.WriteFile(confPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}

	w := New(confPath)
	specs := []listeners.ListenerSpec{{
		ID:        "default",
		Port:      1883,
		Bind:      "0.0.0.0",
		Protocols: []listeners.Protocol{listeners.ProtocolMQTT},
		Options:   map[string]string{"acl_file": writtenACLFile(t, dir, "default")},
	}}

	_, err := w.Write(specs)
	if err == nil {
		t.Fatal("Write() accepted a listener on the default port that owns an acl file")
	}
	if !errors.Is(err, ErrDefaultListenerScope) {
		t.Fatalf("Write() error = %v, want ErrDefaultListenerScope", err)
	}
}
