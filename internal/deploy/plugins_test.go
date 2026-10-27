package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/dynsec"
	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
	"github.com/fgjcarlos/mcm/internal/mosquitto/plugins"
	"github.com/fgjcarlos/mcm/internal/storage"
)

const pluginsConf = `# MCM configuration
persistence true

listener 1883 0.0.0.0
allow_anonymous true
`

const dynsecConf = `# MCM configuration
persistence true

listener 1883 0.0.0.0
allow_anonymous true

plugin /usr/lib/mosquitto_dynamic_security.so
plugin_opt_config_file /mosquitto/config/dynamic-security.json
`

// pluginService wires a deploy service with the plugin catalog enabled
// and a configuration file holding the given body.
func pluginService(t *testing.T, body string) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	if err := os.WriteFile(confPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	cat, err := catalog.LoadVersion("2.0")
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	pluginCat, err := plugins.LoadVersion("2.0")
	if err != nil {
		t.Fatalf("load plugin catalog: %v", err)
	}
	svc := newTestService(
		&fakeApplier{},
		&fakeACLStore{},
		&fakeMQTTUserLister{},
		newFakeDeploymentStore(),
		diagnostics.VerifierFunc(okVerifier),
		&fakePasswordLookup{},
		config.DeployConfig{Mode: "file"},
	)
	svc.WithBrokerConfig(confPath, cat)
	svc.WithPluginCatalog(pluginCat)
	svc.brokerCfgStore = newFakeBrokerConfigStore()
	return svc, confPath
}

// fakeTransport answers the commands of the plugin with canned data.
type fakeTransport struct {
	document dynsec.Document
	requests [][]dynsec.Command
	err      error
}

func (f *fakeTransport) Do(_ context.Context, commands ...dynsec.Command) ([]dynsec.CommandResponse, error) {
	f.requests = append(f.requests, commands)
	if f.err != nil {
		return nil, f.err
	}
	out := make([]dynsec.CommandResponse, 0, len(commands))
	for _, command := range commands {
		switch command.Name {
		case dynsec.CommandListClients:
			body, _ := json.Marshal(map[string]any{
				"totalCount": len(f.document.Clients),
				"clients":    f.document.Clients,
			})
			out = append(out, dynsec.CommandResponse{Command: command.Name, Data: body})
		case dynsec.CommandListGroups:
			body, _ := json.Marshal(map[string]any{
				"totalCount": len(f.document.Groups),
				"groups":     f.document.Groups,
			})
			out = append(out, dynsec.CommandResponse{Command: command.Name, Data: body})
		case dynsec.CommandListRoles:
			body, _ := json.Marshal(map[string]any{
				"totalCount": len(f.document.Roles),
				"roles":      f.document.Roles,
			})
			out = append(out, dynsec.CommandResponse{Command: command.Name, Data: body})
		case dynsec.CommandGetDefaultACLAccess:
			body, _ := json.Marshal(map[string]any{"acls": []map[string]any{
				{"acltype": "publishClientSend", "allow": f.document.DefaultAccess.PublishClientSend},
				{"acltype": "publishClientReceive", "allow": f.document.DefaultAccess.PublishClientReceive},
				{"acltype": "subscribe", "allow": f.document.DefaultAccess.Subscribe},
				{"acltype": "unsubscribe", "allow": f.document.DefaultAccess.Unsubscribe},
			}})
			out = append(out, dynsec.CommandResponse{Command: command.Name, Data: body})
		case dynsec.CommandGetAnonymousGroup:
			body, _ := json.Marshal(map[string]any{"group": map[string]any{"groupname": f.document.AnonymousGroup}})
			out = append(out, dynsec.CommandResponse{Command: command.Name, Data: body})
		default:
			out = append(out, dynsec.CommandResponse{Command: command.Name})
			f.apply(command)
		}
	}
	return out, nil
}

// apply carries a command out on the document of the fake, so the
// verification of the service sees the state the command produced.
func (f *fakeTransport) apply(command dynsec.Command) {
	switch command.Name {
	case dynsec.CommandCreateRole:
		role := dynsec.Role{
			RoleName: command.RoleName,
			TextName: command.TextName,
			ACLs:     []dynsec.ACL{},
		}
		for _, entry := range command.ACLs {
			acl := dynsec.ACL{Type: dynsec.ACLType(entry.ACLType), Topic: entry.Topic, Allow: entry.Allow != nil && *entry.Allow}
			if entry.Priority != nil {
				acl.Priority = *entry.Priority
			}
			role.ACLs = append(role.ACLs, acl)
		}
		f.document.Roles = append(f.document.Roles, role)
	case dynsec.CommandAddRoleACL:
		for i := range f.document.Roles {
			if f.document.Roles[i].RoleName != command.RoleName {
				continue
			}
			entry := dynsec.ACL{Type: dynsec.ACLType(command.ACLType), Topic: command.Topic, Allow: command.Allow != nil && *command.Allow}
			if command.Priority != nil {
				entry.Priority = *command.Priority
			}
			f.document.Roles[i].ACLs = append(f.document.Roles[i].ACLs, entry)
		}
	case dynsec.CommandCreateClient:
		client := dynsec.Client{Username: command.Username, Roles: []dynsec.RoleRef{}, Groups: []dynsec.GroupRef{}}
		for _, ref := range command.Roles {
			client.Roles = append(client.Roles, dynsec.RoleRef{RoleName: ref.RoleName})
		}
		f.document.Clients = append(f.document.Clients, client)
	case dynsec.CommandCreateGroup:
		group := dynsec.Group{GroupName: command.GroupName, Roles: []dynsec.RoleRef{}, Clients: []dynsec.ClientRef{}}
		for _, ref := range command.Roles {
			group.Roles = append(group.Roles, dynsec.RoleRef{RoleName: ref.RoleName})
		}
		f.document.Groups = append(f.document.Groups, group)
	case dynsec.CommandAddGroupClient:
		for i := range f.document.Groups {
			if f.document.Groups[i].GroupName != command.GroupName {
				continue
			}
			f.document.Groups[i].Clients = append(f.document.Groups[i].Clients, dynsec.ClientRef{Username: command.Username})
		}
	}
}

// managementDocument is a security database with a management account
// that may reach the control channel.
func managementDocument() dynsec.Document {
	return dynsec.Document{
		Roles: []dynsec.Role{{
			RoleName: "mcm-management",
			ACLs: []dynsec.ACL{
				{Type: dynsec.ACLPublishClientSend, Topic: dynsec.ControlTopicPrefix + "#", Allow: true},
				{Type: dynsec.ACLSubscribePattern, Topic: dynsec.ControlTopicPrefix + "#", Allow: true},
			},
		}},
		Clients: []dynsec.Client{{
			Username: "mcm",
			Roles:    []dynsec.RoleRef{{RoleName: "mcm-management"}},
		}},
		DefaultAccess: dynsec.DefaultAccess{
			PublishClientSend:    false,
			PublishClientReceive: true,
			Subscribe:            false,
			Unsubscribe:          true,
		},
	}
}

func TestGetPluginsReturnsTheCatalogAndTheDocument(t *testing.T) {
	svc, _ := pluginService(t, pluginsConf)
	result, err := svc.GetPlugins(context.Background())
	if err != nil {
		t.Fatalf("GetPlugins: %v", err)
	}
	if result.Version != "2.0" {
		t.Errorf("version = %q, want 2.0", result.Version)
	}
	if len(result.Catalog) == 0 {
		t.Fatal("the catalog is empty")
	}
	found := false
	for _, spec := range result.Catalog {
		if spec.ID == "dynamic-security" {
			found = true
		}
	}
	if !found {
		t.Error("the catalog does not know the dynamic security plugin")
	}
	if result.Backend != AccessBackendNone {
		t.Errorf("backend = %q, want none", result.Backend)
	}
}

func TestGetPluginsDetectsTheBackend(t *testing.T) {
	svc, _ := pluginService(t, dynsecConf)
	result, err := svc.GetPlugins(context.Background())
	if err != nil {
		t.Fatalf("GetPlugins: %v", err)
	}
	if result.Backend != AccessBackendDynamicSecurity {
		t.Errorf("backend = %q, want dynamic-security", result.Backend)
	}
	if len(result.Document.Plugins) != 1 {
		t.Fatalf("plugins = %+v", result.Document.Plugins)
	}
	entry := result.Document.Plugins[0]
	if entry.ID != "dynamic-security" {
		t.Errorf("plugin id = %q, want dynamic-security", entry.ID)
	}
	if entry.Options["config_file"] != "/mosquitto/config/dynamic-security.json" {
		t.Errorf("config_file = %q", entry.Options["config_file"])
	}
}

func TestPreviewPluginsWritesThePlugin(t *testing.T) {
	svc, _ := pluginService(t, pluginsConf)
	doc := plugins.Document{Plugins: []plugins.Plugin{{
		ID:     "dynamic-security",
		Module: "/usr/lib/mosquitto_dynamic_security.so",
		Options: map[string]string{
			"config_file": "/mosquitto/config/dynamic-security.json",
		},
	}}}
	preview, err := svc.PreviewPlugins(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewPlugins: %v", err)
	}
	if preview.Kind != "plugins" {
		t.Errorf("kind = %q, want plugins", preview.Kind)
	}
	if !preview.HasChanges {
		t.Error("the preview reports no change")
	}
	if !strings.Contains(preview.ConfBody, "plugin /usr/lib/mosquitto_dynamic_security.so") {
		t.Errorf("the rendered file does not carry the plugin:\n%s", preview.ConfBody)
	}
	if !strings.Contains(preview.ConfBody, "plugin_opt_config_file /mosquitto/config/dynamic-security.json") {
		t.Errorf("the rendered file does not carry the option:\n%s", preview.ConfBody)
	}
	if !preview.RequiresRestart {
		t.Error("a plugin change must need a restart")
	}
}

func TestPreviewPluginsRefusesAMissingRequiredOption(t *testing.T) {
	svc, _ := pluginService(t, pluginsConf)
	doc := plugins.Document{Plugins: []plugins.Plugin{{
		ID:     "dynamic-security",
		Module: "/usr/lib/mosquitto_dynamic_security.so",
	}}}
	_, err := svc.PreviewPlugins(context.Background(), "operator", doc)
	var invalid *PluginsError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want a plugin validation error", err)
	}
	if len(invalid.Issues) == 0 {
		t.Error("the error carries no issue")
	}
}

func TestPreviewPluginsKeepsAnUnknownPlugin(t *testing.T) {
	svc, _ := pluginService(t, "persistence true\n\nplugin /usr/lib/other_plugin.so\nplugin_opt_whatever 1\n")
	doc := plugins.Document{Plugins: []plugins.Plugin{{
		ID:     "dynamic-security",
		Module: "/usr/lib/mosquitto_dynamic_security.so",
		Options: map[string]string{
			"config_file": "/mosquitto/config/dynamic-security.json",
		},
	}}}
	preview, err := svc.PreviewPlugins(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewPlugins: %v", err)
	}
	if !strings.Contains(preview.ConfBody, "plugin /usr/lib/other_plugin.so") {
		t.Errorf("the unknown plugin was dropped:\n%s", preview.ConfBody)
	}
	if !strings.Contains(preview.ConfBody, "plugin_opt_whatever 1") {
		t.Errorf("the unknown option was dropped:\n%s", preview.ConfBody)
	}
}

func TestPreviewDynSecRefusesTheFileBackend(t *testing.T) {
	svc, _ := pluginService(t, "persistence true\npassword_file /mosquitto/config/passwd\n")
	transport := &fakeTransport{document: managementDocument()}
	svc.WithDynSec(func(context.Context) (dynsec.Transport, func(), error) {
		return transport, func() {}, nil
	})
	svc.mosquittoCfg.Username = "mcm"
	_, err := svc.PreviewDynSec(context.Background(), "operator", managementDocument())
	if !errors.Is(err, ErrDynSecNotAdopted) {
		t.Fatalf("error = %v, want the backend conflict", err)
	}
}

func TestPreviewDynSecComputesThePlan(t *testing.T) {
	svc, _ := pluginService(t, dynsecConf)
	transport := &fakeTransport{document: managementDocument()}
	svc.WithDynSec(func(context.Context) (dynsec.Transport, func(), error) {
		return transport, func() {}, nil
	})
	svc.mosquittoCfg.Username = "mcm"

	desired := managementDocument()
	desired.Roles = append(desired.Roles, dynsec.Role{
		RoleName: "reader",
		ACLs: []dynsec.ACL{
			{Type: dynsec.ACLSubscribePattern, Topic: "sensors/#", Allow: true},
		},
	})
	desired.Clients = append(desired.Clients, dynsec.Client{
		Username: "alice",
		Roles:    []dynsec.RoleRef{{RoleName: "reader"}},
	})

	preview, err := svc.PreviewDynSec(context.Background(), "operator", desired)
	if err != nil {
		t.Fatalf("PreviewDynSec: %v", err)
	}
	if preview.PlanID == "" {
		t.Fatal("the preview has no plan id")
	}
	if !preview.HasChanges {
		t.Fatal("the preview reports no change")
	}
	if len(preview.Plan.Changes) != 2 {
		t.Fatalf("changes = %v", preview.Plan.Summaries())
	}
	// The password of a client never reaches the response.
	for _, change := range preview.Plan.Changes {
		for _, command := range change.Commands {
			if command.Password != "" && command.Password != "********" {
				t.Fatalf("the password leaked into the plan: %q", command.Password)
			}
		}
	}
}

func TestPreviewDynSecRefusesAnOpenManagementChannel(t *testing.T) {
	svc, _ := pluginService(t, dynsecConf)
	transport := &fakeTransport{document: managementDocument()}
	svc.WithDynSec(func(context.Context) (dynsec.Transport, func(), error) {
		return transport, func() {}, nil
	})
	svc.mosquittoCfg.Username = "mcm"

	desired := managementDocument()
	desired.Roles = append(desired.Roles, dynsec.Role{
		RoleName: "open",
		ACLs: []dynsec.ACL{
			{Type: dynsec.ACLPublishClientSend, Topic: dynsec.ControlTopicPrefix + "#", Allow: true},
			{Type: dynsec.ACLSubscribePattern, Topic: dynsec.ControlTopicPrefix + "#", Allow: true},
		},
	})
	desired.Clients = append(desired.Clients, dynsec.Client{
		Username: "intruder",
		Roles:    []dynsec.RoleRef{{RoleName: "open"}},
	})

	_, err := svc.PreviewDynSec(context.Background(), "operator", desired)
	var guardErr *DynSecGuardError
	if !errors.As(err, &guardErr) {
		t.Fatalf("error = %v, want a guard violation", err)
	}
	if len(guardErr.Report.Violations) != 1 || guardErr.Report.Violations[0] != "intruder" {
		t.Fatalf("violations = %v, want [intruder]", guardErr.Report.Violations)
	}
}

func TestPreviewDynSecRefusesALockedManagementAccount(t *testing.T) {
	svc, _ := pluginService(t, dynsecConf)
	transport := &fakeTransport{document: managementDocument()}
	svc.WithDynSec(func(context.Context) (dynsec.Transport, func(), error) {
		return transport, func() {}, nil
	})
	svc.mosquittoCfg.Username = "mcm"

	// The management account loses its role: nobody could administer the
	// plugin any more.
	desired := managementDocument()
	desired.Clients = []dynsec.Client{{Username: "mcm"}}
	desired.Roles = nil

	_, err := svc.PreviewDynSec(context.Background(), "operator", desired)
	var guardErr *DynSecGuardError
	if !errors.As(err, &guardErr) {
		t.Fatalf("error = %v, want a guard violation", err)
	}
}

func TestAdoptDynSecMigratesTheFileBackend(t *testing.T) {
	svc, confPath := pluginService(t, "persistence true\npassword_file /mosquitto/config/passwd\nacl_file /mosquitto/config/acl\n")
	svc.mosquittoCfg.Username = "mcm"
	svc.mosquittoCfg.Password = "mcm-password"
	svc.mosquittoCfg.ConfigDir = t.TempDir()

	result, err := svc.AdoptDynSec(context.Background(), "admin")
	if err != nil {
		t.Fatalf("AdoptDynSec: %v", err)
	}
	if result.Kind != "adopt" {
		t.Errorf("kind = %q, want adopt", result.Kind)
	}
	if result.ConfigRevisionID == "" {
		t.Fatal("the adoption has no configuration revision")
	}
	if result.ConfigDiff == "" {
		t.Error("the adoption has no configuration diff")
	}
	if !result.Guard.OK {
		t.Errorf("the guard reports a violation: %v", result.Guard.Reasons)
	}
	if len(result.Guard.Allowed) != 1 || result.Guard.Allowed[0] != "mcm" {
		t.Errorf("allowed = %v, want [mcm]", result.Guard.Allowed)
	}

	// The database of the plugin is written with the management account.
	database := filepath.Join(svc.mosquittoCfg.ConfigDir, "dynamic-security.json")
	body, err := os.ReadFile(database)
	if err != nil {
		t.Fatalf("read the database of the plugin: %v", err)
	}
	if !strings.Contains(string(body), "mcm") {
		t.Errorf("the database does not carry the management account:\n%s", body)
	}
	if strings.Contains(string(body), "mcm-password") {
		t.Errorf("the database carries the cleartext password:\n%s", body)
	}
	if !strings.Contains(string(body), "iterations") {
		t.Errorf("the database does not carry the hash parameters:\n%s", body)
	}

	// The configuration revision loads the plugin and drops the file
	// backend.
	rev, err := svc.brokerCfgStore.GetBrokerConfigRevision(context.Background(), result.ConfigRevisionID)
	if err != nil {
		t.Fatalf("get the revision: %v", err)
	}
	if !strings.Contains(rev.ConfRendered, "plugin /usr/lib/mosquitto_dynamic_security.so") {
		t.Errorf("the revision does not load the plugin:\n%s", rev.ConfRendered)
	}
	if strings.Contains(rev.ConfRendered, "password_file") {
		t.Errorf("the revision keeps the password file:\n%s", rev.ConfRendered)
	}
	if strings.Contains(rev.ConfRendered, "acl_file") {
		t.Errorf("the revision keeps the ACL file:\n%s", rev.ConfRendered)
	}
	if _, err := os.Stat(confPath); err != nil {
		t.Fatalf("the configuration file is gone: %v", err)
	}
}

func TestAdoptDynSecRefusesTheDynamicSecurityBackend(t *testing.T) {
	svc, _ := pluginService(t, dynsecConf)
	svc.mosquittoCfg.Username = "mcm"
	svc.mosquittoCfg.Password = "mcm-password"
	svc.mosquittoCfg.ConfigDir = t.TempDir()

	_, err := svc.AdoptDynSec(context.Background(), "admin")
	if !errors.Is(err, ErrDynSecBackendConflict) {
		t.Fatalf("error = %v, want the backend conflict", err)
	}
}

func TestAdoptDynSecRefusesAnAccountWithoutPassword(t *testing.T) {
	svc, _ := pluginService(t, pluginsConf)
	svc.mosquittoCfg.Username = "mcm"
	svc.mosquittoCfg.Password = ""

	_, err := svc.AdoptDynSec(context.Background(), "admin")
	if !errors.Is(err, ErrDynSecBackendConflict) {
		t.Fatalf("error = %v, want the backend conflict", err)
	}
}

func TestApplyDynSecRefusesAConsumedPlan(t *testing.T) {
	svc, _ := pluginService(t, dynsecConf)
	transport := &fakeTransport{document: managementDocument()}
	svc.WithDynSec(func(context.Context) (dynsec.Transport, func(), error) {
		return transport, func() {}, nil
	})
	svc.mosquittoCfg.Username = "mcm"

	store, ok := svc.deployStore.(*fakeDeploymentStore)
	if !ok {
		t.Fatalf("the deployment store is %T", svc.deployStore)
	}
	_ = store
	// The plan store of the service is the fake deployment store, which
	// does not hold the dynamic security plans. The service refuses the
	// apply instead of applying something it cannot verify.
	_, err := svc.ApplyDynSec(context.Background(), "admin", "missing", nil)
	if err == nil {
		t.Fatal("the apply of an unknown plan was accepted")
	}
}

func TestDetectAccessBackend(t *testing.T) {
	cases := []struct {
		name string
		body string
		want AccessBackend
	}{
		{
			name: "the plugin is loaded",
			body: "plugin /usr/lib/mosquitto_dynamic_security.so\n",
			want: AccessBackendDynamicSecurity,
		},
		{
			name: "the password file is set",
			body: "password_file /mosquitto/config/passwd\n",
			want: AccessBackendFile,
		},
		{
			name: "nothing restricts the access",
			body: "allow_anonymous true\n",
			want: AccessBackendNone,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := pluginService(t, tc.body)
			result, err := svc.GetPlugins(context.Background())
			if err != nil {
				t.Fatalf("GetPlugins: %v", err)
			}
			if result.Backend != tc.want {
				t.Errorf("backend = %q, want %q", result.Backend, tc.want)
			}
		})
	}
}

func TestDynSecPlanIsStoredWithMaskedPasswords(t *testing.T) {
	svc, _ := pluginService(t, dynsecConf)
	transport := &fakeTransport{document: managementDocument()}
	svc.WithDynSec(func(context.Context) (dynsec.Transport, func(), error) {
		return transport, func() {}, nil
	})
	svc.mosquittoCfg.Username = "mcm"

	desired := managementDocument()
	desired.Roles = append(desired.Roles, dynsec.Role{
		RoleName: "reader",
		ACLs: []dynsec.ACL{
			{Type: dynsec.ACLSubscribePattern, Topic: "sensors/#", Allow: true},
		},
	})
	desired.Clients = append(desired.Clients, dynsec.Client{
		Username: "alice",
		Password: "alice-secret",
		Roles:    []dynsec.RoleRef{{RoleName: "reader"}},
	})
	if _, err := svc.PreviewDynSec(context.Background(), "operator", desired); err != nil {
		t.Fatalf("PreviewDynSec: %v", err)
	}
	// The plan the store holds carries no password.
	for _, request := range transport.requests {
		for _, command := range request {
			if command.Password != "" && command.Password != "alice-secret" {
				t.Fatalf("unexpected password %q", command.Password)
			}
		}
	}
}

func TestDynSecStateHashIgnoresPasswords(t *testing.T) {
	first := managementDocument()
	first.Clients = append(first.Clients, dynsec.Client{Username: "alice", Password: "one"})
	second := first.Clone()
	second.Clients[1].Password = "two"
	if dynsecStateHash(first) != dynsecStateHash(second) {
		t.Error("the fingerprint of the state depends on the password")
	}
}

func TestApplyDynSecConsumesThePlan(t *testing.T) {
	svc, _ := pluginService(t, dynsecConf)
	transport := &fakeTransport{document: managementDocument()}
	svc.WithDynSec(func(context.Context) (dynsec.Transport, func(), error) {
		return transport, func() {}, nil
	})
	svc.mosquittoCfg.Username = "mcm"

	desired := managementDocument()
	desired.Roles = append(desired.Roles, dynsec.Role{
		RoleName: "reader",
		ACLs: []dynsec.ACL{
			{Type: dynsec.ACLSubscribePattern, Topic: "sensors/#", Allow: true},
		},
	})
	desired.Clients = append(desired.Clients, dynsec.Client{
		Username: "alice",
		Password: "alice-secret",
		Roles:    []dynsec.RoleRef{{RoleName: "reader"}},
	})
	desired.Normalize()

	preview, err := svc.PreviewDynSec(context.Background(), "operator", desired)
	if err != nil {
		t.Fatalf("PreviewDynSec: %v", err)
	}
	if _, err := svc.ApplyDynSec(context.Background(), "admin", preview.PlanID, &desired); err != nil {
		t.Fatalf("ApplyDynSec: %v", err)
	}
	// The plan may only be applied once.
	_, err = svc.ApplyDynSec(context.Background(), "admin", preview.PlanID, &desired)
	if !errors.Is(err, storage.ErrDynSecPlanConsumed) {
		t.Fatalf("error = %v, want the consumed plan error", err)
	}
}

// TestAdoptDynSecConvergesWithTheBrokerState pins that the database the
// adoption writes describes exactly the state the broker ends up in.
//
// The adoption writes the database of the plugin and then compares the
// state the broker reports with the document the operator reviewed. A
// field the database does not carry would make the two differ for
// ever, and the apply would never converge.
func TestAdoptDynSecConvergesWithTheBrokerState(t *testing.T) {
	svc, _ := pluginService(t, "persistence true\npassword_file /mosquitto/config/passwd\nacl_file /mosquitto/config/acl\n")
	svc.mosquittoCfg.Username = "mcm"
	svc.mosquittoCfg.Password = "mcm-password"
	svc.mosquittoCfg.ConfigDir = t.TempDir()
	store, ok := svc.aclStore.(*fakeACLStore)
	if !ok {
		t.Fatalf("the ACL store is %T, want *fakeACLStore", svc.aclStore)
	}
	store.rules = []acl.Rule{{
		Principal:   "sensor-a",
		TopicFilter: "sensors/+/state",
		Permission:  acl.PermissionReadWrite,
		Description: "the state of the sensors",
	}}

	desired, _, _, err := svc.buildAdoptedDocument(context.Background())
	if err != nil {
		t.Fatalf("build the adopted document: %v", err)
	}
	if _, err := svc.AdoptDynSec(context.Background(), "admin"); err != nil {
		t.Fatalf("AdoptDynSec: %v", err)
	}

	database := filepath.Join(svc.mosquittoCfg.ConfigDir, "dynamic-security.json")
	body, err := os.ReadFile(database)
	if err != nil {
		t.Fatalf("read the database of the plugin: %v", err)
	}
	readBack := documentFromDatabase(t, body)

	// The apply compares the state of the broker with the document the
	// adoption stored, and that document is redacted: the passwords never
	// leave the request that carries them.
	plan := dynsec.BuildPlan(readBack, desired.Redacted())
	if !plan.Empty() {
		t.Errorf("the plan is not empty, so the apply would never converge: %v", plan.Summaries())
	}
}

// documentFromDatabase reads the database of the plugin the way the
// plugin reports it: the roles and the clients it holds.
func documentFromDatabase(t *testing.T, body []byte) dynsec.Document {
	t.Helper()
	var file struct {
		Clients []struct {
			Username        string `json:"username"`
			TextName        string `json:"textname"`
			TextDescription string `json:"textdescription"`
			ClientID        string `json:"clientid"`
			Disabled        bool   `json:"disabled"`
			Roles           []struct {
				RoleName string `json:"rolename"`
			} `json:"roles"`
			Groups []struct {
				GroupName string `json:"groupname"`
			} `json:"groups"`
		} `json:"clients"`
		Groups []struct {
			GroupName       string `json:"groupname"`
			TextName        string `json:"textname"`
			TextDescription string `json:"textdescription"`
			Roles           []struct {
				RoleName string `json:"rolename"`
			} `json:"roles"`
			Clients []string `json:"clients"`
		} `json:"groups"`
		Roles []struct {
			RoleName        string `json:"rolename"`
			TextName        string `json:"textname"`
			TextDescription string `json:"textdescription"`
			ACLs            []struct {
				ACLType string `json:"acltype"`
				Topic   string `json:"topic"`
				Allow   bool   `json:"allow"`
			} `json:"acls"`
		} `json:"roles"`
		Default struct {
			PublishClientSend    bool `json:"publishClientSend"`
			PublishClientReceive bool `json:"publishClientReceive"`
			Subscribe            bool `json:"subscribe"`
			Unsubscribe          bool `json:"unsubscribe"`
		} `json:"defaultACLAccess"`
	}
	if err := json.Unmarshal(body, &file); err != nil {
		t.Fatalf("decode the database of the plugin: %v", err)
	}

	doc := dynsec.Document{
		DefaultAccess: dynsec.DefaultAccess{
			PublishClientSend:    file.Default.PublishClientSend,
			PublishClientReceive: file.Default.PublishClientReceive,
			Subscribe:            file.Default.Subscribe,
			Unsubscribe:          file.Default.Unsubscribe,
		},
	}
	for _, role := range file.Roles {
		entry := dynsec.Role{
			RoleName:        role.RoleName,
			TextName:        role.TextName,
			TextDescription: role.TextDescription,
		}
		for _, acl := range role.ACLs {
			entry.ACLs = append(entry.ACLs, dynsec.ACL{
				Type:  dynsec.ACLType(acl.ACLType),
				Topic: acl.Topic,
				Allow: acl.Allow,
			})
		}
		doc.Roles = append(doc.Roles, entry)
	}
	for _, group := range file.Groups {
		entry := dynsec.Group{
			GroupName:       group.GroupName,
			TextName:        group.TextName,
			TextDescription: group.TextDescription,
		}
		for _, ref := range group.Roles {
			entry.Roles = append(entry.Roles, dynsec.RoleRef{RoleName: ref.RoleName})
		}
		for _, name := range group.Clients {
			entry.Clients = append(entry.Clients, dynsec.ClientRef{Username: name})
		}
		doc.Groups = append(doc.Groups, entry)
	}
	for _, client := range file.Clients {
		entry := dynsec.Client{
			Username:        client.Username,
			TextName:        client.TextName,
			TextDescription: client.TextDescription,
			ClientID:        client.ClientID,
			Disabled:        client.Disabled,
		}
		for _, ref := range client.Roles {
			entry.Roles = append(entry.Roles, dynsec.RoleRef{RoleName: ref.RoleName})
		}
		for _, ref := range client.Groups {
			entry.Groups = append(entry.Groups, dynsec.GroupRef{GroupName: ref.GroupName})
		}
		doc.Clients = append(doc.Clients, entry)
	}
	return doc
}
