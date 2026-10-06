package deploy

import (
	"bufio"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const composePortsCacheTTL = 5 * time.Second

// ComposeFileReader abstracts os.ReadFile for tests.
type ComposeFileReader func(path string) ([]byte, error)

// ComposePortsReader reads published host ports for a Docker Compose service.
type ComposePortsReader interface {
	Configure(path, service string)
	IsConfigured() bool
	Disabled() bool
	HostPorts(ctx context.Context) ([]int, error)
}

type composePortsReader struct {
	mu         sync.Mutex
	fileReader ComposeFileReader
	clock      func() time.Time
	path       string
	service    string
	cached     []int
	cachedAt   time.Time
}

// NewComposePortsReader constructs a ComposePortsReader with a five-second cache.
func NewComposePortsReader(fileReader ComposeFileReader, clock func() time.Time) ComposePortsReader {
	return &composePortsReader{fileReader: fileReader, clock: clock}
}

func (r *composePortsReader) Configure(path, service string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.path = path
	r.service = service
	r.cached = nil
	r.cachedAt = time.Time{}
}

func (r *composePortsReader) IsConfigured() bool {
	return !r.Disabled()
}

func (r *composePortsReader) Disabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.TrimSpace(r.path) == ""
}

func (r *composePortsReader) HostPorts(ctx context.Context) ([]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(r.path) == "" {
		return nil, nil
	}

	now := r.clock()
	if !r.cachedAt.IsZero() && now.Sub(r.cachedAt) < composePortsCacheTTL {
		return append([]int(nil), r.cached...), nil
	}

	data, err := r.fileReader(r.path)
	if err != nil {
		return nil, fmt.Errorf("read compose file %q: %w", r.path, err)
	}
	ports, err := parseComposePorts(string(data), r.service)
	if err != nil {
		return nil, fmt.Errorf("parse compose file %q: %w", r.path, err)
	}
	r.cached = append([]int(nil), ports...)
	r.cachedAt = now
	return append([]int(nil), ports...), nil
}

func parseComposePorts(text, service string) ([]int, error) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	var (
		servicesIndent = -1
		serviceIndent  = -1
		portsIndent    = -1
		inService      bool
		inPorts        bool
		ports          []int
		longPublished  string
	)

	flushLong := func() {
		if port, ok := parsePort(longPublished); ok {
			ports = append(ports, port)
		}
		longPublished = ""
	}
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(stripComment(line))
		if trimmed == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))

		if servicesIndent < 0 {
			if trimmed == "services:" {
				servicesIndent = indent
			}
			continue
		}
		if indent <= servicesIndent {
			break
		}
		if !inService {
			if indent > servicesIndent && trimmed == service+":" {
				inService = true
				serviceIndent = indent
			}
			continue
		}
		if indent <= serviceIndent {
			flushLong()
			break
		}
		if !inPorts {
			if indent > serviceIndent && trimmed == "ports:" {
				inPorts = true
				portsIndent = indent
			}
			continue
		}
		if indent <= portsIndent {
			flushLong()
			break
		}
		if strings.HasPrefix(trimmed, "- ") {
			flushLong()
			entry := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			if strings.HasPrefix(entry, "{") {
				if published, ok := composeField(entry, "published"); ok {
					longPublished = published
				}
				continue
			}
			if strings.HasPrefix(entry, "target:") {
				continue
			}
			if port, ok := parseHostFromComposePortEntry(entry); ok {
				ports = append(ports, port)
			}
			continue
		}
		if strings.HasPrefix(trimmed, "published:") {
			longPublished = strings.TrimSpace(strings.TrimPrefix(trimmed, "published:"))
		}
	}
	flushLong()
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	unique := make(map[int]struct{}, len(ports))
	result := make([]int, 0, len(ports))
	for _, port := range ports {
		if _, exists := unique[port]; !exists {
			unique[port] = struct{}{}
			result = append(result, port)
		}
	}
	sort.Ints(result)
	return result, nil
}

func parseHostFromComposePortEntry(entry string) (int, bool) {
	entry = strings.Trim(strings.TrimSpace(entry), "\"'")
	entry = strings.SplitN(entry, "/", 2)[0]
	parts := strings.Split(entry, ":")
	if len(parts) == 1 {
		return parsePort(parts[0])
	}
	return parsePort(parts[len(parts)-2])
}

func composeField(entry, field string) (string, bool) {
	entry = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(entry, "{"), "}"))
	for _, part := range strings.Split(entry, ",") {
		key, value, ok := strings.Cut(part, ":")
		if ok && strings.TrimSpace(key) == field {
			return strings.Trim(strings.TrimSpace(value), "\"'"), true
		}
	}
	return "", false
}

func parsePort(raw string) (int, bool) {
	port, err := strconv.Atoi(strings.Trim(strings.TrimSpace(raw), "\"'"))
	if err != nil || port < 1 || port > 65535 {
		return 0, false
	}
	return port, true
}

func stripComment(line string) string {
	inQuote := rune(0)
	for i, r := range line {
		if (r == '\'' || r == '"') && (inQuote == 0 || inQuote == r) {
			if inQuote == 0 {
				inQuote = r
			} else {
				inQuote = 0
			}
			continue
		}
		if r == '#' && inQuote == 0 {
			return line[:i]
		}
	}
	return line
}
