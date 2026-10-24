package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// listenerSpecStore is the subset of the storage layer the HTTP layer uses
// to keep the access control of a listener in sync with the scoped rules.
type listenerSpecStore interface {
	UpdateListenerSpec(ctx context.Context, id string, mutate func(*storage.ListenerSpecRow) error) (storage.ListenerSpecRow, error)
	ListListenerSpecs(ctx context.Context) ([]storage.ListenerSpecRow, error)
}

// aclScopeSync keeps the ACL file of a listener in sync with the scoped
// rules MCM manages for it.
//
// Mosquitto only applies the ACL file of a listener when the listener
// declares it as its `acl_file`, and only once the per-listener settings
// are enabled. MCM therefore points the listener at the file it writes,
// and removes the option again when the last scoped rule of the listener
// is gone: MCM writes the broker-wide file into the listener again and
// deletes the file it owned.
type aclScopeSync struct {
	store   listenerSpecStore
	rules   acl.Store
	aclPath string
}

// Sync updates the acl_file option of every listener that owns scoped
// rules.
func (s *aclScopeSync) Sync(ctx context.Context) error {
	if s == nil || s.store == nil || s.aclPath == "" {
		return nil
	}
	rules, err := s.rules.ListRules(ctx)
	if err != nil {
		return fmt.Errorf("list acl rules: %w", err)
	}
	scoped := make(map[string]struct{})
	for _, rule := range rules {
		if rule.IsListenerScoped() {
			scoped[rule.ListenerID] = struct{}{}
		}
	}

	specs, err := s.store.ListListenerSpecs(ctx)
	if err != nil {
		return fmt.Errorf("list listener specs: %w", err)
	}
	for _, spec := range specs {
		want := ""
		if _, owns := scoped[spec.ID]; owns {
			want = mosquitto.ListenerACLPath(s.aclPath, spec.ID)
		}
		if strings.TrimSpace(spec.Options["acl_file"]) == want {
			continue
		}
		if err := s.setACLFile(ctx, spec.ID, want); err != nil {
			return err
		}
	}
	return nil
}

// setACLFile sets or removes the acl_file option of a listener.
func (s *aclScopeSync) setACLFile(ctx context.Context, listenerID string, path string) error {
	_, err := s.store.UpdateListenerSpec(ctx, listenerID, func(row *storage.ListenerSpecRow) error {
		options := make(map[string]string, len(row.Options)+1)
		for key, value := range row.Options {
			options[key] = value
		}
		if path == "" {
			delete(options, "acl_file")
		} else {
			options["acl_file"] = path
		}
		row.Options = options
		return nil
	})
	if err != nil {
		if errors.Is(err, storage.ErrListenerNotFound) {
			// The listener is gone: its scoped rules become orphans and
			// the deploy reports them.
			return nil
		}
		return fmt.Errorf("update the acl_file of the listener %q: %w", listenerID, err)
	}
	return nil
}

// scopedListenerIDs returns the listeners that own scoped rules, in a
// stable order.
func scopedListenerIDs(rules []acl.Rule) []string {
	seen := make(map[string]struct{})
	ids := make([]string, 0, len(rules))
	for _, rule := range rules {
		if !rule.IsListenerScoped() {
			continue
		}
		if _, known := seen[rule.ListenerID]; known {
			continue
		}
		seen[rule.ListenerID] = struct{}{}
		ids = append(ids, rule.ListenerID)
	}
	sort.Strings(ids)
	return ids
}
