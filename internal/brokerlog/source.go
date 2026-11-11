// Package brokerlog adapts Mosquitto's file log into bounded, explicitly
// classified records. Include and exclude filters use case-sensitive substring
// matching; any include match admits a record, while any exclude match rejects it.
//
// Classification is deliberately conservative:
//
//	Log shape                                           Severity  Kind
//	"New client connected from ... as <id> (..., u'<user>')":
//	                                                   info      client_connected
//	"Client <id> disconnected"                          info      client_disconnected
//	"Received PUBLISH from <id> (d<q>, q<q>, r<r>, m<m>, '<topic>', ...)":
//	                                                   debug     publish_received
//	"Opening new connection"                           info      connection_opening
//	contains "Error:" or begins "Error"               error     error
//	contains "Warning:" or begins "Warning"           warning   warning
//	all other lines                                     info      unclassified
//
// ClientID, Username and Topic are set only for matching broker log shapes
// that name them. Username is the account Mosquitto authenticated, read from
// the u'<user>' field of the connection line; it is empty for an anonymous
// client. The file source polls at most Limits.MaxBytesPerRead bytes per call.
// It notices replacement by file identity and truncation by file size. On either
// event it starts at byte zero of the current file; already-ingested entries
// remain in the ring, and unread bytes from the old file are not recoverable.
// Only complete lines are emitted; a partial final line is retained (up to a
// bounded line buffer) for the next poll. An overlong line is discarded through
// its newline so subsequent lines remain aligned.
package brokerlog

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Severity is the severity assigned to a broker log entry.
type Severity string

const (
	SeverityDebug   Severity = "debug"
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Entry is one bounded record from Mosquitto's log. ClientID, Username and
// Topic are empty unless the matched broker line itself names that value.
type Entry struct {
	ObservedAt time.Time `json:"observed_at"`
	Severity   Severity  `json:"severity"`
	Kind       string    `json:"kind"`
	Message    string    `json:"message"`
	ClientID   string    `json:"client_id,omitempty"`
	Username   string    `json:"username,omitempty"`
	Topic      string    `json:"topic,omitempty"`
}

// Filter limits records returned from the retained ring. MinimumSeverity may
// be empty (equivalent to info). Include/Exclude are substring patterns.
type Filter struct {
	MinimumSeverity Severity
	Include         []string
	Exclude         []string
}

// Limits bounds every retained/read allocation and the per-poll file work.
type Limits struct {
	MaxEntries        int
	MaxEntriesPerRead int
	MaxMessageBytes   int
	MaxAge            time.Duration
	MaxBytesPerRead   int
}

// DefaultLimits is suitable for the issue #306 diagnostics contract.
func DefaultLimits() Limits {
	return Limits{MaxEntries: 200, MaxEntriesPerRead: 50, MaxMessageBytes: 1024, MaxAge: 15 * time.Minute, MaxBytesPerRead: 64 * 1024}
}

const (
	maxAllowedEntries      = 10000
	maxAllowedMessageBytes = 64 * 1024
	maxAllowedPollBytes    = 1024 * 1024
	maxAllowedAge          = 365 * 24 * time.Hour
	maxFilterPatterns      = 32
	maxFilterPatternBytes  = 256
	maxLineOverhead        = 4096
)

// ReadResult distinguishes an unavailable file from an empty available source.
type ReadResult struct {
	Available bool    `json:"available"`
	Entries   []Entry `json:"entries"`
}

// Source is injectable so consumers and tests do not depend on a real broker.
type Source interface {
	Read(context.Context, Filter) (ReadResult, error)
}

// FileSource tails one Mosquitto log file using bounded reads and storage.
type FileSource struct {
	mu       sync.Mutex
	path     string
	limits   Limits
	entries  []Entry
	offset   int64
	identity os.FileInfo
	partial  []byte
	discard  bool
}

// NewFileSource creates a file-backed source. Invalid or unbounded limits are
// rejected before any operator-controlled file is opened.
func NewFileSource(path string, limits Limits) (*FileSource, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("broker log path is empty")
	}
	if limits.MaxEntries < 1 || limits.MaxEntries > maxAllowedEntries {
		return nil, fmt.Errorf("max entries must be between 1 and %d", maxAllowedEntries)
	}
	if limits.MaxEntriesPerRead < 1 || limits.MaxEntriesPerRead > limits.MaxEntries {
		return nil, errors.New("max entries per read must be between 1 and max entries")
	}
	if limits.MaxMessageBytes < 1 || limits.MaxMessageBytes > maxAllowedMessageBytes {
		return nil, fmt.Errorf("max message bytes must be between 1 and %d", maxAllowedMessageBytes)
	}
	if limits.MaxAge <= 0 || limits.MaxAge > maxAllowedAge {
		return nil, fmt.Errorf("max age must be between zero and %s", maxAllowedAge)
	}
	if limits.MaxBytesPerRead < 1 || limits.MaxBytesPerRead > maxAllowedPollBytes {
		return nil, fmt.Errorf("max bytes per read must be between 1 and %d", maxAllowedPollBytes)
	}
	return &FileSource{path: path, limits: limits, entries: make([]Entry, 0, limits.MaxEntries), partial: make([]byte, 0, min(limits.MaxMessageBytes+maxLineOverhead, limits.MaxBytesPerRead))}, nil
}

// Read polls at most MaxBytesPerRead new bytes and returns at most
// MaxEntriesPerRead matching retained entries. A missing log reports
// Available=false without an error.
func (s *FileSource) Read(ctx context.Context, filter Filter) (ReadResult, error) {
	if err := validateFilter(filter); err != nil {
		return ReadResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ReadResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// The path comes from the operator's configuration, so it may point at
	// anything the container can see. A FIFO or a device would block the
	// open (and with it the poll loop and the shutdown) until a writer
	// appears, so only a regular file is ingested.
	pathInfo, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return ReadResult{Available: false, Entries: []Entry{}}, nil
	}
	if err != nil {
		return ReadResult{}, fmt.Errorf("stat broker log %q: %w", s.path, err)
	}
	if !pathInfo.Mode().IsRegular() {
		return ReadResult{}, fmt.Errorf("broker log %q is not a regular file", s.path)
	}
	f, err := os.Open(s.path)
	if err != nil {
		return ReadResult{}, fmt.Errorf("open broker log %q: %w", s.path, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return ReadResult{}, fmt.Errorf("stat broker log %q: %w", s.path, err)
	}
	if !info.Mode().IsRegular() {
		return ReadResult{}, fmt.Errorf("broker log %q is not a regular file", s.path)
	}
	if s.identity == nil || !os.SameFile(s.identity, info) || info.Size() < s.offset {
		s.offset, s.partial, s.discard = 0, s.partial[:0], false
		s.identity = info
	}
	if _, err := f.Seek(s.offset, io.SeekStart); err != nil {
		return ReadResult{}, fmt.Errorf("seek broker log %q: %w", s.path, err)
	}
	reader := bufio.NewReaderSize(io.LimitReader(f, int64(s.limits.MaxBytesPerRead)), min(s.limits.MaxBytesPerRead, 32*1024))
	read := 0
	for read < s.limits.MaxBytesPerRead {
		if err := ctx.Err(); err != nil {
			return ReadResult{}, err
		}
		b, err := reader.ReadByte()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return ReadResult{}, fmt.Errorf("read broker log %q: %w", s.path, err)
			}
			break
		}
		read++
		s.offset++
		if s.discard {
			if b == '\n' {
				s.discard = false
			}
			continue
		}
		if b == '\n' {
			line := strings.TrimSuffix(string(s.partial), "\r")
			s.partial = s.partial[:0]
			if entry, ok := parseLine(line, s.limits.MaxMessageBytes); ok {
				s.append(entry)
			}
			continue
		}
		if len(s.partial) >= s.limits.MaxMessageBytes+maxLineOverhead {
			s.partial = s.partial[:0]
			s.discard = true
			continue
		}
		s.partial = append(s.partial, b)
	}
	s.expire(time.Now())
	return ReadResult{Available: true, Entries: s.filtered(filter)}, nil
}

func (s *FileSource) append(entry Entry) {
	if len(s.entries) == s.limits.MaxEntries {
		copy(s.entries, s.entries[1:])
		s.entries = s.entries[:len(s.entries)-1]
	}
	s.entries = append(s.entries, entry)
}

func (s *FileSource) expire(now time.Time) {
	keep := s.entries[:0]
	for _, entry := range s.entries {
		if now.Sub(entry.ObservedAt) <= s.limits.MaxAge {
			keep = append(keep, entry)
		}
	}
	for i := len(keep); i < len(s.entries); i++ {
		s.entries[i] = Entry{}
	}
	s.entries = keep
}

func (s *FileSource) filtered(filter Filter) []Entry {
	minimum := filter.MinimumSeverity
	if minimum == "" {
		minimum = SeverityInfo
	}
	if severityRank(minimum) < 0 {
		return []Entry{}
	}
	out := make([]Entry, 0, min(s.limits.MaxEntriesPerRead, len(s.entries)))
	for i := len(s.entries) - 1; i >= 0 && len(out) < s.limits.MaxEntriesPerRead; i-- {
		e := s.entries[i]
		if severityRank(e.Severity) < severityRank(minimum) || !matches(e, filter) {
			continue
		}
		out = append(out, e)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func matches(entry Entry, filter Filter) bool {
	text := entry.Kind + " " + entry.Message + " " + entry.ClientID + " " + entry.Topic
	if len(filter.Include) > 0 {
		found := false
		for _, pattern := range filter.Include {
			if strings.Contains(text, pattern) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, pattern := range filter.Exclude {
		if strings.Contains(text, pattern) {
			return false
		}
	}
	return true
}

func validateFilter(filter Filter) error {
	if filter.MinimumSeverity != "" && severityRank(filter.MinimumSeverity) < 0 {
		return fmt.Errorf("unsupported minimum severity %q", filter.MinimumSeverity)
	}
	if len(filter.Include) > maxFilterPatterns || len(filter.Exclude) > maxFilterPatterns {
		return fmt.Errorf("filter supports at most %d include and exclude patterns", maxFilterPatterns)
	}
	for _, patterns := range [][]string{filter.Include, filter.Exclude} {
		for _, pattern := range patterns {
			if len(pattern) > maxFilterPatternBytes {
				return fmt.Errorf("filter patterns must be at most %d bytes", maxFilterPatternBytes)
			}
		}
	}
	return nil
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityDebug:
		return 0
	case SeverityInfo:
		return 1
	case SeverityWarning:
		return 2
	case SeverityError:
		return 3
	default:
		return -1
	}
}

var (
	connectedRE  = regexp.MustCompile(`New client connected from .+ as ([^\s]+)`)
	usernameRE   = regexp.MustCompile(`\(p[0-9]+, c[0-9]+, k[0-9]+, u'([^']*)'\)`)
	disconnectRE = regexp.MustCompile(`^Client ([^\s]+) disconnected`)
	publishRE    = regexp.MustCompile(`Received PUBLISH from ([^\s]+) \(d[0-9]+, q[0-9]+, r[0-9]+, m[0-9]+, '([^']*)'`)
)

func parseLine(raw string, maxMessage int) (Entry, bool) {
	if raw == "" {
		return Entry{}, false
	}
	observed := time.Now().UTC()
	message := raw
	if colon := strings.Index(raw, ": "); colon > 0 {
		prefix := raw[:colon]
		if seconds, err := time.Parse(time.RFC3339, prefix); err == nil {
			observed, message = seconds.UTC(), raw[colon+2:]
		} else if unix, err := time.Parse("2006-01-02T15:04:05", prefix); err == nil {
			observed, message = unix.UTC(), raw[colon+2:]
		} else if epoch, err := strconv.ParseFloat(prefix, 64); err == nil && epoch >= 0 {
			seconds := int64(epoch)
			nanos := int64((epoch - float64(seconds)) * float64(time.Second))
			observed, message = time.Unix(seconds, nanos).UTC(), raw[colon+2:]
		}
	}
	entry := Entry{ObservedAt: observed, Severity: SeverityInfo, Kind: "unclassified", Message: message}
	switch {
	case strings.Contains(message, "Error:") || strings.HasPrefix(message, "Error"):
		entry.Severity, entry.Kind = SeverityError, "error"
	case strings.Contains(message, "Warning:") || strings.HasPrefix(message, "Warning"):
		entry.Severity, entry.Kind = SeverityWarning, "warning"
	case connectedRE.MatchString(message):
		entry.Kind = "client_connected"
		entry.ClientID = connectedRE.FindStringSubmatch(message)[1]
		if match := usernameRE.FindStringSubmatch(message); match != nil {
			entry.Username = match[1]
		}
	case disconnectRE.MatchString(message):
		entry.Kind = "client_disconnected"
		entry.ClientID = disconnectRE.FindStringSubmatch(message)[1]
	case publishRE.MatchString(message):
		match := publishRE.FindStringSubmatch(message)
		entry.Severity, entry.Kind = SeverityDebug, "publish_received"
		entry.ClientID, entry.Topic = match[1], match[2]
	case strings.Contains(message, "Opening new connection"):
		entry.Kind = "connection_opening"
	}
	if len(entry.Message) > maxMessage {
		entry.Message = entry.Message[:maxMessage]
	}
	return entry, true
}
