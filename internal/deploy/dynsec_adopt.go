package deploy

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/dynsec"
	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
	"github.com/fgjcarlos/mcm/internal/mosquitto/plugins"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// ManagementRoleName is the role that may reach the control channel of
// the plugin.
//
// Only the management account carries it. The role is created by the
// adoption and is what keeps the channel closed to everybody else.
const ManagementRoleName = "mcm-management"

// ManagementRoleTopic is the topic the management role may reach.
const ManagementRoleTopic = dynsec.ControlTopicPrefix + "#"

// AnonymousGroupName is the group of the anonymous clients.
const AnonymousGroupName = "anonymous"

// AdoptDynSec prepares the migration from the file backend to the
// dynamic security backend.
//
// The migration is never implicit. The operator asks for it, MCM shows
// what the migration brings, and the apply only happens after the
// operator confirmed it:
//
//   - the users and the rules of the file backend become the clients and
//     the roles of the plugin;
//   - the database of the plugin is written, with the passwords hashed
//     the way the plugin hashes them;
//   - the configuration revision that loads the plugin and drops the
//     file backend is stored.
//
// The passwords of the clients the migration creates are shown once, in
// the answer of this call.
func (s *Service) AdoptDynSec(ctx context.Context, actor string) (DynSecPreviewResult, error) {
	if s.confPath == "" || s.pluginCatalog == nil {
		return DynSecPreviewResult{}, ErrBrokerConfigDisabled
	}
	if s.dynsecStore == nil {
		return DynSecPreviewResult{}, fmt.Errorf("the dynamic security store is not configured")
	}
	if s.mosquittoCfg.Username == "" {
		return DynSecPreviewResult{}, fmt.Errorf("%w: the broker account of MCM has no name, so there is no account that may administer the plugin", ErrDynSecBackendConflict)
	}
	if s.mosquittoCfg.Password == "" {
		return DynSecPreviewResult{}, fmt.Errorf("%w: the broker account of MCM has no password, so it cannot authenticate on the control channel", ErrDynSecBackendConflict)
	}

	_, parsed, err := s.readBrokerConfig()
	if err != nil {
		return DynSecPreviewResult{}, err
	}
	backend := DetectAccessBackend(parsed, s.pluginCatalog)
	if backend == AccessBackendDynamicSecurity {
		return DynSecPreviewResult{}, fmt.Errorf("%w: the broker already uses the dynamic security backend", ErrDynSecBackendConflict)
	}

	desired, credentials, warnings, err := s.buildAdoptedDocument(ctx)
	if err != nil {
		return DynSecPreviewResult{}, err
	}
	if issues := dynsec.Validate(desired); dynsec.HasErrors(issues) {
		return DynSecPreviewResult{}, &DynSecDocumentError{Issues: issues}
	}
	guard := dynsec.CheckControlChannel(desired, s.mosquittoCfg.Username)
	if !guard.OK {
		return DynSecPreviewResult{}, &DynSecGuardError{Report: guard}
	}

	// The database of the plugin is written before the plugin is loaded:
	// without it the plugin starts with an empty database and no account
	// could reach the control channel to fill it.
	brokerPath := s.dynsecBrokerPath(parsed)
	if brokerPath == "" {
		return DynSecPreviewResult{}, fmt.Errorf("the plugin catalog has no path for the database of the plugin")
	}
	localPath := s.dynsecLocalPath(brokerPath)
	if err := s.writeDynsecDatabase(localPath, desired); err != nil {
		return DynSecPreviewResult{}, err
	}
	if info, err := os.Stat(localPath); err == nil {
		warnings = append(warnings, fmt.Sprintf("the database of the plugin was written to %s with the permissions %04o", localPath, info.Mode().Perm()))
	}

	// The configuration revision loads the plugin and hands the access
	// control over to it.
	configRevisionID, diff, err := s.previewAdoptionConfig(ctx, actor, parsed, brokerPath)
	if err != nil {
		return DynSecPreviewResult{}, err
	}

	planID, err := newRevisionID()
	if err != nil {
		return DynSecPreviewResult{}, err
	}
	envelope := dynSecPlanEnvelope{
		Plan:             dynsec.Plan{},
		Desired:          desired.Redacted(),
		ManagementClient: s.mosquittoCfg.Username,
		ConfigRevisionID: configRevisionID,
		Adopted:          credentials,
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
		Kind:             "adopt",
		BaseHash:         "",
		Plan:             string(body),
		Desired:          string(desiredBody),
		ManagementClient: s.mosquittoCfg.Username,
		CreatedAt:        time.Now().UTC(),
	}
	if err := s.dynsecStore.InsertDynSecPlan(ctx, stored); err != nil {
		return DynSecPreviewResult{}, fmt.Errorf("store dynamic security adoption: %w", err)
	}
	if s.auditFn != nil {
		s.auditFn(ctx, actor, "dynsec.adopt", "dynsec", planID, "success", auditMetadata(map[string]any{
			"clients": len(desired.Clients),
			"roles":   len(desired.Roles),
		}))
	}

	return DynSecPreviewResult{
		PlanID:           planID,
		Kind:             "adopt",
		Plan:             dynsec.Plan{},
		Guard:            guard,
		Backend:          backend,
		HasChanges:       true,
		Credentials:      credentials,
		ConfigRevisionID: configRevisionID,
		ConfigDiff:       diff,
		Warnings:         warnings,
	}, nil
}

// buildAdoptedDocument builds the security database the migration leads
// to.
//
// The clients and the rules of the file backend become the clients and
// the roles of the plugin, and the account MCM uses to reach the broker
// becomes the management account.
func (s *Service) buildAdoptedDocument(ctx context.Context) (dynsec.Document, []AdoptedCredential, []string, error) {
	var (
		credentials []AdoptedCredential
		warnings    []string
	)

	var rules []acl.Rule
	if s.aclStore != nil {
		stored, err := s.aclStore.ListRules(ctx)
		if err != nil {
			return dynsec.Document{}, nil, nil, fmt.Errorf("list acl rules: %w", err)
		}
		rules = stored
	}

	roles, byUser, _, patternRoles, anonymousRoles := migrateACLToDynsec(rules)

	// The management role keeps the control channel reachable by the
	// account of MCM and by nobody else.
	managementRole := dynsec.Role{
		RoleName: ManagementRoleName,
		TextName: "Management channel of MCM",
		ACLs: []dynsec.ACL{
			{Type: dynsec.ACLPublishClientSend, Topic: ManagementRoleTopic, Allow: true},
			{Type: dynsec.ACLPublishClientReceive, Topic: ManagementRoleTopic, Allow: true},
			{Type: dynsec.ACLSubscribePattern, Topic: ManagementRoleTopic, Allow: true},
			{Type: dynsec.ACLSubscribeLiteral, Topic: dynsec.ControlTopic, Allow: true},
			{Type: dynsec.ACLSubscribeLiteral, Topic: dynsec.ResponseTopic, Allow: true},
		},
	}
	roles = append(roles, managementRole)

	// The management account is the account MCM uses to reach the broker.
	managementPassword := s.mosquittoCfg.Password
	clients := []dynsec.Client{{
		Username: s.mosquittoCfg.Username,
		Password: managementPassword,
		Roles:    []dynsec.RoleRef{{RoleName: ManagementRoleName}},
	}}

	// The users of the file backend become clients of the plugin.
	var users []storage.MQTTUser
	if s.mqttStore != nil {
		stored, err := s.mqttStore.ListMQTTUsers(ctx)
		if err != nil {
			return dynsec.Document{}, nil, nil, fmt.Errorf("list mqtt users: %w", err)
		}
		users = stored
	}
	for _, user := range users {
		if user.Username == s.mosquittoCfg.Username {
			// The management account keeps the credential it has.
			continue
		}
		password := ""
		reason := ""
		if s.passwordLookup != nil {
			if cleartext, ok := s.passwordLookup.CleartextPassword(user.Username); ok && cleartext != "" {
				password = cleartext
				reason = "the password of the client was kept"
			}
		}
		if password == "" {
			generated, err := generateDynsecPassword()
			if err != nil {
				return dynsec.Document{}, nil, nil, err
			}
			password = generated
			reason = "the password of the client was changed, the old one was only stored as a hash"
			credentials = append(credentials, AdoptedCredential{
				Username: user.Username,
				Password: password,
				Reason:   reason,
			})
		}
		client := dynsec.Client{
			Username: user.Username,
			Password: password,
			Disabled: user.Disabled,
			Roles:    []dynsec.RoleRef{},
		}
		for _, roleName := range sortStrings(byUser[user.Username]) {
			client.Roles = append(client.Roles, dynsec.RoleRef{RoleName: roleName})
		}
		for _, roleName := range patternRoles {
			client.Roles = append(client.Roles, dynsec.RoleRef{RoleName: roleName})
		}
		clients = append(clients, client)
	}

	groups := []dynsec.Group{}
	if len(anonymousRoles) > 0 {
		group := dynsec.Group{
			GroupName: AnonymousGroupName,
			TextName:  "Anonymous clients",
			Roles:     []dynsec.RoleRef{},
		}
		for _, roleName := range sortStrings(anonymousRoles) {
			group.Roles = append(group.Roles, dynsec.RoleRef{RoleName: roleName})
		}
		groups = append(groups, group)
	}

	// The rules that apply to a single listener cannot be expressed by
	// the plugin, which decides the access control of the whole broker.
	for _, rule := range rules {
		if rule.EffectiveScope() == acl.ScopeListener {
			warnings = append(warnings, fmt.Sprintf(
				"the rule for %q on %q applied to a single listener; the plugin decides the access control of the whole broker, so the rule now applies everywhere",
				rule.Principal, rule.TopicFilter,
			))
		}
	}

	doc := dynsec.Document{
		Clients: clients,
		Groups:  groups,
		Roles:   roles,
		DefaultAccess: dynsec.DefaultAccess{
			PublishClientSend:    false,
			PublishClientReceive: true,
			Subscribe:            false,
			Unsubscribe:          true,
		},
		AnonymousGroup: AnonymousGroupName,
	}
	if len(groups) == 0 {
		doc.AnonymousGroup = ""
	}
	doc.Sort()
	return doc, credentials, warnings, nil
}

// previewAdoptionConfig builds the configuration revision that loads the
// plugin and hands the access control over to it.
func (s *Service) previewAdoptionConfig(ctx context.Context, actor string, parsed *conf.File, brokerPath string) (string, string, error) {
	doc := plugins.Extract(parsed, s.pluginCatalog)
	updated := doc.Clone()

	found := false
	for i := range updated.Plugins {
		if updated.Plugins[i].ID != "dynamic-security" {
			continue
		}
		found = true
		if updated.Plugins[i].Options == nil {
			updated.Plugins[i].Options = map[string]string{}
		}
		updated.Plugins[i].Options["config_file"] = brokerPath
	}
	if !found {
		updated.Plugins = append(updated.Plugins, plugins.Plugin{
			ID:      "dynamic-security",
			Module:  s.pluginCatalog.ModuleOf("dynamic-security"),
			Options: map[string]string{"config_file": brokerPath},
		})
	}

	rendered, changed, err := plugins.Apply(parsed, s.pluginCatalog, updated)
	if err != nil {
		return "", "", err
	}
	// The plugin takes the authentication and the authorization over, so
	// the file backend is dropped from the configuration.
	rendered, changed = removeFileBackend(rendered, changed)

	renderedBytes, err := rendered.Render()
	if err != nil {
		return "", "", fmt.Errorf("render broker config: %w", err)
	}
	topBytes, err := os.ReadFile(s.confPath)
	if err != nil {
		return "", "", fmt.Errorf("read broker config: %w", err)
	}
	diff, err := conf.Diff(s.confPath, s.confPath, topBytes, renderedBytes)
	if err != nil {
		return "", "", fmt.Errorf("generate conf diff: %w", err)
	}

	revisionID, err := newRevisionID()
	if err != nil {
		return "", "", err
	}
	changedList := strings.Join(changed, ",")
	rev := &storage.BrokerConfigRevision{
		ID:                revisionID,
		Actor:             actor,
		BaseConfHash:      contentHash(string(topBytes)),
		RenderedConfHash:  contentHash(string(renderedBytes)),
		BasePath:          s.confPath,
		ConfRendered:      string(renderedBytes),
		ChangedDirectives: &changedList,
		CreatedAt:         time.Now().UTC(),
	}
	if err := s.brokerCfgStore.InsertBrokerConfigRevision(ctx, rev); err != nil {
		return "", "", fmt.Errorf("store dynamic security adoption revision: %w", err)
	}
	return revisionID, diff, nil
}

// removeFileBackend drops the directives of the file backend from a
// parsed configuration.
//
// The plugin answers the authentication and the authorization of the
// broker on its own. A password file or an ACL file that stays in the
// configuration would look active and do nothing, so the migration
// removes it.
func removeFileBackend(f *conf.File, changed []string) (*conf.File, []string) {
	if f == nil {
		return nil, changed
	}
	items := make([]conf.Item, 0, len(f.Items))
	changedSet := map[string]bool{}
	for _, name := range changed {
		changedSet[name] = true
	}
	for _, item := range f.Items {
		if item.Kind == conf.ItemDirective {
			switch item.Key {
			case "password_file", "acl_file":
				changedSet[item.Key] = true
				continue
			}
		}
		items = append(items, item)
	}
	out := &conf.File{Path: f.Path, Items: items}
	names := make([]string, 0, len(changedSet))
	for name := range changedSet {
		names = append(names, name)
	}
	sort.Strings(names)
	return out, names
}

// generateDynsecPassword returns a random password for a client the
// migration creates.
func generateDynsecPassword() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	buf := make([]byte, 24)
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	for i, b := range raw {
		buf[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(buf), nil
}
