package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/dynsec"
	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
	"github.com/fgjcarlos/mcm/internal/mosquitto/plugins"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// AccessBackend is the backend that owns the access control of the
// broker.
//
// The two backends are kept apart on purpose. The file backend lives in
// the password and ACL files Mosquitto reads at start-up, the dynamic
// security backend lives in the database of the plugin. When both are
// active the broker picks one and the other silently does nothing, so
// MCM never writes to one while the other is in charge.
type AccessBackend string

const (
	// AccessBackendNone means the broker does not restrict access at
	// all: no password file and no ACL file.
	AccessBackendNone AccessBackend = "none"
	// AccessBackendFile means the access control lives in the password
	// and ACL files.
	AccessBackendFile AccessBackend = "file"
	// AccessBackendDynamicSecurity means the access control lives in the
	// database of the dynamic security plugin.
	AccessBackendDynamicSecurity AccessBackend = "dynamic-security"
)

// DynSecTransportFactory opens the control channel of the dynamic
// security plugin.
//
// The returned function closes the channel again.
type DynSecTransportFactory func(ctx context.Context) (dynsec.Transport, func(), error)

// ErrDynSecBackendConflict is returned when an operation would mix the
// file backend and the dynamic security backend.
var ErrDynSecBackendConflict = errors.New("the access control backend of the broker is in the way")

// ErrDynSecGuardViolation is returned when a change would open the
// management channel of the plugin to clients that must not reach it.
var ErrDynSecGuardViolation = errors.New("the management channel of the dynamic security plugin would be open")

// ErrDynSecNotAdopted is returned when the operator asks for a change of
// the dynamic security database while the file backend is still in
// charge.
var ErrDynSecNotAdopted = errors.New("the dynamic security backend has not been adopted")

// DynSecGuardError carries the findings of a management channel that
// would be open to clients that must not reach it.
type DynSecGuardError struct {
	Report dynsec.GuardReport
}

func (e *DynSecGuardError) Error() string {
	if len(e.Report.Reasons) == 0 {
		return ErrDynSecGuardViolation.Error()
	}
	return ErrDynSecGuardViolation.Error() + ": " + strings.Join(e.Report.Reasons, "; ")
}

// Unwrap lets errors.Is(err, ErrDynSecGuardViolation) match.
func (e *DynSecGuardError) Unwrap() error { return ErrDynSecGuardViolation }

// DynSecResult is the payload of the dynamic security read endpoint.
type DynSecResult struct {
	// Backend tells which backend owns the access control of the broker.
	Backend AccessBackend `json:"backend"`
	// Enabled reports whether the plugin is loaded by the broker.
	Enabled bool `json:"enabled"`
	// ManagementClient is the account that administers the broker.
	ManagementClient string `json:"management_client,omitempty"`
	// Document is the state of the security database.
	Document dynsec.Document `json:"document"`
	// Guard reports who may reach the management channel.
	Guard dynsec.GuardReport `json:"guard"`
	// Warnings carry the findings that do not stop the read.
	Warnings []string `json:"warnings,omitempty"`
}

// DynSecPreviewResult is the payload of the dynamic security preview
// endpoint.
//
// The plan is the reviewable form of the change: it holds the commands
// MCM is about to run, and the apply runs exactly those.
type DynSecPreviewResult struct {
	// PlanID identifies the immutable plan.
	PlanID string `json:"plan_id"`
	// Kind is "update" or "adopt".
	Kind string `json:"kind"`
	// Plan holds the changes and the commands that apply them.
	Plan dynsec.Plan `json:"plan"`
	// Guard reports who may reach the management channel once the plan
	// is applied.
	Guard dynsec.GuardReport `json:"guard"`
	// Backend tells which backend owns the access control of the broker.
	Backend AccessBackend `json:"backend"`
	// HasChanges reports whether the plan changes anything.
	HasChanges bool `json:"has_changes"`
	// Credentials carries the passwords of the clients the migration
	// created. They are shown once and never again.
	Credentials []AdoptedCredential `json:"credentials,omitempty"`
	// ConfigRevisionID identifies the configuration revision that
	// enables the plugin. It is set for the adoption only.
	ConfigRevisionID string `json:"config_revision_id,omitempty"`
	// ConfigDiff is the difference the configuration revision brings.
	ConfigDiff string `json:"config_diff,omitempty"`
	// Warnings carry the findings that do not stop the preview.
	Warnings []string `json:"warnings,omitempty"`
}

// AdoptedCredential is the password of a client the migration created.
//
// The password is shown once, in the answer of the adoption, and never
// reaches the broker configuration or the audit trail.
type AdoptedCredential struct {
	// Username is the client the password belongs to.
	Username string `json:"username"`
	// Password is the new password of the client.
	Password string `json:"password"`
	// Reason explains why the password changed.
	Reason string `json:"reason"`
}

// DynSecApplyResult is the payload of the dynamic security apply
// endpoint.
type DynSecApplyResult struct {
	// PlanID identifies the plan that was applied.
	PlanID string `json:"plan_id"`
	// Document is the state of the security database after the change.
	Document dynsec.Document `json:"document"`
	// Guard reports who may reach the management channel.
	Guard dynsec.GuardReport `json:"guard"`
	// Deployment is the deployment record of the change.
	Deployment storage.Deployment `json:"deployment"`
}

// DetectAccessBackend reports which backend owns the access control of
// the broker.
//
// The dynamic security plugin takes over the authentication and the
// authorization of the broker as soon as it is loaded, so a broker that
// loads it is a dynamic security broker even when the file directives
// are still in the configuration file.
func DetectAccessBackend(parsed *conf.File, catalog *plugins.Catalog) AccessBackend {
	if parsed == nil {
		return AccessBackendNone
	}
	if catalog != nil {
		for _, item := range parsed.Items {
			if item.Kind != conf.ItemDirective || item.Key != "plugin" || len(item.Values) == 0 {
				continue
			}
			if _, ok := catalog.MatchModule(item.Values[0]); ok {
				return AccessBackendDynamicSecurity
			}
		}
	}
	for _, item := range parsed.Items {
		if item.Kind != conf.ItemDirective {
			continue
		}
		switch item.Key {
		case "password_file", "acl_file":
			return AccessBackendFile
		}
	}
	return AccessBackendNone
}

// GetDynSec reads the state of the security database of the plugin.
//
// The state comes from the broker itself, not from a file MCM keeps: the
// plugin is the only authority on what it enforces.
func (s *Service) GetDynSec(ctx context.Context) (DynSecResult, error) {
	if s.confPath == "" || s.pluginCatalog == nil {
		return DynSecResult{}, ErrBrokerConfigDisabled
	}
	_, parsed, err := s.readBrokerConfig()
	if err != nil {
		return DynSecResult{}, err
	}
	backend := DetectAccessBackend(parsed, s.pluginCatalog)
	result := DynSecResult{
		Backend:          backend,
		Enabled:          backend == AccessBackendDynamicSecurity,
		ManagementClient: s.mosquittoCfg.Username,
	}
	if backend != AccessBackendDynamicSecurity {
		result.Warnings = append(result.Warnings, "the dynamic security plugin is not loaded by the broker")
		return result, nil
	}
	if s.dynsecFactory == nil {
		return DynSecResult{}, fmt.Errorf("the dynamic security control channel is not configured")
	}
	transport, closeFn, err := s.dynsecFactory(ctx)
	if err != nil {
		return DynSecResult{}, fmt.Errorf("open the dynamic security control channel: %w", err)
	}
	defer closeFn()

	service := dynsec.NewService(transport)
	doc, err := service.Read(ctx)
	if err != nil {
		return DynSecResult{}, err
	}
	result.Document = doc.Redacted()
	result.Guard = dynsec.CheckControlChannel(doc, s.mosquittoCfg.Username)
	return result, nil
}

// PreviewDynSec computes the plan that turns the state of the broker
// into the requested one and stores it as an immutable plan.
//
// The plan is refused when it would open the management channel of the
// plugin to clients that must not reach it.
func (s *Service) PreviewDynSec(ctx context.Context, actor string, desired dynsec.Document) (DynSecPreviewResult, error) {
	if s.confPath == "" || s.pluginCatalog == nil {
		return DynSecPreviewResult{}, ErrBrokerConfigDisabled
	}
	if s.dynsecFactory == nil {
		return DynSecPreviewResult{}, fmt.Errorf("the dynamic security control channel is not configured")
	}
	if s.dynsecStore == nil {
		return DynSecPreviewResult{}, fmt.Errorf("the dynamic security store is not configured")
	}
	if issues := dynsec.Validate(desired); dynsec.HasErrors(issues) {
		return DynSecPreviewResult{}, &DynSecDocumentError{Issues: issues}
	}
	// The plugin stores the membership of a group in the group itself, so
	// the group is the authority. The document is normalised before it is
	// planned, so the two views of a membership cannot drift apart.
	desired.Normalize()

	parsed, err := s.readParsedBrokerConfig()
	if err != nil {
		return DynSecPreviewResult{}, err
	}
	backend := DetectAccessBackend(parsed, s.pluginCatalog)
	if backend != AccessBackendDynamicSecurity {
		return DynSecPreviewResult{}, fmt.Errorf("%w: the broker still uses the %q backend, adopt the dynamic security backend first", ErrDynSecNotAdopted, backend)
	}

	transport, closeFn, err := s.dynsecFactory(ctx)
	if err != nil {
		return DynSecPreviewResult{}, fmt.Errorf("open the dynamic security control channel: %w", err)
	}
	defer closeFn()

	service := dynsec.NewService(transport)
	current, err := service.Read(ctx)
	if err != nil {
		return DynSecPreviewResult{}, err
	}

	plan := dynsec.BuildPlan(current, desired)
	guard := dynsec.CheckControlChannel(desired, s.mosquittoCfg.Username)
	if !guard.OK {
		return DynSecPreviewResult{}, &DynSecGuardError{Report: guard}
	}

	planID, err := newRevisionID()
	if err != nil {
		return DynSecPreviewResult{}, err
	}
	// The plan is stored without the passwords: the reviewed plan is
	// compared with the plan of the apply, and a password never reaches
	// the store nor a response body.
	envelope := dynSecPlanEnvelope{
		Plan:             maskDynSecPlan(plan),
		Desired:          desired.Redacted(),
		ManagementClient: s.mosquittoCfg.Username,
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return DynSecPreviewResult{}, fmt.Errorf("encode dynamic security plan: %w", err)
	}
	desiredBody, err := json.Marshal(desired.Redacted())
	if err != nil {
		return DynSecPreviewResult{}, fmt.Errorf("encode dynamic security document: %w", err)
	}
	stored := &storage.DynSecPlan{
		ID:               planID,
		Actor:            actor,
		Kind:             "update",
		BaseHash:         dynsecStateHash(current),
		Plan:             string(body),
		Desired:          string(desiredBody),
		ManagementClient: s.mosquittoCfg.Username,
		CreatedAt:        time.Now().UTC(),
	}
	if err := s.dynsecStore.InsertDynSecPlan(ctx, stored); err != nil {
		return DynSecPreviewResult{}, fmt.Errorf("store dynamic security plan: %w", err)
	}
	if s.auditFn != nil {
		s.auditFn(ctx, actor, "dynsec.preview", "dynsec", planID, "success", auditMetadata(map[string]any{
			"changes": len(plan.Changes),
		}))
	}

	return DynSecPreviewResult{
		PlanID:     planID,
		Kind:       "update",
		Plan:       maskDynSecPlan(plan),
		Guard:      guard,
		Backend:    backend,
		HasChanges: !plan.Empty(),
	}, nil
}

// ApplyDynSec applies a reviewed plan and verifies that the broker ended
// up in the requested state.
//
// The document carries the passwords of the clients the plan creates. It
// is compared with the plan the operator reviewed, so a change that was
// not reviewed is refused.
//
// The plan of an adoption first loads the plugin: a plugin is read by
// Mosquitto at start-up, so the broker is restarted before the state is
// pushed through the control channel.
func (s *Service) ApplyDynSec(ctx context.Context, actor, planID string, desired *dynsec.Document) (DynSecApplyResult, error) {
	if s.dynsecFactory == nil {
		return DynSecApplyResult{}, fmt.Errorf("the dynamic security control channel is not configured")
	}
	if s.dynsecStore == nil {
		return DynSecApplyResult{}, fmt.Errorf("the dynamic security store is not configured")
	}
	if planID == "" {
		return DynSecApplyResult{}, ErrRevisionMissing
	}
	stored, err := s.dynsecStore.GetDynSecPlan(ctx, planID)
	if err != nil {
		return DynSecApplyResult{}, err
	}
	if !stored.AppliedAt.IsZero() {
		return DynSecApplyResult{}, storage.ErrDynSecPlanConsumed
	}
	var envelope dynSecPlanEnvelope
	if err := json.Unmarshal([]byte(stored.Plan), &envelope); err != nil {
		return DynSecApplyResult{}, fmt.Errorf("decode dynamic security plan: %w", err)
	}

	// The adoption loads the plugin, which needs a restart of the broker.
	var deployment storage.Deployment
	if envelope.ConfigRevisionID != "" {
		deployment, err = s.applyBrokerConfigRevision(ctx, actor, envelope.ConfigRevisionID, true, true, "dynsec")
		if err != nil {
			if s.auditFn != nil {
				s.auditFn(ctx, actor, "dynsec.apply", "dynsec", planID, "failure", auditMetadata(map[string]any{
					"error": err.Error(),
				}))
			}
			return DynSecApplyResult{}, err
		}
		// The plugin is loaded when the broker starts, so the control
		// channel is only reachable once the broker listens again.
		if err := s.waitForBroker(ctx); err != nil {
			if s.auditFn != nil {
				s.auditFn(ctx, actor, "dynsec.apply", "dynsec", planID, "failure", auditMetadata(map[string]any{
					"error": err.Error(),
				}))
			}
			return DynSecApplyResult{}, err
		}
	}

	transport, closeFn, err := s.dynsecFactory(ctx)
	if err != nil {
		return DynSecApplyResult{}, fmt.Errorf("open the dynamic security control channel: %w", err)
	}
	defer closeFn()

	service := dynsec.NewService(transport)
	current, err := service.Read(ctx)
	if err != nil {
		return DynSecApplyResult{}, err
	}

	// The effective desired state: the document of the request when the
	// operator sends one, the stored one otherwise.
	effective := envelope.Desired
	if desired != nil {
		if issues := dynsec.Validate(*desired); dynsec.HasErrors(issues) {
			return DynSecApplyResult{}, &DynSecDocumentError{Issues: issues}
		}
		effective = *desired
	}

	if stored.BaseHash != "" {
		if hash := dynsecStateHash(current); hash != stored.BaseHash {
			return DynSecApplyResult{}, fmt.Errorf("%w: the security database changed after the preview, review the change again", ErrDynSecBackendConflict)
		}
	}

	plan := dynsec.BuildPlan(current, effective)
	if !sameDynSecPlan(maskDynSecPlan(plan), envelope.Plan) {
		return DynSecApplyResult{}, fmt.Errorf("%w: the change does not match the reviewed plan, review the change again", ErrDynSecBackendConflict)
	}
	guard := dynsec.CheckControlChannel(effective, s.mosquittoCfg.Username)
	if !guard.OK {
		return DynSecApplyResult{}, &DynSecGuardError{Report: guard}
	}

	actual, err := service.Apply(ctx, plan, effective)
	if err != nil {
		if s.auditFn != nil {
			s.auditFn(ctx, actor, "dynsec.apply", "dynsec", planID, "failure", auditMetadata(map[string]any{
				"error": err.Error(),
			}))
		}
		return DynSecApplyResult{}, err
	}

	if err := s.dynsecStore.ConsumeDynSecPlan(ctx, planID, time.Now().UTC()); err != nil {
		return DynSecApplyResult{}, err
	}
	guard = dynsec.CheckControlChannel(actual, s.mosquittoCfg.Username)
	if !guard.OK {
		if s.auditFn != nil {
			s.auditFn(ctx, actor, "dynsec.apply", "dynsec", planID, "failure", auditMetadata(map[string]any{
				"error": "the management channel is open",
			}))
		}
		return DynSecApplyResult{}, &DynSecGuardError{Report: guard}
	}
	if s.auditFn != nil {
		s.auditFn(ctx, actor, "dynsec.apply", "dynsec", planID, "success", auditMetadata(map[string]any{
			"changes":  len(plan.Changes),
			"commands": dynsec.MarshalCommands(plan.Commands()),
		}))
	}
	return DynSecApplyResult{
		PlanID:     planID,
		Document:   actual.Redacted(),
		Guard:      guard,
		Deployment: deployment,
	}, nil
}

// maskDynSecPlan replaces the passwords of the commands of a plan.
//
// A password must not travel through a response body, a store or an audit
// trail. The masked plan still describes the change exactly, so it can be
// compared with the plan of the apply.
func maskDynSecPlan(plan dynsec.Plan) dynsec.Plan {
	out := dynsec.Plan{Changes: make([]dynsec.Change, 0, len(plan.Changes))}
	for _, change := range plan.Changes {
		masked := change
		masked.Commands = make([]dynsec.Command, 0, len(change.Commands))
		for _, command := range change.Commands {
			safe := command
			if safe.Password != "" {
				safe.Password = "********"
			}
			masked.Commands = append(masked.Commands, safe)
		}
		out.Changes = append(out.Changes, masked)
	}
	return out
}

// sameDynSecPlan compares two plans of the dynamic security database.
//
// A plan without changes and a plan with an empty list of changes are
// the same plan: neither of them changes anything.
func sameDynSecPlan(a, b dynsec.Plan) bool {
	if len(a.Changes) == 0 && len(b.Changes) == 0 {
		return true
	}
	left, err := json.Marshal(a.Changes)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b.Changes)
	if err != nil {
		return false
	}
	return string(left) == string(right)
}

// dynSecPlanEnvelope is what the plan column of a stored plan holds.
type dynSecPlanEnvelope struct {
	// Plan holds the changes and the commands that apply them.
	Plan dynsec.Plan `json:"plan"`
	// Desired is the state the plan leads to.
	Desired dynsec.Document `json:"desired"`
	// ManagementClient is the account that administers the broker.
	ManagementClient string `json:"management_client,omitempty"`
	// ConfigRevisionID identifies the configuration revision that enables
	// the plugin. It is set for the adoption only.
	ConfigRevisionID string `json:"config_revision_id,omitempty"`
	// Adopted carries the passwords of the clients the migration
	// created.
	Adopted []AdoptedCredential `json:"adopted,omitempty"`
}

// DynSecDocumentError carries the findings of a security database that
// failed validation.
type DynSecDocumentError struct {
	Issues []dynsec.Issue
}

func (e *DynSecDocumentError) Error() string {
	if len(e.Issues) == 0 {
		return "the dynamic security document is invalid"
	}
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		parts = append(parts, issue.Message)
	}
	return "the dynamic security document is invalid: " + strings.Join(parts, "; ")
}

// readParsedBrokerConfig reads and parses the managed configuration file.
func (s *Service) readParsedBrokerConfig() (*conf.File, error) {
	_, parsed, err := s.readBrokerConfig()
	return parsed, err
}

// dynsecStateHash fingerprints the state of the security database, so a
// plan can tell whether the state moved on.
func dynsecStateHash(doc dynsec.Document) string {
	clone := doc.Clone()
	for i := range clone.Clients {
		clone.Clients[i].Password = ""
	}
	clone.Sort()
	body, err := json.Marshal(clone)
	if err != nil {
		return ""
	}
	return contentHash(string(body))
}

// dynsecDatabaseFile describes the JSON database of the plugin.
//
// The format is the one the plugin reads and writes itself: a client
// carries the hash of its password, the salt and the number of
// iterations, so the database never holds a cleartext password.
type dynsecDatabaseFile struct {
	Clients []dynsecClientFile `json:"clients"`
	Groups  []dynsecGroupFile  `json:"groups"`
	Roles   []dynsecRoleFile   `json:"roles"`
	Default dynsecDefaultsFile `json:"defaultACLAccess"`
}

// dynsecClientFile is a client of the database file.
type dynsecClientFile struct {
	Username        string           `json:"username"`
	TextName        string           `json:"textname,omitempty"`
	TextDescription string           `json:"textdescription,omitempty"`
	ClientID        string           `json:"clientid,omitempty"`
	Password        string           `json:"password,omitempty"`
	Salt            string           `json:"salt,omitempty"`
	Iterations      int              `json:"iterations,omitempty"`
	Roles           []dynsecRoleRef  `json:"roles"`
	Groups          []dynsecGroupRef `json:"groups"`
	Disabled        bool             `json:"disabled,omitempty"`
}

// dynsecRoleRef is a role reference of the database file.
type dynsecRoleRef struct {
	RoleName string `json:"rolename"`
}

// dynsecGroupRef is a group reference of the database file.
type dynsecGroupRef struct {
	GroupName string `json:"groupname"`
}

// dynsecGroupFile is a group of the database file.
type dynsecGroupFile struct {
	GroupName       string          `json:"groupname"`
	TextName        string          `json:"textname,omitempty"`
	TextDescription string          `json:"textdescription,omitempty"`
	Roles           []dynsecRoleRef `json:"roles"`
	Clients         []string        `json:"clients"`
	Privileges      map[string]bool `json:"-"`
}

// dynsecRoleFile is a role of the database file.
type dynsecRoleFile struct {
	RoleName        string          `json:"rolename"`
	TextName        string          `json:"textname,omitempty"`
	TextDescription string          `json:"textdescription,omitempty"`
	ACLs            []dynsecACLFile `json:"acls"`
}

// dynsecACLFile is an access control entry of the database file.
type dynsecACLFile struct {
	ACLType string `json:"acltype"`
	Topic   string `json:"topic"`
	Allow   bool   `json:"allow"`
}

// dynsecDefaultsFile is the default access of the database file.
type dynsecDefaultsFile struct {
	PublishClientSend    bool `json:"publishClientSend"`
	PublishClientReceive bool `json:"publishClientReceive"`
	Subscribe            bool `json:"subscribe"`
	Unsubscribe          bool `json:"unsubscribe"`
}

// dynsecDatabasePerm is the permission of the database of the plugin
// while MCM writes it.
//
// The plugin expects 0600 and warns about a world readable file. MCM
// cannot always reach that: the broker may run under another user than
// MCM. The plugin rewrites the file with the permissions it wants the
// first time it writes the database itself.
const dynsecDatabasePerm = 0o644

// writeDynsecDatabase writes the JSON database of the plugin.
//
// The file must not be world readable and must be readable by the broker,
// so it is written with the permissions of 0600 and, when the shared
// directory belongs to the broker user, with the same owner.
func (s *Service) writeDynsecDatabase(path string, doc dynsec.Document) error {
	file := dynsecDatabaseFile{
		Clients: []dynsecClientFile{},
		Groups:  []dynsecGroupFile{},
		Roles:   []dynsecRoleFile{},
		Default: dynsecDefaultsFile{
			PublishClientSend:    doc.DefaultAccess.PublishClientSend,
			PublishClientReceive: doc.DefaultAccess.PublishClientReceive,
			Subscribe:            doc.DefaultAccess.Subscribe,
			Unsubscribe:          doc.DefaultAccess.Unsubscribe,
		},
	}
	for _, client := range doc.Clients {
		entry := dynsecClientFile{
			Username:        client.Username,
			TextName:        client.TextName,
			TextDescription: client.TextDescription,
			ClientID:        client.ClientID,
			Disabled:        client.Disabled,
			Roles:           []dynsecRoleRef{},
			Groups:          []dynsecGroupRef{},
		}
		for _, ref := range client.Roles {
			entry.Roles = append(entry.Roles, dynsecRoleRef{RoleName: ref.RoleName})
		}
		for _, ref := range client.Groups {
			entry.Groups = append(entry.Groups, dynsecGroupRef{GroupName: ref.GroupName})
		}
		if client.Password != "" {
			hash, salt, err := dynsec.HashPassword(client.Password)
			if err != nil {
				return err
			}
			entry.Password = hash
			entry.Salt = salt
			entry.Iterations = dynsec.PasswordIterations
		}
		file.Clients = append(file.Clients, entry)
	}
	for _, group := range doc.Groups {
		entry := dynsecGroupFile{
			GroupName:       group.GroupName,
			TextName:        group.TextName,
			TextDescription: group.TextDescription,
			Roles:           []dynsecRoleRef{},
			Clients:         []string{},
		}
		for _, ref := range group.Roles {
			entry.Roles = append(entry.Roles, dynsecRoleRef{RoleName: ref.RoleName})
		}
		for _, ref := range group.Clients {
			entry.Clients = append(entry.Clients, ref.Username)
		}
		file.Groups = append(file.Groups, entry)
	}
	for _, role := range doc.Roles {
		entry := dynsecRoleFile{
			RoleName:        role.RoleName,
			TextName:        role.TextName,
			TextDescription: role.TextDescription,
			ACLs:            []dynsecACLFile{},
		}
		for _, acl := range role.ACLs {
			entry.ACLs = append(entry.ACLs, dynsecACLFile{
				ACLType: string(acl.Type),
				Topic:   acl.Topic,
				Allow:   acl.Allow,
			})
		}
		file.Roles = append(file.Roles, entry)
	}

	body, err := json.MarshalIndent(file, "", "\t")
	if err != nil {
		return fmt.Errorf("encode dynamic security database: %w", err)
	}
	body = append(body, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create dynamic security directory: %w", err)
	}
	// The plugin wants a file that only the broker may read. The broker
	// and MCM do not necessarily share a user, so the file is written
	// with permissions that let the broker read it. The plugin takes the
	// file over with the permissions it wants as soon as it writes the
	// database itself, which happens on the first change that goes
	// through the control channel.
	if err := os.WriteFile(path, body, dynsecDatabasePerm); err != nil {
		return fmt.Errorf("write dynamic security database: %w", err)
	}
	if err := os.Chmod(path, dynsecDatabasePerm); err != nil {
		return fmt.Errorf("set dynamic security database permissions: %w", err)
	}
	return nil
}

// dynsecLocalPath returns the path MCM writes the database of the plugin
// to.
//
// The broker and MCM see the shared configuration directory under
// different names, so the file name is what both sides agree on.
func (s *Service) dynsecLocalPath(brokerPath string) string {
	if s.mosquittoCfg.ConfigDir == "" {
		return filepath.Base(brokerPath)
	}
	return filepath.Join(s.mosquittoCfg.ConfigDir, filepath.Base(brokerPath))
}

// dynsecBrokerPath returns the path the broker reads the database of the
// plugin from.
//
// The database lives in the shared configuration directory, which the
// broker sees under another name. The broker-side directory is the one
// the operator pinned, and the default of the broker image otherwise.
//
// A path the operator already configured wins: the migration has to
// write the database where the broker reads it.
func (s *Service) dynsecBrokerPath(parsed *conf.File) string {
	if parsed != nil && s.pluginCatalog != nil {
		doc := plugins.Extract(parsed, s.pluginCatalog)
		if entry, ok := doc.Find("dynamic-security"); ok {
			if configured := strings.TrimSpace(entry.Options["config_file"]); configured != "" {
				return configured
			}
		}
	}
	dir := strings.TrimSpace(s.mosquittoCfg.BrokerConfigDir)
	if dir == "" {
		dir = defaultBrokerConfigDir
	}
	return filepath.Join(dir, "dynamic-security.json")
}

// defaultBrokerConfigDir is the broker-side configuration directory used
// when the operator does not pin one.
const defaultBrokerConfigDir = "/mosquitto/config"

// migrateACLToDynsec turns the rules of the file backend into roles.
//
// A rule that applies to a user becomes a role of that user, a rule that
// applies to every user becomes a role every client carries, and a rule
// that applies to the anonymous clients becomes the group of the
// anonymous clients.
func migrateACLToDynsec(rules []acl.Rule) (roles []dynsec.Role, byUser map[string][]string, byGroup map[string][]string, patternRoles []string, anonymousRoles []string) {
	byUser = map[string][]string{}
	byGroup = map[string][]string{}

	for _, rule := range rules {
		roleName := dynsecRoleName(rule)
		entries := dynsecACLsForRule(rule)
		if len(entries) == 0 {
			continue
		}
		roles = append(roles, dynsec.Role{
			RoleName:        roleName,
			TextName:        rule.Description,
			TextDescription: rule.Description,
			ACLs:            entries,
		})
		switch rule.EffectiveSelector() {
		case acl.SelectorUser:
			byUser[rule.Principal] = append(byUser[rule.Principal], roleName)
		case acl.SelectorAnonymous:
			anonymousRoles = append(anonymousRoles, roleName)
		case acl.SelectorPattern:
			patternRoles = append(patternRoles, roleName)
		}
	}
	return roles, byUser, byGroup, patternRoles, anonymousRoles
}

// dynsecRoleName builds the name of the role a rule becomes.
func dynsecRoleName(rule acl.Rule) string {
	switch rule.EffectiveSelector() {
	case acl.SelectorAnonymous:
		return "anonymous-" + sanitizeDynsecName(rule.TopicFilter)
	case acl.SelectorPattern:
		return "pattern-" + sanitizeDynsecName(rule.TopicFilter)
	default:
		return "user-" + sanitizeDynsecName(rule.Principal) + "-" + sanitizeDynsecName(rule.TopicFilter)
	}
}

// sanitizeDynsecName turns a topic filter or a name into something the
// plugin accepts as a role name.
func sanitizeDynsecName(value string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// dynsecACLsForRule turns one rule of the file backend into the access
// entries of a role.
//
// The file backend grants a permission with one line, while the plugin
// decides every event of the protocol on its own. A read therefore
// becomes the delivery of the messages and the subscription to them.
func dynsecACLsForRule(rule acl.Rule) []dynsec.ACL {
	allow := rule.Permission != acl.PermissionDeny
	switch rule.Permission {
	case acl.PermissionWrite:
		return []dynsec.ACL{{
			Type:  dynsec.ACLPublishClientSend,
			Topic: rule.TopicFilter,
			Allow: allow,
		}}
	case acl.PermissionRead:
		return []dynsec.ACL{
			{Type: dynsec.ACLPublishClientReceive, Topic: rule.TopicFilter, Allow: allow},
			{Type: dynsec.ACLSubscribePattern, Topic: rule.TopicFilter, Allow: allow},
		}
	case acl.PermissionReadWrite:
		return []dynsec.ACL{
			{Type: dynsec.ACLPublishClientSend, Topic: rule.TopicFilter, Allow: allow},
			{Type: dynsec.ACLPublishClientReceive, Topic: rule.TopicFilter, Allow: allow},
			{Type: dynsec.ACLSubscribePattern, Topic: rule.TopicFilter, Allow: allow},
		}
	case acl.PermissionDeny:
		return []dynsec.ACL{
			{Type: dynsec.ACLPublishClientSend, Topic: rule.TopicFilter, Allow: false},
			{Type: dynsec.ACLPublishClientReceive, Topic: rule.TopicFilter, Allow: false},
			{Type: dynsec.ACLSubscribePattern, Topic: rule.TopicFilter, Allow: false},
		}
	default:
		return nil
	}
}

// sortStrings returns a sorted copy of a list of strings.
func sortStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// auditMetadata encodes the metadata of an audit event.
func auditMetadata(payload map[string]any) []byte {
	if payload == nil {
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return body
}
