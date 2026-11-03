package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
	"github.com/fgjcarlos/mcm/internal/storage"
)

var ErrInvalidBrokerConfig = errors.New("broker configuration validation failed")

// BrokerConfigDraft edits only the configured root file. Includes remain
// separate files and are hashed, never concatenated into the written root.
type BrokerConfigDraft struct {
	Body     string `json:"body"`
	BaseHash string `json:"base_hash"`
}

func (s *Service) PreviewBrokerConfigDraft(ctx context.Context, actor string, draft BrokerConfigDraft) (PreviewResult, error) {
	return s.previewBrokerConfig(ctx, actor, &draft)
}

func (s *Service) previewBrokerConfigLocked(ctx context.Context, actor string, draft *BrokerConfigDraft) (PreviewResult, error) {
	current, err := os.ReadFile(s.confPath)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("read broker config: %w", err)
	}
	currentParsed, err := conf.ParseString(string(current), s.confPath)
	if err != nil {
		return PreviewResult{}, ErrInvalidBrokerConfig
	}
	if hasInlineCredentials(currentParsed) {
		return PreviewResult{}, ErrSensitiveBrokerConfig
	}
	baseHash := contentHash(string(current))
	body := string(current)
	if draft != nil {
		if draft.BaseHash != "" && draft.BaseHash != baseHash {
			return PreviewResult{}, ErrRevisionMismatch
		}
		body = draft.Body
	}
	top, err := conf.ParseString(body, s.confPath)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("%w: %v", ErrInvalidBrokerConfig, err)
	}
	if hasInlineCredentials(top) {
		return PreviewResult{}, ErrSensitiveBrokerConfig
	}
	resolved, snapshot, err := conf.NewIncludeResolver(filepath.Dir(s.confPath)).Resolve(top)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("%w: %v", ErrInvalidBrokerConfig, err)
	}
	cat, _ := s.confCatalog.(*catalog.Catalog)
	issues := conf.Validate(resolved, cat)
	result := PreviewResult{Kind: "broker_config", BaseConfHash: baseHash, RenderedConfHash: contentHash(body), ConfBody: body, ValidationIssues: issues, IncludeSnapshot: snapshot, HasChanges: body != string(current)}
	if draft != nil {
		for _, issue := range issues {
			if issue.Kind == "type" || issue.Kind == "dependency" {
				return result, ErrInvalidBrokerConfig
			}
		}
	}
	// Compare changed directives only: an unchanged listener must not turn a
	// logging edit into a restart. Unknown changed directives fail conservative.
	result.ReloadKind, result.RequiresRestart = s.brokerConfigImpact(string(current), body)
	result.ConfDiff, err = conf.Diff(s.confPath, s.confPath, current, []byte(body))
	if err != nil {
		return PreviewResult{}, err
	}
	result.RevisionID, err = newRevisionID()
	if err != nil {
		return PreviewResult{}, err
	}
	includeJSON, err := json.Marshal(snapshot)
	if err != nil {
		return PreviewResult{}, err
	}
	rev := &storage.BrokerConfigRevision{ID: result.RevisionID, Actor: actor, BaseConfHash: baseHash, RenderedConfHash: result.RenderedConfHash, BasePath: s.confPath, ConfRendered: body, IncludeSnapshot: string(includeJSON), CreatedAt: time.Now().UTC()}
	if err := s.brokerCfgStore.InsertBrokerConfigRevision(ctx, rev); err != nil {
		return PreviewResult{}, err
	}
	return result, nil
}

func (s *Service) brokerConfigImpact(before, after string) (string, bool) {
	if before == after {
		return "none", false
	}
	values := func(body string) map[string][]string {
		f, _ := conf.ParseString(body, s.confPath)
		out := map[string][]string{}
		if f != nil {
			for _, it := range f.Items {
				if it.Kind == conf.ItemDirective || it.Kind == conf.ItemBlockOpen {
					out[it.Key] = append(out[it.Key], strings.Join(it.Values, " "))
				}
			}
		}
		return out
	}
	old, next := values(before), values(after)
	keys := map[string]bool{}
	for key := range old {
		keys[key] = true
	}
	for key := range next {
		keys[key] = true
	}
	kind := "reload"
	for key := range keys {
		if reflect.DeepEqual(old[key], next[key]) {
			continue
		}
		spec, ok := s.confCatalog.Lookup(key)
		if !ok || spec.ReloadKind == "restart" || spec.ReloadKind == "both" {
			return "restart", true
		}
	}
	return kind, false
}
