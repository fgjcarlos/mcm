package listeners

import (
	"bufio"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Protocol identifies the transport protocol served by a listener.
type Protocol string

const (
	// ProtocolMQTT is the native MQTT transport protocol.
	ProtocolMQTT Protocol = "mqtt"
	// ProtocolWebSocket is the MQTT-over-WebSocket transport protocol.
	ProtocolWebSocket Protocol = "websockets"
)

// IsValid reports whether p is a supported listener protocol.
func (p Protocol) IsValid() bool {
	return p == ProtocolMQTT || p == ProtocolWebSocket
}

// ListenerSpec describes one Mosquitto listener block.
type ListenerSpec struct {
	ID            string
	Port          int
	Bind          string
	Protocols     []Protocol
	Options       map[string]string
	RestartNeeded bool
}

// Normalize applies listener defaults and canonicalizes protocol values.
func Normalize(spec *ListenerSpec) error {
	if spec == nil {
		return ErrInvalidSpec
	}
	if spec.Bind == "" {
		spec.Bind = "0.0.0.0"
	}
	for i, protocol := range spec.Protocols {
		spec.Protocols[i] = Protocol(strings.ToLower(string(protocol)))
		if !spec.Protocols[i].IsValid() {
			return ErrInvalidSpec
		}
	}
	return nil
}

// Render returns the Mosquitto configuration block for spec.
func Render(spec ListenerSpec) (string, error) {
	if err := Normalize(&spec); err != nil {
		return "", err
	}
	issues, _ := Validate([]ListenerSpec{spec})
	if len(issues) != 0 {
		return "", ErrInvalidSpec
	}

	protocols := append([]Protocol(nil), spec.Protocols...)
	sort.SliceStable(protocols, func(i, j int) bool { return protocols[i] < protocols[j] })

	var builder strings.Builder
	fmt.Fprintf(&builder, "listener %d %s\n", spec.Port, spec.Bind)
	for _, protocol := range protocols {
		fmt.Fprintf(&builder, "    protocol %s\n", protocol)
	}
	keys := sortedOptionKeys(spec.Options)
	for _, key := range keys {
		fmt.Fprintf(&builder, "    %s %s\n", key, renderOptionValue(spec.Options[key]))
	}
	return builder.String(), nil
}

// RenderAll renders specs ordered by bind address and port.
func RenderAll(specs []ListenerSpec) (string, error) {
	if len(specs) == 0 {
		return "# no listeners configured\n", nil
	}

	ordered := append([]ListenerSpec(nil), specs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Bind == ordered[j].Bind {
			return ordered[i].Port < ordered[j].Port
		}
		return ordered[i].Bind < ordered[j].Bind
	})

	blocks := make([]string, 0, len(ordered))
	for _, spec := range ordered {
		block, err := Render(spec)
		if err != nil {
			return "", err
		}
		blocks = append(blocks, strings.TrimSuffix(block, "\n"))
	}
	return strings.Join(blocks, "\n\n") + "\n", nil
}

// ParseSpec parses one listener block produced by Render.
func ParseSpec(text string) (ListenerSpec, error) {
	var spec ListenerSpec
	var headerFound bool

	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "listener" {
			if headerFound || len(fields) != 3 {
				return ListenerSpec{}, ErrInvalidSpec
			}
			port, err := strconv.Atoi(fields[1])
			if err != nil {
				return ListenerSpec{}, ErrInvalidSpec
			}
			spec.Port, spec.Bind, headerFound = port, fields[2], true
			continue
		}
		if !headerFound || len(fields) < 2 {
			return ListenerSpec{}, ErrInvalidSpec
		}
		if fields[0] == "protocol" {
			if len(fields) != 2 {
				return ListenerSpec{}, ErrInvalidSpec
			}
			protocol := Protocol(strings.ToLower(fields[1]))
			if !protocol.IsValid() {
				return ListenerSpec{}, ErrInvalidSpec
			}
			spec.Protocols = append(spec.Protocols, protocol)
			continue
		}

		value, err := parseOptionValue(strings.TrimSpace(strings.TrimPrefix(line, fields[0])))
		if err != nil {
			return ListenerSpec{}, ErrInvalidSpec
		}
		if spec.Options == nil {
			spec.Options = make(map[string]string)
		}
		if _, exists := spec.Options[fields[0]]; exists {
			return ListenerSpec{}, ErrInvalidSpec
		}
		spec.Options[fields[0]] = value
	}
	if err := scanner.Err(); err != nil {
		return ListenerSpec{}, err
	}
	if !headerFound {
		return ListenerSpec{}, ErrInvalidSpec
	}
	if err := Normalize(&spec); err != nil {
		return ListenerSpec{}, err
	}
	issues, _ := Validate([]ListenerSpec{spec})
	if len(issues) != 0 {
		return ListenerSpec{}, ErrInvalidSpec
	}
	return spec, nil
}

func parseOptionValue(value string) (string, error) {
	if strings.HasPrefix(value, "\"") {
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return "", err
		}
		return unquoted, nil
	}
	if strings.ContainsAny(value, " \t") {
		return "", errors.New("unquoted option value contains whitespace")
	}
	return value, nil
}

func renderOptionValue(value string) string {
	if strings.ContainsAny(value, " \t\n\r\"") {
		return strconv.Quote(value)
	}
	return value
}

func sortedOptionKeys(options map[string]string) []string {
	keys := make([]string, 0, len(options))
	for key := range options {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
