package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
	"github.com/fgjcarlos/mcm/internal/mosquitto/plugins"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// ErrPluginsInvalid is the sentinel of a plugin document the catalog
// rejected.
var ErrPluginsInvalid = errors.New("plugin configuration is invalid")

// PluginsError carries the findings of a plugin document that failed
// validation.
type PluginsError struct {
	Issues []plugins.Issue
}

func (e *PluginsError) Error() string {
	if len(e.Issues) == 0 {
		return ErrPluginsInvalid.Error()
	}
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		if issue.Plugin != "" {
			parts = append(parts, issue.Plugin+": "+issue.Message)
			continue
		}
		parts = append(parts, issue.Message)
	}
	return ErrPluginsInvalid.Error() + ": " + strings.Join(parts, "; ")
}

// Unwrap lets errors.Is(err, ErrPluginsInvalid) match.
func (e *PluginsError) Unwrap() error { return ErrPluginsInvalid }

// PluginsResult is the payload of the plugins read endpoint.
//
// It carries the catalog of the broker version, so the operator sees
// which plugins MCM is able to configure, and the document that describes
// the plugins the broker actually loads.
type PluginsResult struct {
	// Path is the managed configuration file.
	Path string `json:"path"`
	// Version is the broker version the catalog describes.
	Version string `json:"version"`
	// Catalog lists the plugins MCM supports for this version.
	Catalog []plugins.PluginSpec `json:"catalog"`
	// Document describes the plugins the broker loads.
	Document plugins.Document `json:"document"`
	// Backend tells which backend owns the access control of the broker.
	Backend AccessBackend `json:"backend"`
	// Warnings carry the findings that do not stop the read.
	Warnings []string `json:"warnings,omitempty"`
}

// GetPlugins reads the plugin configuration of the managed
// mosquitto.conf.
//
// Only the managed file is read: a plugin that lives in an included file
// belongs to that file and is outside the surface MCM owns.
func (s *Service) GetPlugins(ctx context.Context) (PluginsResult, error) {
	if s.confPath == "" || s.confCatalog == nil {
		return PluginsResult{}, ErrBrokerConfigDisabled
	}
	if s.pluginCatalog == nil {
		return PluginsResult{}, ErrBrokerConfigDisabled
	}
	body, err := os.ReadFile(s.confPath)
	if err != nil {
		return PluginsResult{}, fmt.Errorf("read broker config: %w", err)
	}
	parsed, err := conf.ParseString(string(body), s.confPath)
	if err != nil {
		return PluginsResult{}, fmt.Errorf("parse broker config: %w", err)
	}
	doc := plugins.Extract(parsed, s.pluginCatalog)
	result := PluginsResult{
		Path:     s.confPath,
		Version:  s.pluginCatalog.VersionName(),
		Catalog:  s.pluginCatalog.Specs(),
		Document: doc,
		Backend:  DetectAccessBackend(parsed, s.pluginCatalog),
	}
	if issues := plugins.Validate(doc, s.pluginCatalog); plugins.HasErrors(issues) {
		for _, issue := range issues {
			result.Warnings = append(result.Warnings, issue.Message)
		}
	}
	return result, nil
}

// PreviewPlugins validates a plugin document, renders it into the
// managed mosquitto.conf and stores the result as an immutable revision.
//
// The document is the desired state of the plugin section of the file. A
// plugin or an option the catalog does not know is preserved, so an
// operator who runs a plugin MCM does not manage keeps it working.
func (s *Service) PreviewPlugins(ctx context.Context, actor string, doc plugins.Document) (PreviewResult, error) {
	if s.confPath == "" || s.confCatalog == nil {
		return PreviewResult{}, ErrBrokerConfigDisabled
	}
	if s.pluginCatalog == nil {
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
		preview, err = s.previewPluginsLocked(ctx, actor, doc)
		s.mutationCoordinator.UnlockMutations()
	} else {
		preview, err = s.previewPluginsLocked(ctx, actor, doc)
	}
	if err != nil {
		return PreviewResult{}, err
	}
	if s.auditFn != nil {
		s.auditFn(ctx, actor, "deployment.preview", "plugins", "", "success", nil)
	}
	return preview, nil
}

func (s *Service) previewPluginsLocked(ctx context.Context, actor string, doc plugins.Document) (PreviewResult, error) {
	topBytes, err := os.ReadFile(s.confPath)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("read broker config: %w", err)
	}
	parsed, err := conf.ParseString(string(topBytes), s.confPath)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("parse broker config: %w", err)
	}

	updated, changed, err := plugins.Apply(parsed, s.pluginCatalog, doc)
	if err != nil {
		var invalid *plugins.ValidationError
		if errors.As(err, &invalid) {
			return PreviewResult{}, &PluginsError{Issues: invalid.Issues}
		}
		return PreviewResult{}, err
	}
	renderedBytes, err := updated.Render()
	if err != nil {
		return PreviewResult{}, fmt.Errorf("render broker config: %w", err)
	}
	rendered := string(renderedBytes)

	diffStr, err := conf.Diff(s.confPath, s.confPath, topBytes, renderedBytes)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("generate conf diff: %w", err)
	}

	// A plugin is loaded at start-up, so the change needs a restart. The
	// classification only looks at the directives the revision actually
	// changes, so an untouched restart required directive elsewhere does
	// not block the apply.
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
		return PreviewResult{}, fmt.Errorf("store plugin preview revision: %w", err)
	}

	return PreviewResult{
		RevisionID:        revisionID,
		Kind:              "plugins",
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

// ApplyPlugins applies a reviewed plugin revision and signals the broker.
//
// A plugin is only read at start-up, so the apply refuses the revision
// unless the operator forces it or the deployment is configured with a
// restart command.
func (s *Service) ApplyPlugins(ctx context.Context, actor, revisionID string, force, adopt bool) (storage.Deployment, error) {
	if s.confPath == "" || s.confCatalog == nil {
		return storage.Deployment{}, ErrBrokerConfigDisabled
	}
	if s.brokerCfgStore == nil {
		return storage.Deployment{}, fmt.Errorf("broker-config store is not configured")
	}
	if revisionID == "" {
		return storage.Deployment{}, ErrRevisionMissing
	}
	return s.applyBrokerConfigRevision(ctx, actor, revisionID, force, adopt, "plugins")
}

// WithPluginCatalog wires the plugin catalog of the broker version.
//
// The catalog lists the plugins MCM is able to configure and the options
// of those plugins MCM writes. A plugin or an option the catalog does
// not know is preserved byte-for-byte.
func (s *Service) WithPluginCatalog(catalog *plugins.Catalog) *Service {
	s.pluginCatalog = catalog
	return s
}

// DynSecPlanStore is the storage of the immutable plans of the dynamic
// security changes.
//
// The production storage.Store implements it; the deploy service resolves
// it with a type assertion on the deployment store, exactly like the
// broker-config store.
type DynSecPlanStore interface {
	InsertDynSecPlan(ctx context.Context, plan *storage.DynSecPlan) error
	GetDynSecPlan(ctx context.Context, id string) (storage.DynSecPlan, error)
	ConsumeDynSecPlan(ctx context.Context, id string, appliedAt time.Time) error
}

// WithDynSec wires the control channel of the dynamic security plugin.
//
// The factory opens the channel to the broker MCM manages and returns the
// function that closes it again.
func (s *Service) WithDynSec(factory DynSecTransportFactory) *Service {
	s.dynsecFactory = factory
	return s
}
