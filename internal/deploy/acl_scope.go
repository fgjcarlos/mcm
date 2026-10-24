package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// ManagedACLFileStore is the subset of the storage layer the deploy service
// uses to keep track of the per-listener ACL files it owns.
//
// Mosquitto resolves `acl_file` per listener once
// `per_listener_settings true` is set: a listener that declares its own
// file uses only that file, and every other listener falls back to the
// broker-wide file. MCM therefore writes one file per listener that owns
// scoped rules — and must delete that file again when the last scoped rule
// disappears, otherwise the listener would keep serving access the operator
// removed. The registry is what tells MCM which files it owns.
type ManagedACLFileStore interface {
	ListManagedACLFiles(ctx context.Context) ([]storage.ManagedACLFileRow, error)
	RecordManagedACLFile(ctx context.Context, path, listenerID string) error
	DeleteManagedACLFile(ctx context.Context, path string) error
}

// ListenerScopeLister lists the listener specs MCM manages.
//
// The deploy service uses it to verify the wiring of a listener-scoped ACL
// file: Mosquitto only applies the file of a listener when the listener
// declares it as its `acl_file`, so a rule that is not wired would be
// stored, deployed and reported as active while the broker keeps serving
// the broker-wide access.
type ListenerScopeLister interface {
	ListListenerSpecs(ctx context.Context) ([]storage.ListenerSpecRow, error)
}

// aclFilePlan is the set of ACL files one apply writes and removes.
type aclFilePlan struct {
	// Global is the broker-wide ACL file.
	Global mosquitto.ACLFile
	// Listener are the per-listener ACL files the apply writes.
	Listener []mosquitto.ACLFile
	// Remove are the files the apply deletes. Their snapshot carries the
	// content the rollback restores.
	Remove []mosquitto.ACLFile
}

// Paths returns the paths of every file the plan touches.
func (p aclFilePlan) Paths() []string {
	paths := make([]string, 0, len(p.Listener)+len(p.Remove)+1)
	paths = append(paths, p.Global.Path)
	for _, file := range p.Listener {
		paths = append(paths, file.Path)
	}
	for _, file := range p.Remove {
		paths = append(paths, file.Path)
	}
	return paths
}

// planACLFiles builds the set of ACL files an apply writes and removes.
//
// Every written file carries the on-disk content before the apply, so a
// failed apply can restore the broker-wide file, the per-listener files and
// the files that are about to be deleted.
func (s *Service) planACLFiles(ctx context.Context, rendered renderedConfig, bodies map[string]string) (aclFilePlan, error) {
	var plan aclFilePlan

	for _, file := range rendered.ACLFiles {
		snapshot, err := s.readFile(file.Path)
		if err != nil {
			return aclFilePlan{}, fmt.Errorf("snapshot acl file %q: %w", file.Path, err)
		}
		body := file.Body
		if override, ok := bodies[file.Path]; ok {
			body = override
		}
		entry := mosquitto.ACLFile{
			Path:     file.Path,
			Body:     body,
			Snapshot: snapshot,
		}
		if file.Scope == acl.ScopeListener {
			plan.Listener = append(plan.Listener, entry)
			continue
		}
		plan.Global = entry
	}

	managed, err := s.managedACLFiles(ctx)
	if err != nil {
		return aclFilePlan{}, err
	}
	current := make(map[string]struct{}, len(rendered.ACLFiles))
	for _, file := range rendered.ACLFiles {
		current[file.Path] = struct{}{}
	}
	for _, row := range managed {
		if _, stillUsed := current[row.Path]; stillUsed {
			continue
		}
		snapshot, err := s.readFile(row.Path)
		if err != nil {
			return aclFilePlan{}, fmt.Errorf("snapshot acl file %q: %w", row.Path, err)
		}
		plan.Remove = append(plan.Remove, mosquitto.ACLFile{Path: row.Path, Snapshot: snapshot})
	}
	sort.Slice(plan.Remove, func(i, j int) bool { return plan.Remove[i].Path < plan.Remove[j].Path })

	return plan, nil
}

// managedACLFiles returns the per-listener ACL files MCM owns.
func (s *Service) managedACLFiles(ctx context.Context) ([]storage.ManagedACLFileRow, error) {
	if s.managedACLFileStore == nil {
		return nil, nil
	}
	rows, err := s.managedACLFileStore.ListManagedACLFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list managed acl files: %w", err)
	}
	return rows, nil
}

// recordManagedACLFiles registers the per-listener ACL files of a render
// and drops the registration of the files that are no longer used.
//
// The registration follows the file set, not the rules: a listener whose
// scoped rules disappeared keeps no registration, so the next deploy does
// not try to delete a file twice.
func (s *Service) recordManagedACLFiles(ctx context.Context, rendered renderedConfig) error {
	if s.managedACLFileStore == nil {
		return nil
	}
	current := make(map[string]string)
	for _, file := range rendered.ACLFiles {
		if file.Scope != acl.ScopeListener {
			continue
		}
		current[file.Path] = file.ListenerID
	}
	existing, err := s.managedACLFiles(ctx)
	if err != nil {
		return err
	}
	for _, row := range existing {
		if _, stillUsed := current[row.Path]; stillUsed {
			continue
		}
		if err := s.managedACLFileStore.DeleteManagedACLFile(ctx, row.Path); err != nil &&
			!isManagedACLFileMissing(err) {
			return fmt.Errorf("release managed acl file %q: %w", row.Path, err)
		}
	}
	for path, listenerID := range current {
		if err := s.managedACLFileStore.RecordManagedACLFile(ctx, path, listenerID); err != nil {
			return fmt.Errorf("record managed acl file %q: %w", path, err)
		}
	}
	return nil
}

// restoreManagedACLFiles puts the registry back into the state it had
// before an apply. A rolled-back apply restored the previous ACL files, so
// the registry must describe those files again.
func (s *Service) restoreManagedACLFiles(ctx context.Context, previous []storage.ManagedACLFileRow) error {
	if s.managedACLFileStore == nil {
		return nil
	}
	current, err := s.managedACLFiles(ctx)
	if err != nil {
		return err
	}
	keep := make(map[string]struct{}, len(previous))
	for _, row := range previous {
		keep[row.Path] = struct{}{}
	}
	for _, row := range current {
		if _, wanted := keep[row.Path]; wanted {
			continue
		}
		if err := s.managedACLFileStore.DeleteManagedACLFile(ctx, row.Path); err != nil &&
			!isManagedACLFileMissing(err) {
			return fmt.Errorf("release managed acl file %q: %w", row.Path, err)
		}
	}
	for _, row := range previous {
		if err := s.managedACLFileStore.RecordManagedACLFile(ctx, row.Path, row.ListenerID); err != nil {
			return fmt.Errorf("restore managed acl file %q: %w", row.Path, err)
		}
	}
	return nil
}

// isManagedACLFileMissing reports whether the error means the file was
// already unregistered.
func isManagedACLFileMissing(err error) bool {
	return err == storage.ErrManagedACLFileNotFound
}

// listenerACLRenderedJSON encodes the rendered per-listener ACL files as a
// JSON object keyed by listener id. The preview revision stores it so the
// apply writes exactly the content the preview showed.
func listenerACLRenderedJSON(files []ACLFileRender) (string, error) {
	if len(files) == 0 {
		return "", nil
	}
	payload := make(map[string]string, len(files))
	for _, file := range files {
		if file.Scope != acl.ScopeListener {
			continue
		}
		payload[file.ListenerID] = file.Body
	}
	if len(payload) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode listener acl files: %w", err)
	}
	return string(encoded), nil
}

// decodeListenerACLRendered decodes the per-listener ACL files stored in a
// preview revision.
func decodeListenerACLRendered(payload string) (map[string]string, error) {
	if strings.TrimSpace(payload) == "" {
		return nil, nil
	}
	decoded := make(map[string]string)
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return nil, fmt.Errorf("decode listener acl files: %w", err)
	}
	return decoded, nil
}

// listenerSnapshotJSON encodes the on-disk content of every per-listener
// ACL file an apply touches as a JSON object keyed by path. The deployment
// record keeps it so the rollback can restore the listener files and the
// files the apply deleted.
func listenerSnapshotJSON(plan aclFilePlan) (string, error) {
	payload := make(map[string]string)
	for _, file := range plan.Listener {
		payload[file.Path] = file.Snapshot
	}
	for _, file := range plan.Remove {
		payload[file.Path] = file.Snapshot
	}
	if len(payload) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode listener acl snapshots: %w", err)
	}
	return string(encoded), nil
}

// listenerRenderedJSON encodes the rendered content of every per-listener
// ACL file an apply writes as a JSON object keyed by path.
func listenerRenderedJSON(plan aclFilePlan) (string, error) {
	payload := make(map[string]string)
	for _, file := range plan.Listener {
		payload[file.Path] = file.Body
	}
	if len(payload) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode listener acl files: %w", err)
	}
	return string(encoded), nil
}

// scopeWiringIssues reports the configuration problems that would leave the
// listener-scoped rules without effect.
//
// Two things must line up for a listener-scoped rule to apply:
//
//   - the listener must declare `acl_file <path>` with the path MCM writes
//     the listener's file to, otherwise Mosquitto keeps using the
//     broker-wide file for that listener;
//   - `per_listener_settings true` must be set, otherwise Mosquitto
//     ignores a listener-local `acl_file` entirely.
//
// The issues are blocking: an apply that would leave the scoped rules
// inert would report success while the broker serves something else.
func (s *Service) scopeWiringIssues(ctx context.Context, rendered renderedConfig) ([]string, error) {
	listenerFiles := make([]ACLFileRender, 0, len(rendered.ACLFiles))
	for _, file := range rendered.ACLFiles {
		if file.Scope == acl.ScopeListener {
			listenerFiles = append(listenerFiles, file)
		}
	}
	if len(listenerFiles) == 0 {
		return nil, nil
	}

	var issues []string

	// The listener must point at the file MCM writes for it.
	specs, err := s.listenerSpecs(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]storage.ListenerSpecRow, len(specs))
	for _, spec := range specs {
		byID[spec.ID] = spec
	}
	for _, file := range listenerFiles {
		spec, known := byID[file.ListenerID]
		if !known {
			issues = append(issues, fmt.Sprintf(
				"listener %q does not exist; its scoped rules would never apply",
				file.ListenerID,
			))
			continue
		}
		declared := strings.TrimSpace(spec.Options["acl_file"])
		if declared == "" {
			issues = append(issues, fmt.Sprintf(
				"listener %q does not declare acl_file %q; its scoped rules would never apply",
				file.ListenerID, file.Path,
			))
			continue
		}
		if declared != file.Path {
			issues = append(issues, fmt.Sprintf(
				"listener %q declares acl_file %q but MCM writes %q; its scoped rules would never apply",
				file.ListenerID, declared, file.Path,
			))
		}
	}

	// Mosquitto only resolves the acl_file per listener when the global
	// directive is enabled.
	enabled, err := s.perListenerSettingsEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if !enabled {
		issues = append(issues, fmt.Sprintf(
			"%s is not enabled; Mosquitto would ignore the acl_file of every listener",
			"per_listener_settings",
		))
	}

	return issues, nil
}

// listenerSpecs returns the listener specs MCM manages.
func (s *Service) listenerSpecs(ctx context.Context) ([]storage.ListenerSpecRow, error) {
	if s.listenerLister == nil {
		return nil, nil
	}
	specs, err := s.listenerLister.ListListenerSpecs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list listener specs: %w", err)
	}
	return specs, nil
}

// perListenerSettingsEnabled reports whether the broker configuration
// enables the per-listener access control settings.
//
// A broker configuration MCM does not manage (no conf path configured) is
// reported as enabled: the check then has nothing to verify, and the
// listener's own `acl_file` option still carries the requirement.
func (s *Service) perListenerSettingsEnabled(ctx context.Context) (bool, error) {
	if s.confPath == "" {
		return true, nil
	}
	body, err := s.readFile(s.confPath)
	if err != nil {
		return false, fmt.Errorf("read broker config %q: %w", s.confPath, err)
	}
	if strings.TrimSpace(body) == "" {
		return true, nil
	}
	parsed, err := conf.ParseString(body, s.confPath)
	if err != nil {
		return false, fmt.Errorf("parse broker config %q: %w", s.confPath, err)
	}
	for _, item := range parsed.Items {
		if item.Kind != conf.ItemDirective || item.Key != "per_listener_settings" {
			continue
		}
		if len(item.Values) == 0 {
			return false, nil
		}
		switch strings.ToLower(strings.TrimSpace(item.Values[0])) {
		case "true", "yes", "on", "1":
			return true, nil
		default:
			return false, nil
		}
	}
	return false, nil
}

// PerListenerSettingsEnabled reports whether the broker configuration
// resolves the access control options per listener.
//
// The HTTP layer uses it to tell the operator that a listener-scoped ACL
// file would be ignored by Mosquitto before a deploy refuses it.
func (s *Service) PerListenerSettingsEnabled(ctx context.Context) (bool, error) {
	return s.perListenerSettingsEnabled(ctx)
}

// ACLPath returns the path of the broker-wide ACL file MCM manages.
func (s *Service) ACLPath() string {
	return s.deployCfg.ACLPath
}

// aclFilesHash hashes the whole set of ACL files an apply would write. It
// detects a drift between the preview and the apply that also covers the
// per-listener files, which the hash of the broker-wide file alone cannot
// see.
func aclFilesHash(files []ACLFileRender) string {
	var builder strings.Builder
	sorted := make([]ACLFileRender, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	for _, file := range sorted {
		builder.WriteString(file.Path)
		builder.WriteByte(0)
		builder.WriteString(file.Body)
		builder.WriteByte(0)
	}
	return contentHash(builder.String())
}
