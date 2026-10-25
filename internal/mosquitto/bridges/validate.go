package bridges

import (
	"fmt"
	"regexp"
	"strings"
)

// namePattern is the set of connection names MCM accepts. Mosquitto uses
// the connection name as an identifier in its logs and in the $SYS topics
// it publishes, so MCM keeps it to characters that survive every log
// format without escaping.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// hostnamePattern accepts a DNS label sequence.
var hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// Validate checks a bridge document and returns every finding. The
// returned slice is empty when the document is valid.
//
// localAddresses holds the `host:port` pairs of the listeners of the
// managed broker. It is used to refuse a bridge that connects the broker
// to itself, which would loop every message. It may be empty when the
// caller does not know the local listeners.
func Validate(doc Document, localAddresses []string) []Issue {
	var issues []Issue

	seen := map[string]struct{}{}
	seenRemoteIDs := map[string]string{}
	seenLocalIDs := map[string]string{}
	for _, bridge := range doc.Bridges {
		issues = append(issues, validateBridge(bridge)...)
		if bridge.Name != "" {
			if _, dup := seen[bridge.Name]; dup {
				issues = append(issues, Issue{
					Kind:    IssueKindDuplicate,
					Bridge:  bridge.Name,
					Field:   "name",
					Message: fmt.Sprintf("bridge %q is declared more than once", bridge.Name),
				})
			}
			seen[bridge.Name] = struct{}{}
		}
		// Mosquitto derives the local client id from the remote one
		// ("local.<remote_clientid>") and refuses the file when two
		// bridges end up with the same client id.
		if bridge.RemoteClientID != "" {
			if other, dup := seenRemoteIDs[bridge.RemoteClientID]; dup {
				issues = append(issues, Issue{
					Kind:    IssueKindDuplicate,
					Bridge:  bridge.Name,
					Field:   "remote_client_id",
					Message: fmt.Sprintf("the remote client id %q is already used by the bridge %q; Mosquitto refuses two bridges with the same client id", bridge.RemoteClientID, other),
				})
			}
			seenRemoteIDs[bridge.RemoteClientID] = bridge.Name
		}
		if bridge.LocalClientID != "" {
			if other, dup := seenLocalIDs[bridge.LocalClientID]; dup {
				issues = append(issues, Issue{
					Kind:    IssueKindDuplicate,
					Bridge:  bridge.Name,
					Field:   "local_client_id",
					Message: fmt.Sprintf("the local client id %q is already used by the bridge %q; Mosquitto refuses two bridges with the same client id", bridge.LocalClientID, other),
				})
			}
			seenLocalIDs[bridge.LocalClientID] = bridge.Name
		}
	}

	issues = append(issues, validateLoops(doc, localAddresses)...)
	SortIssues(issues)
	return issues
}

// validateBridge checks a single bridge in isolation.
func validateBridge(bridge Bridge) []Issue {
	var issues []Issue
	name := bridge.Name

	add := func(kind, field, message string) {
		issues = append(issues, Issue{Kind: kind, Bridge: name, Field: field, Message: message})
	}

	// --- name ---
	switch {
	case strings.TrimSpace(name) == "":
		add(IssueKindName, "name", "the bridge name is required")
	case !namePattern.MatchString(name):
		add(IssueKindName, "name", "the bridge name must start with a letter or a digit and may only contain letters, digits, dots, dashes and underscores")
	}

	// --- address ---
	if strings.TrimSpace(bridge.Address) == "" {
		add(IssueKindAddress, "address", "the remote broker address is required")
	} else if _, _, err := SplitAddress(bridge.Address); err != nil {
		add(IssueKindAddress, "address", fmt.Sprintf("the remote broker address is invalid: %s", err))
	} else {
		host, _, _ := SplitAddress(bridge.Address)
		if !validHost(host) {
			add(IssueKindAddress, "address", fmt.Sprintf("the remote broker host %q is not a valid hostname or IP address", host))
		}
	}

	// --- topics ---
	if len(bridge.Topics) == 0 {
		add(IssueKindDependency, "topics", "the bridge needs at least one topic rule; Mosquitto refuses a connection without topics")
	}
	seenTopics := map[string]struct{}{}
	for _, rule := range bridge.Topics {
		issues = append(issues, validateTopicRule(name, rule, seenTopics)...)
	}

	// --- credentials ---
	if bridge.Username != "" && bridge.Password == "" {
		issues = append(issues, Issue{
			Kind:     IssueKindSecret,
			Severity: SeverityWarning,
			Bridge:   name,
			Field:    "password",
			Message:  "the bridge sets a user name but no password; the remote broker will most likely refuse the connection",
		})
	}
	if bridge.LocalUsername != "" && bridge.LocalPassword == "" {
		issues = append(issues, Issue{
			Kind:     IssueKindSecret,
			Severity: SeverityWarning,
			Bridge:   name,
			Field:    "local_password",
			Message:  "the bridge sets a local user name but no local password; the local broker will most likely refuse the connection",
		})
	}
	if bridge.Password != "" && bridge.Username == "" {
		issues = append(issues, Issue{
			Kind:     IssueKindSecret,
			Severity: SeverityWarning,
			Bridge:   name,
			Field:    "username",
			Message:  "the bridge sets a password but no user name; Mosquitto sends the password only together with a user name",
		})
	}

	// --- protocol and behaviour ---
	if bridge.ProtocolVersion != "" && !contains(ProtocolVersions(), bridge.ProtocolVersion) {
		add(IssueKindValue, "protocol_version", fmt.Sprintf("the bridge protocol version must be one of %s", strings.Join(ProtocolVersions(), ", ")))
	}
	if bridge.StartType != "" && !contains(StartTypes(), bridge.StartType) {
		add(IssueKindValue, "start_type", fmt.Sprintf("the bridge start type must be one of %s", strings.Join(StartTypes(), ", ")))
	}
	if bridge.KeepaliveSeconds != nil && (*bridge.KeepaliveSeconds < 5 || *bridge.KeepaliveSeconds > 65535) {
		add(IssueKindValue, "keepalive_seconds", "the bridge keepalive must be between 5 and 65535 seconds")
	}
	if bridge.Threshold != nil && *bridge.Threshold < 1 {
		add(IssueKindValue, "threshold", "the bridge threshold must be at least 1")
	}
	if bridge.IdleTimeoutSeconds != nil && *bridge.IdleTimeoutSeconds < 0 {
		add(IssueKindValue, "idle_timeout_seconds", "the bridge idle timeout must not be negative")
	}
	if bridge.TLS.MaxPacketSize != nil && *bridge.TLS.MaxPacketSize < 0 {
		add(IssueKindValue, "tls.max_packet_size", "the bridge maximum packet size must not be negative")
	}
	if bridge.RestartTimeout != "" {
		if err := validateRestartTimeout(bridge.RestartTimeout); err != nil {
			add(IssueKindValue, "restart_timeout", err.Error())
		}
	}

	// --- notifications ---
	if bridge.NotificationTopic != "" && bridge.Notifications != nil && !*bridge.Notifications {
		add(IssueKindDependency, "notification_topic", "the bridge sets a notification topic but the notifications are disabled")
	}

	// --- TLS dependencies ---
	tls := bridge.TLS
	if tls.CertFile != "" && tls.KeyFile == "" {
		add(IssueKindDependency, "tls.key_file", "the bridge presents a client certificate but no private key; set the key file or remove the certificate")
	}
	if tls.KeyFile != "" && tls.CertFile == "" {
		add(IssueKindDependency, "tls.cert_file", "the bridge sets a private key but no client certificate; set the certificate file or remove the key")
	}
	if tls.PSK != "" && tls.CertFile != "" {
		add(IssueKindDependency, "tls.psk", "the bridge uses a pre-shared key and a client certificate at the same time; Mosquitto accepts only one")
	}
	if tls.PSK != "" && tls.Identity == "" {
		add(IssueKindDependency, "tls.identity", "the bridge uses a pre-shared key but no identity; Mosquitto refuses the handshake without one")
	}
	if tls.TLSVersion != "" && !contains(TLSVersions(), tls.TLSVersion) {
		add(IssueKindValue, "tls.tls_version", fmt.Sprintf("the bridge TLS version must be one of %s", strings.Join(TLSVersions(), ", ")))
	}
	if tls.Insecure != nil && *tls.Insecure && tls.CAFile == "" && tls.CAPath == "" {
		issues = append(issues, Issue{
			Kind:     IssueKindSecret,
			Severity: SeverityWarning,
			Bridge:   name,
			Field:    "tls.insecure",
			Message:  "the bridge does not verify the certificate of the remote broker; anyone able to intercept the connection can read the traffic",
		})
	}

	return issues
}

// validateTopicRule checks one topic rule of a bridge.
func validateTopicRule(bridgeName string, rule TopicRule, seen map[string]struct{}) []Issue {
	var issues []Issue

	add := func(kind, message string) {
		issues = append(issues, Issue{
			Kind:    kind,
			Bridge:  bridgeName,
			Field:   "topics",
			Topic:   rule.Topic,
			Message: message,
		})
	}

	if strings.TrimSpace(rule.Topic) == "" {
		add(IssueKindValue, "a topic rule needs a topic pattern")
		return issues
	}
	if err := ValidateTopicPattern(rule.Topic); err != nil {
		add(IssueKindValue, fmt.Sprintf("the topic pattern %q is invalid: %s", rule.Topic, err))
	}
	if rule.Direction != "" && !rule.Direction.Valid() {
		add(IssueKindValue, fmt.Sprintf("the topic direction %q must be one of %s", rule.Direction, strings.Join(directionNames(), ", ")))
	}
	if rule.QoS < 0 || rule.QoS > 2 {
		add(IssueKindValue, fmt.Sprintf("the topic quality of service %d must be 0, 1 or 2", rule.QoS))
	}
	if rule.LocalPrefix != "" {
		if err := ValidateTopicPattern(rule.LocalPrefix); err != nil {
			add(IssueKindValue, fmt.Sprintf("the local prefix %q is invalid: %s", rule.LocalPrefix, err))
		}
	}
	if rule.RemotePrefix != "" {
		if err := ValidateTopicPattern(rule.RemotePrefix); err != nil {
			add(IssueKindValue, fmt.Sprintf("the remote prefix %q is invalid: %s", rule.RemotePrefix, err))
		}
	}

	key := rule.Topic + "|" + string(rule.EffectiveDirection())
	if _, dup := seen[key]; dup {
		add(IssueKindDuplicate, fmt.Sprintf("the topic rule %q is declared more than once in this bridge", rule.Topic))
	}
	seen[key] = struct{}{}

	return issues
}

// validateLoops refuses the configurations in which the broker would feed
// its own traffic back into itself.
//
// Three shapes are detected:
//
//  1. the bridge connects the broker to one of its own listeners;
//  2. two rules of the same bridge bounce the same traffic between the
//     local and the remote side;
//  3. two bridges share the same remote broker and bridge overlapping
//     topics, so the traffic the first one sends comes back through the
//     second one.
func validateLoops(doc Document, localAddresses []string) []Issue {
	var issues []Issue

	// 1. the bridge connects the broker to itself.
	for _, bridge := range doc.Bridges {
		if bridge.Address == "" {
			continue
		}
		host, port, err := SplitAddress(bridge.Address)
		if err != nil {
			continue
		}
		for _, local := range localAddresses {
			localHost, localPort, err := SplitAddress(local)
			if err != nil {
				continue
			}
			if port == localPort && bindMatches(localHost, host) {
				issues = append(issues, Issue{
					Kind:    IssueKindLoop,
					Bridge:  bridge.Name,
					Field:   "address",
					Message: fmt.Sprintf("the bridge connects to %s, which is a listener of this broker; the broker would receive every message it sends", bridge.Address),
				})
			}
		}
	}

	// 2. two rules of the same bridge bounce the same traffic.
	for _, bridge := range doc.Bridges {
		for i := range bridge.Topics {
			for j := range bridge.Topics {
				if i == j {
					continue
				}
				outRule, inRule := bridge.Topics[i], bridge.Topics[j]
				if !outRule.EffectiveDirection().Outbound() || !inRule.EffectiveDirection().Inbound() {
					continue
				}
				if !TopicsOverlap(remoteTopic(outRule), remoteTopic(inRule)) {
					continue
				}
				if !TopicsOverlap(localTopic(inRule), localTopic(outRule)) {
					continue
				}
				issues = append(issues, Issue{
					Kind:    IssueKindLoop,
					Bridge:  bridge.Name,
					Field:   "topics",
					Topic:   inRule.Topic,
					Message: fmt.Sprintf("the topic rules %q and %q send the same traffic back and forth between this broker and the remote one", outRule.Topic, inRule.Topic),
				})
			}
		}
	}

	// 3. two bridges share the same remote broker.
	for i := range doc.Bridges {
		for j := i + 1; j < len(doc.Bridges); j++ {
			a, b := doc.Bridges[i], doc.Bridges[j]
			if !sameAddress(a.Address, b.Address) || a.Address == "" {
				continue
			}
			for _, ra := range a.Topics {
				for _, rb := range b.Topics {
					if !TopicsOverlap(localTopic(ra), localTopic(rb)) {
						continue
					}
					outbound := ra.EffectiveDirection().Outbound() || rb.EffectiveDirection().Outbound()
					inbound := ra.EffectiveDirection().Inbound() || rb.EffectiveDirection().Inbound()
					if !outbound || !inbound {
						continue
					}
					issues = append(issues, Issue{
						Kind:    IssueKindLoop,
						Bridge:  b.Name,
						Field:   "address",
						Topic:   rb.Topic,
						Message: fmt.Sprintf("the bridge %q and the bridge %q bridge overlapping topics through the same remote broker %s; the traffic they send comes back and is forwarded again", a.Name, b.Name, a.Address),
					})
				}
			}
		}
	}

	return issues
}

// localTopic is the topic a rule publishes on the local broker.
func localTopic(rule TopicRule) string {
	return joinTopic(rule.LocalPrefix, rule.Topic)
}

// remoteTopic is the topic a rule publishes on the remote broker.
func remoteTopic(rule TopicRule) string {
	return joinTopic(rule.RemotePrefix, rule.Topic)
}

func joinTopic(prefix, topic string) string {
	if prefix == "" {
		return topic
	}
	return prefix + "/" + strings.TrimPrefix(topic, "/")
}

// sameAddress reports whether two addresses point at the same broker. The
// default port is applied so "broker" and "broker:1883" compare equal.
func sameAddress(a, b string) bool {
	hostA, portA, errA := SplitAddress(a)
	hostB, portB, errB := SplitAddress(b)
	if errA != nil || errB != nil {
		return false
	}
	return portA == portB && sameHost(hostA, hostB)
}

// sameHost compares two hosts, ignoring the case of a DNS name.
func sameHost(a, b string) bool {
	if a == b {
		return true
	}
	if isIP(a) || isIP(b) {
		return false
	}
	return strings.EqualFold(a, b)
}

// bindMatches reports whether a listener bound to bind accepts connections
// to host. A listener bound to the wildcard address accepts every host of
// the same family, so a bridge that connects to 127.0.0.1 reaches a broker
// that listens on 0.0.0.0.
func bindMatches(bind, host string) bool {
	switch bind {
	case "0.0.0.0":
		return !strings.Contains(host, ":")
	case "::", "[::]":
		return true
	}
	return sameHost(bind, host)
}

// ValidateTopicPattern checks an MQTT topic filter. Wildcards are only
// accepted in the way the MQTT specification allows: "#" as the last level
// and "+" as a whole level.
func ValidateTopicPattern(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("the pattern is empty")
	}
	if strings.ContainsAny(pattern, " \t") {
		return fmt.Errorf("the pattern must not contain spaces")
	}
	if strings.HasPrefix(pattern, "$") {
		// $SYS topics are valid filters, but a leading wildcard never
		// matches them; that is a rule of the MQTT specification and not
		// an error, so the check stops here.
		return nil
	}
	levels := strings.Split(pattern, "/")
	for i, level := range levels {
		if strings.Contains(level, "#") {
			if level != "#" {
				return fmt.Errorf("the multi-level wildcard must occupy a whole level")
			}
			if i != len(levels)-1 {
				return fmt.Errorf("the multi-level wildcard must be the last level")
			}
		}
		if strings.Contains(level, "+") && level != "+" {
			return fmt.Errorf("the single-level wildcard must occupy a whole level")
		}
	}
	return nil
}

// TopicsOverlap reports whether two MQTT topic filters can match a common
// topic. It is the building block of the loop detection: two filters
// overlap when there is at least one topic both of them match.
func TopicsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	levelsA := strings.Split(a, "/")
	levelsB := strings.Split(b, "/")
	for i := 0; i < len(levelsA) && i < len(levelsB); i++ {
		la, lb := levelsA[i], levelsB[i]
		if la == "#" || lb == "#" {
			return true
		}
		if la == "+" || lb == "+" {
			continue
		}
		if la != lb {
			return false
		}
	}
	// One filter is a prefix of the other. They only overlap when the
	// longer one ends with the multi-level wildcard.
	if len(levelsA) > len(levelsB) {
		return levelsA[len(levelsB)] == "#"
	}
	if len(levelsB) > len(levelsA) {
		return levelsB[len(levelsA)] == "#"
	}
	return true
}

// validateRestartTimeout checks the "restart_timeout" value. Mosquitto
// accepts either a single number of seconds or a "base cap" pair.
func validateRestartTimeout(value string) error {
	fields := strings.Fields(value)
	if len(fields) == 0 || len(fields) > 2 {
		return fmt.Errorf("the restart timeout must be a number of seconds or a base and cap pair")
	}
	for _, field := range fields {
		value, err := parsePort(field)
		if err != nil {
			return fmt.Errorf("the restart timeout must be a number of seconds or a base and cap pair")
		}
		if value < 1 {
			return fmt.Errorf("the restart timeout must be at least 1 second")
		}
	}
	return nil
}

// validHost reports whether host is a DNS name or an IP address.
func validHost(host string) bool {
	if host == "" {
		return false
	}
	if isIP(host) {
		return true
	}
	if len(host) > 253 {
		return false
	}
	return hostnamePattern.MatchString(host)
}

// isIP reports whether host is an IPv4 or IPv6 literal.
func isIP(host string) bool {
	return regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`).MatchString(host) ||
		regexp.MustCompile(`^[0-9A-Fa-f:]+$`).MatchString(host) && strings.Contains(host, ":")
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func directionNames() []string {
	out := make([]string, 0, 3)
	for _, d := range Directions() {
		out = append(out, string(d))
	}
	return out
}
