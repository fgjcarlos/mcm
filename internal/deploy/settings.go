package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
	"github.com/fgjcarlos/mcm/internal/mosquitto/settings"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// ---------------------------------------------------------------------------
// Broker settings path — issue #302
// ---------------------------------------------------------------------------

// ErrSettingsInvalid is returned when a settings document does not pass
// the catalog checks. The HTTP layer renders the issues of the wrapped
// SettingsError so the operator sees every problem at once.
var ErrSettingsInvalid = errors.New("broker settings are invalid")

// SettingsError carries the issues of a settings document that the
// catalog rejected.
type SettingsError struct {
	Issues []settings.Issue
}

func (e *SettingsError) Error() string {
	if len(e.Issues) == 0 {
		return ErrSettingsInvalid.Error()
	}
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		parts = append(parts, issue.Message)
	}
	return fmt.Sprintf("%s: %s", ErrSettingsInvalid.Error(), strings.Join(parts, "; "))
}

// Unwrap lets callers match the error with errors.Is(err, ErrSettingsInvalid).
func (e *SettingsError) Unwrap() error { return ErrSettingsInvalid }

// BrokerSettingsResult is the GET /api/v1/broker/settings payload. The
// document holds the current state, the sections describe the form, and
// both come from the versioned catalog so the form never guesses.
type BrokerSettingsResult struct {
	Path     string             `json:"path"`
	Hash     string             `json:"hash"`
	Version  string             `json:"version"`
	Document settings.Document  `json:"document"`
	Sections []settings.Section `json:"sections"`
	Warnings []string           `json:"warnings,omitempty"`
}

// GetBrokerSettings reads the current settings of the broker together
// with the catalog description of the form.
func (s *Service) GetBrokerSettings(ctx context.Context) (BrokerSettingsResult, error) {
	if s.confPath == "" || s.confCatalog == nil {
		return BrokerSettingsResult{}, ErrBrokerConfigDisabled
	}
	body, parsed, err := s.readBrokerConfig()
	if err != nil {
		return BrokerSettingsResult{}, err
	}
	doc := settings.Extract(parsed, s.confCatalog)
	result := BrokerSettingsResult{
		Path:     s.confPath,
		Hash:     contentHash(string(body)),
		Version:  s.confCatalog.VersionName(),
		Document: doc,
		Sections: settings.BuildSections(s.confCatalog, doc),
		Warnings: s.settingsWarnings(parsed),
	}
	return result, nil
}

// readBrokerConfig reads and parses the on-disk broker configuration.
func (s *Service) readBrokerConfig() ([]byte, *conf.File, error) {
	body, err := os.ReadFile(s.confPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read broker config: %w", err)
	}
	parsed, err := conf.Parse(strings.NewReader(string(body)), s.confPath)
	if err != nil {
		return nil, nil, fmt.Errorf("parse broker config: %w", err)
	}
	return body, parsed, nil
}

// settingsWarnings reports the settings that are also set in an included
// file. Mosquitto reads the included files after the main one, so the
// value in an include can win over the one MCM writes.
func (s *Service) settingsWarnings(parsed *conf.File) []string {
	if s.confPath == "" {
		return nil
	}
	root := filepath.Dir(s.confPath)
	resolved, _, err := conf.NewIncludeResolver(root).Resolve(parsed)
	if err != nil {
		// A broken include tree is reported by the import preview;
		// the settings editor must not fail because of it.
		return nil
	}
	top := settings.Occurrences(parsed, s.confCatalog)
	full := settings.Occurrences(resolved, s.confCatalog)
	var warnings []string
	for name, count := range full {
		if count > top[name] {
			warnings = append(warnings, fmt.Sprintf(
				"%q is also set in an included file; Mosquitto reads the include after the main file, so the included value may win",
				name,
			))
		}
	}
	sort.Strings(warnings)
	return warnings
}

// PreviewBrokerSettings applies the settings the operator edited to the
// on-disk configuration and stores an immutable preview revision.
//
// The preview only touches the global part of mosquitto.conf: the
// directives of a listener block belong to that listener and are edited
// in the listener panel. The revision records which directives changed so
// the apply path can tell a SIGHUP apart from a full restart.
func (s *Service) PreviewBrokerSettings(ctx context.Context, actor string, doc settings.Document) (PreviewResult, error) {
	if s.confPath == "" || s.confCatalog == nil {
		return PreviewResult{}, ErrBrokerConfigDisabled
	}
	if s.brokerCfgStore == nil {
		return PreviewResult{}, fmt.Errorf("broker-config store is not configured")
	}
	if issues := settings.Validate(doc, s.confCatalog); len(issues) > 0 {
		return PreviewResult{}, &SettingsError{Issues: issues}
	}

	var (
		preview PreviewResult
		err     error
	)
	if s.mutationCoordinator != nil {
		s.mutationCoordinator.LockMutations()
		defer s.mutationCoordinator.UnlockMutations()
	}
	preview, err = s.previewBrokerSettingsLocked(ctx, actor, doc)
	if err != nil {
		return PreviewResult{}, err
	}
	if s.auditFn != nil {
		s.auditFn(ctx, actor, "deployment.preview", "broker_settings", "", "success", nil)
	}
	return preview, nil
}

func (s *Service) previewBrokerSettingsLocked(ctx context.Context, actor string, doc settings.Document) (PreviewResult, error) {
	body, parsed, err := s.readBrokerConfig()
	if err != nil {
		return PreviewResult{}, err
	}
	updated, changed, err := settings.Apply(parsed, doc, s.confCatalog)
	if err != nil {
		return PreviewResult{}, err
	}
	renderedBytes, err := updated.Render()
	if err != nil {
		return PreviewResult{}, fmt.Errorf("render broker config: %w", err)
	}
	rendered := string(renderedBytes)

	// Only the directives that changed decide between a SIGHUP and a
	// restart: a restart-required directive that the operator did not
	// touch must not block the apply.
	reloadKind, requiresRestart := classifyChangedDirectives(changed, s.confCatalog)

	baseHash := contentHash(string(body))
	renderedHash := contentHash(rendered)

	diffStr, err := conf.Diff(s.confPath, s.confPath, body, renderedBytes)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("generate conf diff: %w", err)
	}

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
		return PreviewResult{}, fmt.Errorf("store broker-settings preview revision: %w", err)
	}

	return PreviewResult{
		RevisionID:        revisionID,
		Kind:              "broker_settings",
		BaseConfHash:      baseHash,
		RenderedConfHash:  renderedHash,
		ConfDiff:          diffStr,
		ConfBody:          rendered,
		ReloadKind:        reloadKind,
		RequiresRestart:   requiresRestart,
		ChangedDirectives: changed,
		HasChanges:        baseHash != renderedHash,
	}, nil
}

// classifyChangedDirectives returns the worst reload_kind across the
// directives that changed and whether one of them needs a restart.
//
// It is the counterpart of classifyReload, which looks at the whole
// file. The settings editor knows exactly which directives it touched,
// so an untouched restart-required directive must not block the apply.
func classifyChangedDirectives(names []string, cat ConfCatalog) (string, bool) {
	worst := "reload"
	requiresRestart := false
	for _, name := range names {
		spec, ok := cat.Lookup(name)
		if !ok {
			continue
		}
		switch spec.ReloadKind {
		case "restart":
			worst = "restart"
			requiresRestart = true
		case "both":
			if worst != "restart" {
				worst = "both"
			}
		}
	}
	return worst, requiresRestart
}

// changedDirectiveNames splits the comma separated change set of a
// revision.
func changedDirectiveNames(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
