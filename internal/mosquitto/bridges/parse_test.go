package bridges

import (
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
)

func parseConf(t *testing.T, body string) *conf.File {
	t.Helper()
	parsed, err := conf.ParseString(body, "mosquitto.conf")
	if err != nil {
		t.Fatalf("parse conf: %v", err)
	}
	return parsed
}

func TestParseReadsConnections(t *testing.T) {
	body := `listener 1883 0.0.0.0
allow_anonymous true

connection upstream
address broker.example.org:1884
remote_clientid mcm-upstream
remote_username mcm
remote_password secret
bridge_protocol_version mqttv50
cleansession false
keepalive_interval 45
restart_timeout 5 30
notifications true
notification_topic $SYS/broker/connection/upstream/state
try_private true
start_type lazy
threshold 20
idle_timeout 90
round_robin true
bridge_cafile /etc/mosquitto/ca.pem
bridge_certfile /etc/mosquitto/client.pem
bridge_keyfile /etc/mosquitto/client.key
bridge_insecure false
bridge_tls_version tlsv1.3
bridge_alpn mqtt
bridge_max_packet_size 1024
bridge_outgoing_retain true
topic both/# both 1 local/ remote/
topic sensors/+ in 0
topic commands/# out 2 localcmd/ remotecmd/
`
	doc := Parse(parseConf(t, body))
	if len(doc.Bridges) != 1 {
		t.Fatalf("bridges = %d, want 1", len(doc.Bridges))
	}
	bridge := doc.Bridges[0]
	if bridge.Name != "upstream" {
		t.Errorf("name = %q, want upstream", bridge.Name)
	}
	if bridge.Address != "broker.example.org:1884" {
		t.Errorf("address = %q, want broker.example.org:1884", bridge.Address)
	}
	if bridge.Username != "mcm" || bridge.Password != "secret" {
		t.Errorf("credentials = %q/%q, want mcm/secret", bridge.Username, bridge.Password)
	}
	if bridge.ProtocolVersion != "mqttv50" {
		t.Errorf("protocol = %q, want mqttv50", bridge.ProtocolVersion)
	}
	if bridge.CleanSession == nil || *bridge.CleanSession {
		t.Errorf("cleansession = %v, want false", bridge.CleanSession)
	}
	if bridge.KeepaliveSeconds == nil || *bridge.KeepaliveSeconds != 45 {
		t.Errorf("keepalive = %v, want 45", bridge.KeepaliveSeconds)
	}
	if bridge.RestartTimeout != "5 30" {
		t.Errorf("restart_timeout = %q, want '5 30'", bridge.RestartTimeout)
	}
	if bridge.Threshold == nil || *bridge.Threshold != 20 {
		t.Errorf("threshold = %v, want 20", bridge.Threshold)
	}
	if bridge.IdleTimeoutSeconds == nil || *bridge.IdleTimeoutSeconds != 90 {
		t.Errorf("idle_timeout = %v, want 90", bridge.IdleTimeoutSeconds)
	}
	if bridge.RoundRobin == nil || !*bridge.RoundRobin {
		t.Errorf("round_robin = %v, want true", bridge.RoundRobin)
	}
	if bridge.TLS.CAFile != "/etc/mosquitto/ca.pem" {
		t.Errorf("ca_file = %q", bridge.TLS.CAFile)
	}
	if bridge.TLS.MaxPacketSize == nil || *bridge.TLS.MaxPacketSize != 1024 {
		t.Errorf("max_packet_size = %v, want 1024", bridge.TLS.MaxPacketSize)
	}
	if bridge.TLS.OutgoingRetain == nil || !*bridge.TLS.OutgoingRetain {
		t.Errorf("outgoing_retain = %v, want true", bridge.TLS.OutgoingRetain)
	}
	if len(bridge.Topics) != 3 {
		t.Fatalf("topics = %d, want 3", len(bridge.Topics))
	}
	first := bridge.Topics[0]
	if first.Topic != "both/#" || first.Direction != DirectionBoth || first.QoS != 1 {
		t.Errorf("topic 0 = %+v", first)
	}
	if first.LocalPrefix != "local/" || first.RemotePrefix != "remote/" {
		t.Errorf("topic 0 prefixes = %q/%q", first.LocalPrefix, first.RemotePrefix)
	}
	second := bridge.Topics[1]
	if second.Direction != DirectionIn || second.QoS != 0 {
		t.Errorf("topic 1 = %+v", second)
	}
	third := bridge.Topics[2]
	if third.Direction != DirectionOut || third.QoS != 2 || third.LocalPrefix != "localcmd/" || third.RemotePrefix != "remotecmd/" {
		t.Errorf("topic 2 = %+v", third)
	}
}

func TestParseKeepsGlobalDirectivesOutOfBridges(t *testing.T) {
	body := `listener 1883 0.0.0.0
allow_anonymous true

connection upstream
address broker:1884
allow_anonymous false
topic both/# both 1
`
	doc := Parse(parseConf(t, body))
	if len(doc.Bridges) != 1 {
		t.Fatalf("bridges = %d, want 1", len(doc.Bridges))
	}
	bridge := doc.Bridges[0]
	if bridge.Username != "" {
		t.Errorf("username = %q, want empty", bridge.Username)
	}
	if len(bridge.Topics) != 1 {
		t.Errorf("topics = %d, want 1", len(bridge.Topics))
	}
}

func TestParseIgnoresConnectionInsideListenerBlock(t *testing.T) {
	body := `listener 1883 0.0.0.0 {
    allow_anonymous true
}
`
	doc := Parse(parseConf(t, body))
	if len(doc.Bridges) != 0 {
		t.Fatalf("bridges = %d, want 0", len(doc.Bridges))
	}
}

func TestParseHandlesTwoConnections(t *testing.T) {
	body := `connection first
address a:1883
topic both/a both 1

connection second
address b:1883
topic both/b both 1
`
	doc := Parse(parseConf(t, body))
	if len(doc.Bridges) != 2 {
		t.Fatalf("bridges = %d, want 2", len(doc.Bridges))
	}
	if doc.Bridges[0].Name != "first" || doc.Bridges[1].Name != "second" {
		t.Errorf("names = %q, %q", doc.Bridges[0].Name, doc.Bridges[1].Name)
	}
	if doc.Bridges[1].Address != "b:1883" {
		t.Errorf("second address = %q", doc.Bridges[1].Address)
	}
}

func TestApplyReplacesConnectionsAndKeepsEverythingElse(t *testing.T) {
	body := `# managed by hand
listener 1883 0.0.0.0
allow_anonymous true

# the old bridge
connection old
address old.example.org:1883
topic both/# both 1

persistence true
`
	parsed := parseConf(t, body)
	doc := Document{Bridges: []Bridge{{
		Name:    "upstream",
		Address: "new.example.org:1884",
		Topics:  []TopicRule{{Topic: "both/#", Direction: DirectionBoth, QoS: 1}},
	}}}
	updated := Apply(parsed, doc)
	rendered, err := updated.Render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	got := string(rendered)

	for _, want := range []string{
		"# managed by hand",
		"listener 1883 0.0.0.0",
		"allow_anonymous true",
		"persistence true",
		"connection upstream",
		"address new.example.org:1884",
		"topic both/# both 1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered config is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "connection old") {
		t.Errorf("the removed bridge is still present:\n%s", got)
	}
	if strings.Contains(got, "old.example.org") {
		t.Errorf("the removed bridge address is still present:\n%s", got)
	}
}

func TestApplyAppendsWhenNoConnectionExists(t *testing.T) {
	parsed := parseConf(t, "listener 1883 0.0.0.0\nallow_anonymous true\n")
	doc := Document{Bridges: []Bridge{{
		Name:    "upstream",
		Address: "broker:1883",
		Topics:  []TopicRule{{Topic: "both/#", Direction: DirectionBoth, QoS: 1}},
	}}}
	updated := Apply(parsed, doc)
	rendered, _ := updated.Render()
	got := string(rendered)
	if !strings.Contains(got, "listener 1883 0.0.0.0") {
		t.Errorf("the original content was lost:\n%s", got)
	}
	if !strings.Contains(got, "connection upstream") {
		t.Errorf("the bridge was not appended:\n%s", got)
	}
	if strings.Index(got, "connection upstream") < strings.Index(got, "allow_anonymous true") {
		t.Errorf("the bridge must be appended after the global directives:\n%s", got)
	}
}

func TestApplyKeepsGlobalDirectiveBetweenConnectionOptions(t *testing.T) {
	body := `connection upstream
address broker:1883
allow_anonymous true
topic both/# both 1
`
	parsed := parseConf(t, body)
	doc := Document{Bridges: []Bridge{{
		Name:    "upstream",
		Address: "broker:1883",
		Topics:  []TopicRule{{Topic: "both/#", Direction: DirectionBoth, QoS: 1}},
	}}}
	updated := Apply(parsed, doc)
	rendered, _ := updated.Render()
	got := string(rendered)
	if !strings.Contains(got, "allow_anonymous true") {
		t.Errorf("the global directive was dropped:\n%s", got)
	}
	if strings.Count(got, "address broker:1883") != 1 {
		t.Errorf("the address must be rendered once:\n%s", got)
	}
	if strings.Count(got, "topic both/# both 1") != 1 {
		t.Errorf("the topic must be rendered once:\n%s", got)
	}
}

func TestApplyRoundTripsThroughParse(t *testing.T) {
	original := Bridge{
		Name:             "upstream",
		Address:          "broker.example.org:1884",
		RemoteClientID:   "mcm-upstream",
		Username:         "mcm",
		Password:         "secret",
		ProtocolVersion:  "mqttv311",
		KeepaliveSeconds: intPtr(45),
		StartType:        "automatic",
		TLS:              TLSOptions{CAFile: "/ca.pem", Insecure: boolPtr(false)},
		Topics: []TopicRule{
			{Topic: "both/#", Direction: DirectionBoth, QoS: 1, LocalPrefix: "local/", RemotePrefix: "remote/"},
			{Topic: "in/#", Direction: DirectionIn, QoS: 0},
		},
	}
	doc := Document{Bridges: []Bridge{original}}
	parsed := parseConf(t, "listener 1883 0.0.0.0\n")
	updated := Apply(parsed, doc)
	rendered, _ := updated.Render()
	reparsed := Parse(parseConf(t, string(rendered)))
	if len(reparsed.Bridges) != 1 {
		t.Fatalf("bridges = %d, want 1", len(reparsed.Bridges))
	}
	got := reparsed.Bridges[0]
	if got.Name != original.Name || got.Address != original.Address {
		t.Errorf("identity = %+v", got)
	}
	if got.RemoteClientID != original.RemoteClientID || got.Username != original.Username || got.Password != original.Password {
		t.Errorf("credentials = %+v", got)
	}
	if got.ProtocolVersion != original.ProtocolVersion {
		t.Errorf("protocol = %q", got.ProtocolVersion)
	}
	if got.KeepaliveSeconds == nil || *got.KeepaliveSeconds != 45 {
		t.Errorf("keepalive = %v", got.KeepaliveSeconds)
	}
	if got.StartType != "automatic" {
		t.Errorf("start_type = %q", got.StartType)
	}
	if got.TLS.CAFile != "/ca.pem" {
		t.Errorf("ca_file = %q", got.TLS.CAFile)
	}
	if got.TLS.Insecure == nil || *got.TLS.Insecure {
		t.Errorf("insecure = %v", got.TLS.Insecure)
	}
	if len(got.Topics) != 2 {
		t.Fatalf("topics = %d, want 2", len(got.Topics))
	}
	if got.Topics[0].LocalPrefix != "local/" || got.Topics[0].RemotePrefix != "remote/" {
		t.Errorf("topic 0 = %+v", got.Topics[0])
	}
	if got.Topics[1].Direction != DirectionIn || got.Topics[1].QoS != 0 {
		t.Errorf("topic 1 = %+v", got.Topics[1])
	}
}

func TestRenderOmitsUnsetOptions(t *testing.T) {
	doc := Document{Bridges: []Bridge{{
		Name:    "upstream",
		Address: "broker:1883",
		Topics:  []TopicRule{{Topic: "both/#", Direction: DirectionBoth, QoS: 1}},
	}}}
	got := Render(doc)
	want := "connection upstream\n    address broker:1883\n    topic both/# both 1\n"
	if got != want {
		t.Errorf("render =\n%q\nwant\n%q", got, want)
	}
}

func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }
