package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/mosquitto/bridges"
	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// ErrBridgesInvalid is the sentinel of a bridge document the validator
// rejected.
var ErrBridgesInvalid = errors.New("bridge configuration is invalid")

// BridgesError carries the findings of a bridge document that failed
// validation. The HTTP layer renders the issues so the operator sees
// every problem at once instead of one per round trip.
type BridgesError struct {
	Issues []bridges.Issue
}

func (e *BridgesError) Error() string {
	if len(e.Issues) == 0 {
		return ErrBridgesInvalid.Error()
	}
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		if issue.Bridge != "" {
			parts = append(parts, issue.Bridge+": "+issue.Message)
			continue
		}
		parts = append(parts, issue.Message)
	}
	return ErrBridgesInvalid.Error() + ": " + strings.Join(parts, "; ")
}

// Unwrap lets errors.Is(err, ErrBridgesInvalid) match.
func (e *BridgesError) Unwrap() error { return ErrBridgesInvalid }

// BridgesResult is the payload of the bridges read endpoint. The bridges
// never carry a secret: the passwords are replaced by the flags that say
// whether they are set.
type BridgesResult struct {
	Bridges []bridges.Bridge `json:"bridges"`
}

// GetBridges reads the bridge connections declared in the managed
// mosquitto.conf.
//
// Only the managed file is read: a bridge that lives in an included file
// belongs to that file and is outside the surface MCM owns.
func (s *Service) GetBridges(ctx context.Context) (BridgesResult, error) {
	if s.confPath == "" || s.confCatalog == nil {
		return BridgesResult{}, ErrBrokerConfigDisabled
	}
	body, err := os.ReadFile(s.confPath)
	if err != nil {
		return BridgesResult{}, fmt.Errorf("read broker config: %w", err)
	}
	parsed, err := conf.ParseString(string(body), s.confPath)
	if err != nil {
		return BridgesResult{}, fmt.Errorf("parse broker config: %w", err)
	}
	doc := bridges.Parse(parsed)
	return BridgesResult{Bridges: doc.Redacted().Bridges}, nil
}

// PreviewBridges validates a bridge document, renders it into the
// managed mosquitto.conf and stores the result as an immutable revision.
//
// The document is the desired state of the whole bridge set: the operator
// sends the bridges they want, and MCM replaces the connection sections of
// the file with them. Everything else in the file is preserved.
//
// Secrets the client did not resend keep the value they have on disk, so
// an edit that does not touch a password does not clear it.
func (s *Service) PreviewBridges(ctx context.Context, actor string, doc bridges.Document) (PreviewResult, error) {
	if s.confPath == "" || s.confCatalog == nil {
		return PreviewResult{}, ErrBrokerConfigDisabled
	}
	if s.brokerCfgStore == nil {
		return PreviewResult{}, fmt.Errorf("broker-config store is not configured")
	}

	var (
		preview PreviewResult
		err     error
	)
	if s.mutationCoordinator != nil {
		s.mutationCoordinator.LockMutations()
		preview, err = s.previewBridgesLocked(ctx, actor, doc)
		s.mutationCoordinator.UnlockMutations()
	} else {
		preview, err = s.previewBridgesLocked(ctx, actor, doc)
	}
	if err != nil {
		return PreviewResult{}, err
	}
	if s.auditFn != nil {
		s.auditFn(ctx, actor, "deployment.preview", "bridges", "", "success", nil)
	}
	return preview, nil
}

func (s *Service) previewBridgesLocked(ctx context.Context, actor string, doc bridges.Document) (PreviewResult, error) {
	topBytes, err := os.ReadFile(s.confPath)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("read broker config: %w", err)
	}
	parsed, err := conf.ParseString(string(topBytes), s.confPath)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("parse broker config: %w", err)
	}

	current := bridges.Parse(parsed)
	merged := mergeBridgeSecrets(doc, current)

	issues := bridges.Validate(merged, bridges.LocalAddresses(parsed))
	if bridges.HasErrors(issues) {
		return PreviewResult{}, &BridgesError{Issues: issues}
	}

	updated := bridges.Apply(parsed, merged)
	renderedBytes, err := updated.Render()
	if err != nil {
		return PreviewResult{}, fmt.Errorf("render broker config: %w", err)
	}
	rendered := string(renderedBytes)

	diffStr, err := conf.Diff(s.confPath, s.confPath, topBytes, renderedBytes)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("generate conf diff: %w", err)
	}

	// The reload classification only looks at the directives the revision
	// actually changes. A bridge is read by Mosquitto at start-up, so a
	// changed connection needs a restart while an untouched restart
	// required directive elsewhere must not block the apply.
	changed := changedConnectionDirectives(parsed, updated)
	reloadKind, requiresRestart := classifyChangedDirectives(changed, s.confCatalog)

	baseHash := contentHash(string(topBytes))
	renderedHash := contentHash(rendered)

	revisionID, err := newRevisionID()
	if err != nil {
		return PreviewResult{}, err
	}
	changedList := strings.Join(changed, ",")
	rev := &storage.BrokerConfigRevision{
		ID:                revisionID,
		Actor:             actor,
		BaseConfHash:      baseHash,
		RenderedConfHash:  renderedHash,
		BasePath:          s.confPath,
		ConfRendered:      rendered,
		ChangedDirectives: &changedList,
		CreatedAt:         time.Now().UTC(),
	}
	if err := s.brokerCfgStore.InsertBrokerConfigRevision(ctx, rev); err != nil {
		return PreviewResult{}, fmt.Errorf("store bridge preview revision: %w", err)
	}

	preview := PreviewResult{
		RevisionID:        revisionID,
		Kind:              "bridges",
		BaseConfHash:      baseHash,
		RenderedConfHash:  renderedHash,
		ConfDiff:          diffStr,
		ConfBody:          rendered,
		ReloadKind:        reloadKind,
		RequiresRestart:   requiresRestart,
		ChangedDirectives: changed,
		BridgeIssues:      issues,
		HasChanges:        baseHash != renderedHash,
	}
	// The rendered file holds the bridge passwords. The API never returns
	// them, so the diff and the body the operator reviews are redacted.
	preview.ConfDiff = redactBridgeSecrets(preview.ConfDiff)
	preview.ConfBody = redactBridgeSecrets(preview.ConfBody)
	return preview, nil
}

// ApplyBridges applies a reviewed bridge revision and signals the broker.
//
// A bridge is only read at start-up, so the apply refuses the revision
// with 409 unless the operator forces it or the deployment is configured
// with a restart command.
func (s *Service) ApplyBridges(ctx context.Context, actor, revisionID string, force, adopt bool) (storage.Deployment, error) {
	if s.confPath == "" || s.confCatalog == nil {
		return storage.Deployment{}, ErrBrokerConfigDisabled
	}
	if s.brokerCfgStore == nil {
		return storage.Deployment{}, fmt.Errorf("broker-config store is not configured")
	}
	if revisionID == "" {
		return storage.Deployment{}, ErrRevisionMissing
	}
	return s.applyBrokerConfigRevision(ctx, actor, revisionID, force, adopt, "bridges")
}

// mergeBridgeSecrets keeps the secrets the client did not resend.
//
// The API never returns a bridge password, so an edit that does not touch
// the credential arrives without one. Dropping it would silently break
// the bridge, so the value on disk is carried over.
func mergeBridgeSecrets(doc bridges.Document, current bridges.Document) bridges.Document {
	merged := doc.Clone()
	for i := range merged.Bridges {
		next := &merged.Bridges[i]
		previous, ok := current.Find(next.Name)
		if !ok {
			continue
		}
		if next.Password == "" {
			next.Password = previous.Password
		}
		if next.LocalPassword == "" {
			next.LocalPassword = previous.LocalPassword
		}
		if next.TLS.PSK == "" {
			next.TLS.PSK = previous.TLS.PSK
		}
	}
	return merged
}

// changedConnectionDirectives lists the connection scoped directives whose
// value differs between two parsed files.
func changedConnectionDirectives(before, after *conf.File) []string {
	beforeValues := connectionDirectiveValues(before)
	afterValues := connectionDirectiveValues(after)

	names := map[string]struct{}{}
	for name := range beforeValues {
		names[name] = struct{}{}
	}
	for name := range afterValues {
		names[name] = struct{}{}
	}

	var changed []string
	for name := range names {
		if !equalStringSlices(beforeValues[name], afterValues[name]) {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed
}

// connectionDirectiveValues collects the values of every connection scoped
// directive of a file, keyed by directive name.
func connectionDirectiveValues(f *conf.File) map[string][]string {
	out := map[string][]string{}
	if f == nil {
		return out
	}
	depth := 0
	for _, it := range f.Items {
		switch it.Kind {
		case conf.ItemBlockOpen:
			depth++
			continue
		case conf.ItemBlockClose:
			depth--
			continue
		case conf.ItemComment:
			continue
		}
		if depth != 0 || it.Kind != conf.ItemDirective {
			continue
		}
		if it.Key != "connection" && !bridges.IsConnectionKey(it.Key) {
			continue
		}
		out[it.Key] = append(out[it.Key], strings.Join(it.Values, "\x00"))
	}
	return out
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// redactBridgeSecrets replaces the value of every secret directive of a
// rendered configuration or diff.
//
// The bridge passwords live in the file Mosquitto reads, so the file on
// disk and the stored revision keep the real value. What the API returns
// to the operator never does: a credential must not travel through a
// response body, a diff or a log line.
func redactBridgeSecrets(body string) string {
	if body == "" {
		return body
	}
	secretKeys := map[string]struct{}{
		"remote_password": {},
		"local_password":  {},
		"bridge_psk":      {},
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		indent := line[:len(line)-len(trimmed)]
		// A unified diff prefixes a removed line with "-" and an added
		// line with "+". The marker is part of the line and must
		// survive the redaction.
		marker := ""
		if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "+") {
			marker = trimmed[:1]
			trimmed = trimmed[1:]
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		if _, ok := secretKeys[fields[0]]; !ok {
			continue
		}
		lines[i] = indent + marker + fields[0] + " " + bridgeSecretMask
	}
	return strings.Join(lines, "\n")
}

// bridgeSecretMask replaces the value of a secret directive.
const bridgeSecretMask = "********"
