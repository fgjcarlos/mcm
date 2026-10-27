package dynsec_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/fgjcarlos/mcm/internal/dynsec"
)

// fakeTransport answers the commands of a request with canned data.
type fakeTransport struct {
	responses map[string][]byte
	errors    map[string]string
	err       error
	requests  [][]dynsec.Command
}

func (f *fakeTransport) Do(_ context.Context, commands ...dynsec.Command) ([]dynsec.CommandResponse, error) {
	f.requests = append(f.requests, commands)
	if f.err != nil {
		return nil, f.err
	}
	out := make([]dynsec.CommandResponse, 0, len(commands))
	for _, command := range commands {
		if message, ok := f.errors[command.Name]; ok {
			out = append(out, dynsec.CommandResponse{Command: command.Name, Error: message})
			continue
		}
		payload, ok := f.responses[command.Name]
		if !ok {
			out = append(out, dynsec.CommandResponse{Command: command.Name})
			continue
		}
		out = append(out, dynsec.CommandResponse{Command: command.Name, Data: payload})
	}
	return out, nil
}

// TestReadParsesEveryCollection pins that the state of the broker is read
// completely: the clients, the groups, the roles, the default access and
// the group of the anonymous clients.
func TestReadParsesEveryCollection(t *testing.T) {
	transport := &fakeTransport{responses: map[string][]byte{
		dynsec.CommandListClients:         []byte(`{"totalCount":1,"clients":[{"username":"alice","textname":"Alice","roles":[{"rolename":"reader"}],"groups":[]}]}`),
		dynsec.CommandListGroups:          []byte(`{"totalCount":1,"groups":[{"groupname":"team","clients":[{"username":"alice"}],"roles":[{"rolename":"reader"}]}]}`),
		dynsec.CommandListRoles:           []byte(`{"totalCount":1,"roles":[{"rolename":"reader","acls":[{"acltype":"subscribePattern","topic":"sensors/#","priority":0,"allow":true}]}]}`),
		dynsec.CommandGetDefaultACLAccess: []byte(`{"acls":[{"acltype":"publishClientSend","allow":false},{"acltype":"publishClientReceive","allow":true},{"acltype":"subscribe","allow":false},{"acltype":"unsubscribe","allow":true}]}`),
		dynsec.CommandGetAnonymousGroup:   []byte(`{"group":{"groupname":"anon"}}`),
	}}
	service := dynsec.NewService(transport)

	doc, err := service.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(doc.Clients) != 1 || doc.Clients[0].Username != "alice" {
		t.Fatalf("clients = %+v", doc.Clients)
	}
	if len(doc.Groups) != 1 || doc.Groups[0].GroupName != "team" {
		t.Fatalf("groups = %+v", doc.Groups)
	}
	if len(doc.Roles) != 1 || doc.Roles[0].RoleName != "reader" {
		t.Fatalf("roles = %+v", doc.Roles)
	}
	if len(doc.Roles[0].ACLs) != 1 || doc.Roles[0].ACLs[0].Type != dynsec.ACLSubscribePattern {
		t.Fatalf("acls = %+v", doc.Roles[0].ACLs)
	}
	if doc.DefaultAccess.PublishClientReceive != true || doc.DefaultAccess.Subscribe != false {
		t.Fatalf("default access = %+v", doc.DefaultAccess)
	}
	if doc.AnonymousGroup != "anon" {
		t.Fatalf("anonymous group = %q", doc.AnonymousGroup)
	}
}

// TestReadRefusesAnAnswerWithAnError pins that a refused command is
// reported instead of being treated as an empty answer.
func TestReadRefusesAnAnswerWithAnError(t *testing.T) {
	transport := &fakeTransport{
		responses: map[string][]byte{
			dynsec.CommandListClients: []byte(`{"totalCount":0,"clients":[]}`),
		},
		errors: map[string]string{dynsec.CommandListGroups: "Unknown command"},
	}
	service := dynsec.NewService(transport)
	if _, err := service.Read(context.Background()); err == nil {
		t.Fatal("a refused command was accepted")
	}
}

// TestBuildPlanCreatesMissingObjects pins that the plan creates the
// objects that are missing and leaves the ones that are already there
// alone.
func TestBuildPlanCreatesMissingObjects(t *testing.T) {
	current := dynsec.Document{}
	desired := dynsec.Document{
		Roles: []dynsec.Role{{
			RoleName: "reader",
			ACLs: []dynsec.ACL{{
				Type:  dynsec.ACLSubscribePattern,
				Topic: "sensors/#",
				Allow: true,
			}},
		}},
		Clients: []dynsec.Client{{
			Username: "alice",
			Password: "secret",
			Roles:    []dynsec.RoleRef{{RoleName: "reader"}},
		}},
	}

	plan := dynsec.BuildPlan(current, desired)
	if len(plan.Changes) != 2 {
		t.Fatalf("changes = %d, want 2: %v", len(plan.Changes), plan.Summaries())
	}
	if plan.Changes[0].Kind != dynsec.ObjectRole || plan.Changes[0].Action != dynsec.ActionCreate {
		t.Errorf("the role must be created first: %+v", plan.Changes[0])
	}
	if plan.Changes[1].Kind != dynsec.ObjectClient || plan.Changes[1].Action != dynsec.ActionCreate {
		t.Errorf("the client must be created: %+v", plan.Changes[1])
	}
}

// TestBuildPlanIsEmptyForAnUnchangedState pins that a document that does
// not change produces no commands at all.
func TestBuildPlanIsEmptyForAnUnchangedState(t *testing.T) {
	doc := dynsec.Document{
		Roles: []dynsec.Role{{
			RoleName: "reader",
			ACLs: []dynsec.ACL{{
				Type:  dynsec.ACLSubscribePattern,
				Topic: "sensors/#",
				Allow: true,
			}},
		}},
		Clients: []dynsec.Client{{
			Username: "alice",
			Roles:    []dynsec.RoleRef{{RoleName: "reader"}},
		}},
	}
	plan := dynsec.BuildPlan(doc, doc)
	if !plan.Empty() {
		t.Fatalf("the plan is not empty: %v", plan.Summaries())
	}
}

// TestBuildPlanRemovesAndAdds pins that the plan describes the removal of
// an access entry and the addition of another one.
func TestBuildPlanRemovesAndAdds(t *testing.T) {
	current := dynsec.Document{Roles: []dynsec.Role{{
		RoleName: "reader",
		ACLs: []dynsec.ACL{
			{Type: dynsec.ACLPublishClientSend, Topic: "a/#", Allow: true},
			{Type: dynsec.ACLSubscribePattern, Topic: "b/#", Allow: true},
		},
	}}}
	desired := dynsec.Document{Roles: []dynsec.Role{{
		RoleName: "reader",
		ACLs: []dynsec.ACL{
			{Type: dynsec.ACLSubscribePattern, Topic: "b/#", Allow: true},
			{Type: dynsec.ACLSubscribePattern, Topic: "c/#", Allow: true},
		},
	}}}
	plan := dynsec.BuildPlan(current, desired)
	if len(plan.Changes) != 2 {
		t.Fatalf("changes = %v", plan.Summaries())
	}
	var removed, added bool
	for _, change := range plan.Changes {
		if len(change.Commands) == 0 {
			continue
		}
		switch change.Commands[0].Name {
		case dynsec.CommandRemoveRoleACL:
			removed = true
			if change.Commands[0].Topic != "a/#" {
				t.Errorf("removed the wrong entry: %+v", change.Commands[0])
			}
		case dynsec.CommandAddRoleACL:
			added = true
			if change.Commands[0].Topic != "c/#" {
				t.Errorf("added the wrong entry: %+v", change.Commands[0])
			}
		}
	}
	if !removed || !added {
		t.Errorf("the plan is incomplete: %v", plan.Summaries())
	}
}

// TestBuildPlanDeletesInDependencyOrder pins that the clients are deleted
// before the groups and the groups before the roles, so the plugin never
// refuses a deletion.
func TestBuildPlanDeletesInDependencyOrder(t *testing.T) {
	current := dynsec.Document{
		Clients: []dynsec.Client{{Username: "alice"}},
		Groups:  []dynsec.Group{{GroupName: "team"}},
		Roles:   []dynsec.Role{{RoleName: "reader"}},
	}
	plan := dynsec.BuildPlan(current, dynsec.Document{})
	if len(plan.Changes) != 3 {
		t.Fatalf("changes = %v", plan.Summaries())
	}
	if plan.Changes[0].Kind != dynsec.ObjectClient {
		t.Errorf("the client must be deleted first: %v", plan.Summaries())
	}
	if plan.Changes[1].Kind != dynsec.ObjectGroup {
		t.Errorf("the group must be deleted next: %v", plan.Summaries())
	}
	if plan.Changes[2].Kind != dynsec.ObjectRole {
		t.Errorf("the role must be deleted last: %v", plan.Summaries())
	}
}

// TestCheckControlChannelDetectsForeignAccess pins that a client that may
// reach the control channel is reported as a violation.
func TestCheckControlChannelDetectsForeignAccess(t *testing.T) {
	doc := dynsec.Document{
		Roles: []dynsec.Role{{
			RoleName: "admin",
			ACLs: []dynsec.ACL{
				{Type: dynsec.ACLPublishClientSend, Topic: "$CONTROL/dynamic-security/#", Allow: true},
				{Type: dynsec.ACLSubscribePattern, Topic: "$CONTROL/dynamic-security/#", Allow: true},
			},
		}},
		Clients: []dynsec.Client{
			{Username: "mcm", Roles: []dynsec.RoleRef{{RoleName: "admin"}}},
			{Username: "intruder", Roles: []dynsec.RoleRef{{RoleName: "admin"}}},
		},
		DefaultAccess: dynsec.DefaultAccess{PublishClientReceive: true, Unsubscribe: true},
	}
	report := dynsec.CheckControlChannel(doc, "mcm")
	if report.OK {
		t.Fatal("the foreign access to the control channel was not detected")
	}
	if len(report.Violations) != 1 || report.Violations[0] != "intruder" {
		t.Fatalf("violations = %v, want [intruder]", report.Violations)
	}
	if len(report.Allowed) != 1 || report.Allowed[0] != "mcm" {
		t.Fatalf("allowed = %v, want [mcm]", report.Allowed)
	}
}

// TestCheckControlChannelDetectsOpenDefaults pins that a default access
// that lets every client publish opens the control channel to everybody.
func TestCheckControlChannelDetectsOpenDefaults(t *testing.T) {
	doc := dynsec.Document{DefaultAccess: dynsec.DefaultAccess{PublishClientSend: true}}
	report := dynsec.CheckControlChannel(doc, "mcm")
	if report.OK {
		t.Fatal("the open default access was not detected")
	}
	if len(report.Reasons) == 0 {
		t.Error("the finding carries no reason")
	}
}

// TestCheckControlChannelAllowsAManagedAccount pins that the management
// account may reach the control channel.
func TestCheckControlChannelAllowsAManagedAccount(t *testing.T) {
	doc := dynsec.Document{
		Roles: []dynsec.Role{{
			RoleName: "admin",
			ACLs: []dynsec.ACL{
				{Type: dynsec.ACLPublishClientSend, Topic: "$CONTROL/dynamic-security/#", Allow: true},
				{Type: dynsec.ACLSubscribePattern, Topic: "$CONTROL/dynamic-security/#", Allow: true},
			},
		}},
		Clients: []dynsec.Client{{Username: "mcm", Roles: []dynsec.RoleRef{{RoleName: "admin"}}}},
		DefaultAccess: dynsec.DefaultAccess{
			PublishClientSend:    false,
			PublishClientReceive: true,
			Subscribe:            false,
			Unsubscribe:          true,
		},
	}
	report := dynsec.CheckControlChannel(doc, "mcm")
	if !report.OK {
		t.Fatalf("the management account was reported: %v", report.Reasons)
	}
}

// TestValidateRefusesDanglingReferences pins that a client that refers to
// a role that does not exist is refused.
func TestValidateRefusesDanglingReferences(t *testing.T) {
	doc := dynsec.Document{Clients: []dynsec.Client{{
		Username: "alice",
		Roles:    []dynsec.RoleRef{{RoleName: "ghost"}},
	}}}
	issues := dynsec.Validate(doc)
	if !dynsec.HasErrors(issues) {
		t.Fatal("a dangling role reference was accepted")
	}
}

// TestValidateRefusesInvalidTopic pins that an access entry with an
// invalid topic filter is refused.
func TestValidateRefusesInvalidTopic(t *testing.T) {
	doc := dynsec.Document{Roles: []dynsec.Role{{
		RoleName: "reader",
		ACLs: []dynsec.ACL{{
			Type:  dynsec.ACLSubscribePattern,
			Topic: "a/#/b",
			Allow: true,
		}},
	}}}
	issues := dynsec.Validate(doc)
	if !dynsec.HasErrors(issues) {
		t.Fatal("an invalid topic filter was accepted")
	}
}

// TestVerifyReportsADifference pins that a state that does not match the
// requested one is reported.
func TestVerifyReportsADifference(t *testing.T) {
	desired := dynsec.Document{Clients: []dynsec.Client{{Username: "alice"}}}
	actual := dynsec.Document{Clients: []dynsec.Client{{Username: "bob"}}}
	issues := dynsec.Verify(desired, actual)
	if len(issues) == 0 {
		t.Fatal("the difference was not reported")
	}
}

// TestApplyVerifiesTheResult pins that the apply reports a mismatch
// between the requested state and the state of the broker.
func TestApplyVerifiesTheResult(t *testing.T) {
	transport := &fakeTransport{responses: map[string][]byte{
		dynsec.CommandListClients:         []byte(`{"totalCount":0,"clients":[]}`),
		dynsec.CommandListGroups:          []byte(`{"totalCount":0,"groups":[]}`),
		dynsec.CommandListRoles:           []byte(`{"totalCount":0,"roles":[]}`),
		dynsec.CommandGetDefaultACLAccess: []byte(`{"acls":[]}`),
		dynsec.CommandGetAnonymousGroup:   []byte(`{"group":{"groupname":""}}`),
	}}
	service := dynsec.NewService(transport)
	desired := dynsec.Document{Clients: []dynsec.Client{{Username: "alice"}}}
	plan := dynsec.BuildPlan(dynsec.Document{}, desired)

	_, err := service.Apply(context.Background(), plan, desired)
	if err == nil {
		t.Fatal("the mismatch was not reported")
	}
	var verifyErr *dynsec.VerificationError
	if !errors.As(err, &verifyErr) {
		t.Fatalf("error = %v, want a verification error", err)
	}
}

// TestMarshalCommandsHidesThePassword pins that a password never reaches
// the rendering of a command.
func TestMarshalCommandsHidesThePassword(t *testing.T) {
	rendered := dynsec.MarshalCommands([]dynsec.Command{{
		Name:     dynsec.CommandCreateClient,
		Username: "alice",
		Password: "super-secret",
	}})
	if rendered == "" {
		t.Fatal("the command was not rendered")
	}
	if containsSecret(rendered, "super-secret") {
		t.Fatalf("the password leaked: %s", rendered)
	}
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(rendered), &decoded); err != nil {
		t.Fatalf("the rendering is not valid JSON: %v", err)
	}
}

func containsSecret(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// TestBuildPlanAddsAClientToAGroupOnlyOnce pins that the membership of a
// group is set by one command.
//
// The plugin refuses a client that is added to a group it already
// belongs to, so a plan that creates the client with its group and adds
// it to the group afterwards fails at the broker.
func TestBuildPlanAddsAClientToAGroupOnlyOnce(t *testing.T) {
	current := dynsec.Document{}
	desired := dynsec.Document{
		Roles: []dynsec.Role{{RoleName: "reader"}},
		Groups: []dynsec.Group{{
			GroupName: "team",
			Roles:     []dynsec.RoleRef{{RoleName: "reader"}},
			Clients:   []dynsec.ClientRef{{Username: "alice"}},
		}},
		Clients: []dynsec.Client{{
			Username: "alice",
			Password: "secret",
			Groups:   []dynsec.GroupRef{{GroupName: "team"}},
		}},
	}
	desired.Normalize()

	plan := dynsec.BuildPlan(current, desired)
	var adds int
	for _, change := range plan.Changes {
		for _, command := range change.Commands {
			if command.Name == dynsec.CommandAddGroupClient {
				adds++
			}
			if command.Name == dynsec.CommandCreateClient && len(command.Groups) > 0 {
				t.Errorf("the client must not be created with its groups: %+v", command)
			}
		}
	}
	if adds != 1 {
		t.Errorf("the client is added to the group %d times, want 1: %v", adds, plan.Summaries())
	}

	// The order matters: the group and the client must exist before the
	// client is added to the group.
	var groupAt, clientAt, addAt = -1, -1, -1
	for i, change := range plan.Changes {
		switch change.Commands[0].Name {
		case dynsec.CommandCreateGroup:
			groupAt = i
		case dynsec.CommandCreateClient:
			clientAt = i
		case dynsec.CommandAddGroupClient:
			addAt = i
		}
	}
	if groupAt < 0 || clientAt < 0 || addAt < 0 {
		t.Fatalf("the plan does not create the group, the client and the membership: %v", plan.Summaries())
	}
	if groupAt > clientAt || clientAt > addAt {
		t.Errorf("the order is wrong: group=%d client=%d add=%d", groupAt, clientAt, addAt)
	}
}
