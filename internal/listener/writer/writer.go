// Package writer renders listener specifications into Mosquitto's
// mosquitto.conf file while preserving every other directive verbatim.
//
// The writer is the integration point between internal/listener (which
// owns the preview/apply lifecycle) and internal/mosquitto/conf (the
// lossless AST used by the broker-config deploy flow). It exists to
// keep listener management consistent with the rest of the broker
// configuration: a Mosquitto restart that does not pick up new listeners
// would otherwise look like a successful apply to operators even
// though the broker kept the old ports.
//
// Scope is intentionally narrow: only `listener …` directives (and
// their immediate `protocol …` continuations when the file uses the
// legacy line-based form) are spliced. All other directives — passwd,
// acl, persistence, logging, user, includes — round-trip byte-for-byte
// through the conf.Render path. The snapshot-and-rollback discipline
// mirrors internal/mosquitto.Applier.Apply so a failed listener apply
// leaves the on-disk configuration in the same state as before the call.
package writer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
	"github.com/fgjcarlos/mcm/internal/mosquitto/listeners"
)

// FileMode is the permission applied to the written mosquitto.conf. It
// matches the permission the dev bootstrap script seeds into the shared
// volume (deploy/mosquitto/config/mosquitto-bootstrap.sh).
const FileMode os.FileMode = 0o644

// ErrEmptyConfigPath is returned when ConfigPath is empty; callers must
// not silently skip the splice because Mosquitto would never pick up
// the new listeners.
var ErrConfigPathEmpty = errors.New("listener writer: config path is empty")

// ErrConfigPathNotDirectory is returned when the parent directory of
// ConfigPath does not exist; AtomicWriteConf will fail anyway, but the
// dedicated error makes the failure mode obvious to operators reading
// the listener apply audit log.
var ErrConfigPathNotDirectory = errors.New("listener writer: config parent is not a directory")

// ErrSpecRender is returned when listeners.RenderAll refuses the input
// specs (e.g. duplicate ports, unknown protocols). The caller can map
// this back to the preview issues already returned by listener.Service.
var ErrSpecRender = errors.New("listener writer: render specs")

// Writer renders listener specs into a mosquitto.conf file.
//
// Implementations MUST be safe to call from a single goroutine; the
// owning ListenerService.Apply already serialises calls through its
// in-progress mutex.
type Writer interface {
	// Write replaces every listener block in ConfigPath with the
	// directives produced from specs and returns the post-write content
	// for callers that want to surface a diff or audit trail. The
	// rendered text is also returned so tests can assert against it
	// without re-reading the file.
	Write(specs []listeners.ListenerSpec) (Rendered, error)

	// ConfigPath returns the absolute path of the mosquitto.conf that
	// this writer targets. Used by ListenerService to compose the
	// snapshot rollback path after a failed restart.
	ConfigPath() string

	// Snapshot returns the on-disk file bytes (or an empty slice when
	// the file does not exist). ListenerService.Apply calls Snapshot
	// before Write so it can restore the previous content if the
	// subsequent broker restart fails.
	Snapshot() ([]byte, error)

	// Restore atomically writes the supplied snapshot back to
	// ConfigPath. It is invoked by ListenerService.Apply when a
	// restart fails after the writer has already persisted new
	// listener directives. When snapshot is empty the file is removed
	// so the broker falls back to its bootstrap configuration.
	Restore(snapshot []byte) error
}

// Rendered describes the textual output of a successful Write. It is
// returned to the caller rather than re-reading the file so unit tests
// can assert the splice deterministically.
type Rendered struct {
	// Path is the file written by Write (== Writer.ConfigPath()).
	Path string
	// Head is the file content excluding the spliced listener blocks.
	// It is the rendered form of the original mosquitto.conf minus
	// every legacy `listener …` / `protocol …` pair.
	Head string
	// Listeners is the rendered listener block from
	// listeners.RenderAll.
	Listeners string
	// Full is Head + Listeners, separated by exactly one blank line
	// when Head is non-empty. It is the bytes written to disk.
	Full string
}

// FileWriter is the production Writer implementation. It reads
// ConfigPath, parses it through internal/mosquitto/conf, splices the
// rendered listener directives, and writes the result atomically using
// mosquitto.AtomicWriteConf.
//
// The zero value is not usable; construct one through New.
type FileWriter struct {
	// configPath is the absolute path to the mosquitto.conf file this
	// writer targets. Required. Exposed via ConfigPath().
	configPath string
	// FileMode is the permission used when writing the file. Defaults
	// to FileMode (0o644) when zero.
	FileMode os.FileMode
	// Reader is the function used to read the existing config; defaults
	// to os.ReadFile. Tests inject deterministic readers.
	Reader func(string) ([]byte, error)
	// Writer is the function used to write the spliced result;
	// defaults to mosquitto.AtomicWriteConf. Tests inject a fake to
	// assert without touching the filesystem.
	Writer func(path, content string, perm os.FileMode) error
}

// New returns a FileWriter with sane defaults applied.
func New(configPath string) *FileWriter {
	return &FileWriter{
		configPath: configPath,
		FileMode:   FileMode,
		Reader:     os.ReadFile,
		Writer:     mosquitto.AtomicWriteConf,
	}
}

// ConfigPath returns the absolute path of the mosquitto.conf that this
// writer targets.
func (w *FileWriter) ConfigPath() string { return w.configPath }

// Snapshot returns the current on-disk file content. When the file is
// missing the returned slice is empty and err is nil; the bootstrap
// script seeds mosquitto.conf on first boot so the empty case is the
// "fresh volume" path.
func (w *FileWriter) Snapshot() ([]byte, error) {
	if strings.TrimSpace(w.configPath) == "" {
		return nil, ErrConfigPathEmpty
	}
	reader := w.Reader
	if reader == nil {
		reader = os.ReadFile
	}
	body, err := reader(w.configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []byte{}, nil
		}
		return nil, fmt.Errorf("snapshot config %q: %w", w.configPath, err)
	}
	return body, nil
}

// Restore writes the supplied snapshot back to ConfigPath atomically.
// An empty snapshot removes the file so the broker falls back to its
// bootstrap configuration. Used by ListenerService.Apply to roll back
// listener writes after a failed restart.
func (w *FileWriter) Restore(snapshot []byte) error {
	if strings.TrimSpace(w.configPath) == "" {
		return ErrConfigPathEmpty
	}
	if len(snapshot) == 0 {
		if err := os.Remove(w.configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove config %q: %w", w.configPath, err)
		}
		return nil
	}
	perm := w.FileMode
	if perm == 0 {
		perm = FileMode
	}
	writer := w.Writer
	if writer == nil {
		writer = mosquitto.AtomicWriteConf
	}
	if err := writer(w.configPath, string(snapshot), perm); err != nil {
		return fmt.Errorf("restore config %q: %w", w.configPath, err)
	}
	return nil
}

// Write reads the configured mosquitto.conf, splices the rendered
// listener directives into the result, and writes the new file
// atomically. It does NOT signal a reload — that responsibility stays
// with the listener service so it can choose between SIGHUP,
// ReloadCommand, and a full container restart based on the active
// deployment mode.
func (w *FileWriter) Write(specs []listeners.ListenerSpec) (Rendered, error) {
	if strings.TrimSpace(w.configPath) == "" {
		return Rendered{}, ErrConfigPathEmpty
	}
	if dir := filepath.Dir(w.configPath); dir != "" {
		info, err := os.Stat(dir)
		if err != nil {
			return Rendered{}, fmt.Errorf("stat config dir %q: %w", dir, err)
		}
		if !info.IsDir() {
			return Rendered{}, fmt.Errorf("%w: %q", ErrConfigPathNotDirectory, dir)
		}
	}
	perm := w.FileMode
	if perm == 0 {
		perm = FileMode
	}
	reader := w.Reader
	if reader == nil {
		reader = os.ReadFile
	}
	writer := w.Writer
	if writer == nil {
		writer = mosquitto.AtomicWriteConf
	}

	rendered, err := listeners.RenderAll(specs)
	if err != nil {
		return Rendered{}, fmt.Errorf("%w: %v", ErrSpecRender, err)
	}

	head, err := w.renderHead(reader)
	if err != nil {
		return Rendered{}, err
	}

	full := composeBody(head, rendered)
	if err := writer(w.configPath, full, perm); err != nil {
		return Rendered{}, fmt.Errorf("write spliced config: %w", err)
	}

	return Rendered{
		Path:      w.configPath,
		Head:      head,
		Listeners: rendered,
		Full:      full,
	}, nil
}

// renderHead returns the mosquitto.conf content minus every legacy
// `listener …` directive (and the `protocol …` continuation that
// immediately follows it, when present). It round-trips every other
// directive byte-for-byte through the conf.File AST.
func (w *FileWriter) renderHead(reader func(string) ([]byte, error)) (string, error) {
	body, err := reader(w.configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// No pre-existing file: there is nothing to preserve.
			// Listener specs become the entire file content.
			return "", nil
		}
		return "", fmt.Errorf("read config %q: %w", w.configPath, err)
	}
	if len(body) == 0 {
		return "", nil
	}
	parsed, err := conf.ParseString(string(body), w.configPath)
	if err != nil {
		return "", fmt.Errorf("parse config %q: %w", w.configPath, err)
	}
	filtered := filterListenerItems(parsed.Items)
	rebuilt := *parsed
	rebuilt.Items = filtered
	out, err := rebuilt.Render()
	if err != nil {
		return "", fmt.Errorf("render spliced config: %w", err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// filterListenerItems returns the subset of items that does NOT contain
// listener directives. The line-based mosquitto.conf shape used by the
// dev stack represents a listener as one `listener <port> <bind>`
// directive optionally followed by one `protocol <name>` directive on
// the next line; the braced form (`listener … {…}`) is recognised as a
// single block and removed wholesale. Both shapes are removed together
// so the result contains exactly one source of truth — the rendered
// listener block.
//
// Comment lines and blank lines are preserved between preserved
// directives; they only count as "separator" when computing whether a
// `protocol` continuation belongs to a preceding listener.
func filterListenerItems(items []conf.Item) []conf.Item {
	out := make([]conf.Item, 0, len(items))
	skipBlock := false
	for i := range items {
		it := items[i]
		switch it.Kind {
		case conf.ItemBlockOpen:
			if it.Key == "listener" {
				skipBlock = true
				continue
			}
			skipBlock = false
			out = append(out, it)
		case conf.ItemBlockClose:
			if skipBlock {
				skipBlock = false
				continue
			}
			out = append(out, it)
		case conf.ItemDirective:
			if skipBlock {
				continue
			}
			if it.Key == "listener" {
				continue
			}
			if it.Key == "protocol" && precedesListener(items, i) {
				continue
			}
			out = append(out, it)
		default:
			out = append(out, it)
		}
	}
	return out
}

// precedesListener reports whether the closest preceding non-blank,
// non-comment directive in items[0:i] is a `listener` directive. Used
// to decide whether a `protocol` line is the continuation of a legacy
// `listener …` block (and must therefore be dropped along with it).
func precedesListener(items []conf.Item, idx int) bool {
	for j := idx - 1; j >= 0; j-- {
		switch items[j].Kind {
		case conf.ItemBlockOpen, conf.ItemBlockClose, conf.ItemDirective:
			return items[j].Key == "listener"
		}
	}
	return false
}

// composeBody joins the preserved head with the rendered listener
// block using a single blank-line separator. An empty head means the
// listener block is the whole file.
func composeBody(head, listenersBlock string) string {
	head = strings.TrimRight(head, "\n")
	listenersBlock = strings.TrimRight(listenersBlock, "\n")
	if head == "" {
		return listenersBlock + "\n"
	}
	if listenersBlock == "" {
		return head + "\n"
	}
	return head + "\n\n" + listenersBlock + "\n"
}

// Compile-time guard.
var _ Writer = (*FileWriter)(nil)
