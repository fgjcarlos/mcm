package dynsec

import (
	"fmt"
	"sort"
	"strings"
)

// Action is what a change does to an object.
const (
	// ActionCreate adds an object that does not exist yet.
	ActionCreate = "create"
	// ActionUpdate changes an object that exists.
	ActionUpdate = "update"
	// ActionDelete removes an object.
	ActionDelete = "delete"
)

// Object kinds.
const (
	// ObjectClient is a broker client.
	ObjectClient = "client"
	// ObjectGroup is a group of clients.
	ObjectGroup = "group"
	// ObjectRole is a role with its access control entries.
	ObjectRole = "role"
	// ObjectDefaultAccess is the access granted when no entry matches.
	ObjectDefaultAccess = "default_access"
	// ObjectAnonymousGroup is the group of the anonymous clients.
	ObjectAnonymousGroup = "anonymous_group"
)

// Change is one planned modification of the security database.
type Change struct {
	// Kind is the kind of object the change applies to.
	Kind string `json:"kind"`
	// Name identifies the object.
	Name string `json:"name"`
	// Action is "create", "update" or "delete".
	Action string `json:"action"`
	// Summary explains the change in operator language.
	Summary string `json:"summary"`
	// Commands are the plugin commands that apply the change. They are
	// executed in the order the plan lists them.
	Commands []Command `json:"commands"`
}

// Plan is the set of changes that turn the current state of the security
// database into the desired one.
//
// The plan is the reviewable form of a change: the operator sees what
// MCM is about to do before any of it happens.
type Plan struct {
	// Changes are the modifications, in the order they must be applied.
	Changes []Change `json:"changes"`
}

// Empty reports whether the plan changes nothing.
func (p Plan) Empty() bool { return len(p.Changes) == 0 }

// Summaries returns the human readable summary of every change.
func (p Plan) Summaries() []string {
	out := make([]string, 0, len(p.Changes))
	for _, change := range p.Changes {
		out = append(out, change.Summary)
	}
	return out
}

// Commands returns every command of the plan, in order.
func (p Plan) Commands() []Command {
	var out []Command
	for _, change := range p.Changes {
		out = append(out, change.Commands...)
	}
	return out
}

// BuildPlan compares the current state of the security database with the
// desired one and returns the changes that turn the first into the
// second.
//
// The order of the changes matters: the roles are created before the
// groups and the clients that reference them, and the objects that are
// removed are deleted after the ones that reference them.
func BuildPlan(current, desired Document) Plan {
	var plan Plan

	for _, role := range desired.Roles {
		existing, ok := current.FindRole(role.RoleName)
		if !ok {
			plan.Changes = append(plan.Changes, Change{
				Kind:    ObjectRole,
				Name:    role.RoleName,
				Action:  ActionCreate,
				Summary: fmt.Sprintf("create the role %q with %d access entries", role.RoleName, len(role.ACLs)),
				Commands: []Command{{
					Name:            CommandCreateRole,
					RoleName:        role.RoleName,
					TextName:        role.TextName,
					TextDescription: role.TextDescription,
					ACLs:            roleACLs(role.ACLs),
				}},
			})
			continue
		}
		plan.Changes = append(plan.Changes, roleUpdates(existing, role)...)
	}

	for _, group := range desired.Groups {
		existing, ok := current.FindGroup(group.GroupName)
		if !ok {
			// The plugin creates a group with its roles. The clients of
			// the group are added afterwards, because the command that
			// creates the group does not carry them.
			plan.Changes = append(plan.Changes, Change{
				Kind:    ObjectGroup,
				Name:    group.GroupName,
				Action:  ActionCreate,
				Summary: fmt.Sprintf("create the group %q with %d roles", group.GroupName, len(group.Roles)),
				Commands: []Command{{
					Name:            CommandCreateGroup,
					GroupName:       group.GroupName,
					TextName:        group.TextName,
					TextDescription: group.TextDescription,
					Roles:           roleRefs(group.Roles),
				}},
			})
			continue
		}
		plan.Changes = append(plan.Changes, groupUpdates(existing, group)...)
	}

	for _, client := range desired.Clients {
		existing, ok := current.FindClient(client.Username)
		if !ok {
			plan.Changes = append(plan.Changes, Change{
				Kind:    ObjectClient,
				Name:    client.Username,
				Action:  ActionCreate,
				Summary: fmt.Sprintf("create the client %q", client.Username),
				Commands: []Command{{
					Name:            CommandCreateClient,
					Username:        client.Username,
					Password:        client.Password,
					ClientID:        client.ClientID,
					TextName:        client.TextName,
					TextDescription: client.TextDescription,
					// The membership of the groups is not set here:
					// the phase that owns the membership adds the
					// client to its groups once the client exists.
					Roles: roleRefs(client.Roles),
				}},
			})
			continue
		}
		plan.Changes = append(plan.Changes, clientUpdates(existing, client)...)
	}

	// The membership of the groups is changed after the clients, because
	// the plugin only adds a client to a group that exists already.
	deleted := map[string]bool{}
	for _, client := range current.Clients {
		if _, ok := desired.FindClient(client.Username); !ok {
			deleted[client.Username] = true
		}
	}
	for _, group := range desired.Groups {
		existing, ok := current.FindGroup(group.GroupName)
		if !ok {
			// The group does not exist yet: every client of it is
			// added once the group and the clients are there.
			for _, ref := range group.Clients {
				plan.Changes = append(plan.Changes, Change{
					Kind:    ObjectGroup,
					Name:    group.GroupName,
					Action:  ActionUpdate,
					Summary: fmt.Sprintf("add the client %q to the group %q", ref.Username, group.GroupName),
					Commands: []Command{{
						Name:      CommandAddGroupClient,
						GroupName: group.GroupName,
						Username:  ref.Username,
						Priority:  IntPtr(ref.Priority),
					}},
				})
			}
			continue
		}
		plan.Changes = append(plan.Changes, groupMembershipUpdates(existing, group, deleted)...)
	}

	// The objects that are gone are deleted last: a client is removed
	// before the group it belonged to, and a group before the role it
	// carried.
	for _, client := range current.Clients {
		if _, ok := desired.FindClient(client.Username); ok {
			continue
		}
		plan.Changes = append(plan.Changes, Change{
			Kind:    ObjectClient,
			Name:    client.Username,
			Action:  ActionDelete,
			Summary: fmt.Sprintf("delete the client %q", client.Username),
			Commands: []Command{{
				Name:     CommandDeleteClient,
				Username: client.Username,
			}},
		})
	}
	for _, group := range current.Groups {
		if _, ok := desired.FindGroup(group.GroupName); ok {
			continue
		}
		plan.Changes = append(plan.Changes, Change{
			Kind:    ObjectGroup,
			Name:    group.GroupName,
			Action:  ActionDelete,
			Summary: fmt.Sprintf("delete the group %q", group.GroupName),
			Commands: []Command{{
				Name:      CommandDeleteGroup,
				GroupName: group.GroupName,
			}},
		})
	}
	for _, role := range current.Roles {
		if _, ok := desired.FindRole(role.RoleName); ok {
			continue
		}
		plan.Changes = append(plan.Changes, Change{
			Kind:    ObjectRole,
			Name:    role.RoleName,
			Action:  ActionDelete,
			Summary: fmt.Sprintf("delete the role %q", role.RoleName),
			Commands: []Command{{
				Name:     CommandDeleteRole,
				RoleName: role.RoleName,
			}},
		})
	}

	if current.DefaultAccess != desired.DefaultAccess {
		plan.Changes = append(plan.Changes, Change{
			Kind:    ObjectDefaultAccess,
			Name:    "default_access",
			Action:  ActionUpdate,
			Summary: "change the access granted when no access entry matches",
			Commands: []Command{{
				Name: CommandSetDefaultACLAccess,
				ACLs: []CommandACL{
					{ACLType: string(DefaultPublishClientSend), Allow: BoolPtr(desired.DefaultAccess.PublishClientSend)},
					{ACLType: string(DefaultPublishClientReceive), Allow: BoolPtr(desired.DefaultAccess.PublishClientReceive)},
					{ACLType: string(DefaultSubscribe), Allow: BoolPtr(desired.DefaultAccess.Subscribe)},
					{ACLType: string(DefaultUnsubscribe), Allow: BoolPtr(desired.DefaultAccess.Unsubscribe)},
				},
			}},
		})
	}

	if current.AnonymousGroup != desired.AnonymousGroup {
		plan.Changes = append(plan.Changes, Change{
			Kind:    ObjectAnonymousGroup,
			Name:    "anonymous_group",
			Action:  ActionUpdate,
			Summary: anonymousGroupSummary(current.AnonymousGroup, desired.AnonymousGroup),
			Commands: []Command{{
				Name:      CommandSetAnonymousGroup,
				GroupName: desired.AnonymousGroup,
			}},
		})
	}

	return plan
}

// anonymousGroupSummary describes the change of the anonymous group.
func anonymousGroupSummary(current, desired string) string {
	switch {
	case desired == "":
		return "remove the group of the anonymous clients"
	case current == "":
		return fmt.Sprintf("give the anonymous clients the group %q", desired)
	default:
		return fmt.Sprintf("give the anonymous clients the group %q instead of %q", desired, current)
	}
}

// roleUpdates returns the changes that turn one role into another.
func roleUpdates(current, desired Role) []Change {
	var changes []Change

	if current.TextName != desired.TextName || current.TextDescription != desired.TextDescription {
		changes = append(changes, Change{
			Kind:    ObjectRole,
			Name:    desired.RoleName,
			Action:  ActionUpdate,
			Summary: fmt.Sprintf("change the description of the role %q", desired.RoleName),
			Commands: []Command{{
				Name:            CommandModifyRole,
				RoleName:        desired.RoleName,
				TextName:        desired.TextName,
				TextDescription: desired.TextDescription,
			}},
		})
	}

	added, removed := diffACLs(current.ACLs, desired.ACLs)
	for _, entry := range removed {
		changes = append(changes, Change{
			Kind:    ObjectRole,
			Name:    desired.RoleName,
			Action:  ActionUpdate,
			Summary: fmt.Sprintf("remove the %s access to %q from the role %q", entry.Type, entry.Topic, desired.RoleName),
			Commands: []Command{{
				Name:     CommandRemoveRoleACL,
				RoleName: desired.RoleName,
				ACLType:  string(entry.Type),
				Topic:    entry.Topic,
			}},
		})
	}
	for _, entry := range added {
		commands := []Command{{
			Name:     CommandAddRoleACL,
			RoleName: desired.RoleName,
			ACLType:  string(entry.Type),
			Topic:    entry.Topic,
			Allow:    BoolPtr(entry.Allow),
			Priority: IntPtr(entry.Priority),
		}}
		// A changed priority is a new entry: the plugin identifies an
		// entry by its type and topic, so the old one has to go
		// first.
		if previous, ok := findACL(current.ACLs, entry); ok && previous.Priority != entry.Priority {
			commands = append([]Command{{
				Name:     CommandRemoveRoleACL,
				RoleName: desired.RoleName,
				ACLType:  string(entry.Type),
				Topic:    entry.Topic,
			}}, commands...)
		}
		changes = append(changes, Change{
			Kind:     ObjectRole,
			Name:     desired.RoleName,
			Action:   ActionUpdate,
			Summary:  fmt.Sprintf("give the role %q %s access to %q", desired.RoleName, accessVerb(entry), entry.Topic),
			Commands: commands,
		})
	}
	return changes
}

// groupUpdates returns the changes that turn one group into another.
func groupUpdates(current, desired Group) []Change {
	var changes []Change

	if current.TextName != desired.TextName || current.TextDescription != desired.TextDescription {
		changes = append(changes, Change{
			Kind:    ObjectGroup,
			Name:    desired.GroupName,
			Action:  ActionUpdate,
			Summary: fmt.Sprintf("change the description of the group %q", desired.GroupName),
			Commands: []Command{{
				Name:            CommandModifyGroup,
				GroupName:       desired.GroupName,
				TextName:        desired.TextName,
				TextDescription: desired.TextDescription,
			}},
		})
	}

	for _, ref := range desired.Roles {
		if hasRole(current.Roles, ref.RoleName) {
			continue
		}
		changes = append(changes, Change{
			Kind:    ObjectGroup,
			Name:    desired.GroupName,
			Action:  ActionUpdate,
			Summary: fmt.Sprintf("give the group %q the role %q", desired.GroupName, ref.RoleName),
			Commands: []Command{{
				Name:      CommandAddGroupRole,
				GroupName: desired.GroupName,
				RoleName:  ref.RoleName,
				Priority:  IntPtr(ref.Priority),
			}},
		})
	}
	for _, ref := range current.Roles {
		if hasRole(desired.Roles, ref.RoleName) {
			continue
		}
		changes = append(changes, Change{
			Kind:    ObjectGroup,
			Name:    desired.GroupName,
			Action:  ActionUpdate,
			Summary: fmt.Sprintf("take the role %q away from the group %q", ref.RoleName, desired.GroupName),
			Commands: []Command{{
				Name:      CommandRemoveGroupRole,
				GroupName: desired.GroupName,
				RoleName:  ref.RoleName,
			}},
		})
	}

	for _, ref := range desired.Clients {
		if hasClient(current.Clients, ref.Username) {
			continue
		}
		changes = append(changes, Change{
			Kind:    ObjectGroup,
			Name:    desired.GroupName,
			Action:  ActionUpdate,
			Summary: fmt.Sprintf("add the client %q to the group %q", ref.Username, desired.GroupName),
			Commands: []Command{{
				Name:      CommandAddGroupClient,
				GroupName: desired.GroupName,
				Username:  ref.Username,
				Priority:  IntPtr(ref.Priority),
			}},
		})
	}
	for _, ref := range current.Clients {
		if hasClient(desired.Clients, ref.Username) {
			continue
		}
		changes = append(changes, Change{
			Kind:    ObjectGroup,
			Name:    desired.GroupName,
			Action:  ActionUpdate,
			Summary: fmt.Sprintf("remove the client %q from the group %q", ref.Username, desired.GroupName),
			Commands: []Command{{
				Name:      CommandRemoveGroupClient,
				GroupName: desired.GroupName,
				Username:  ref.Username,
			}},
		})
	}
	return changes
}

// groupMembershipUpdates returns the changes of the clients that belong
// to a group.
//
// The clients that are removed from the broker altogether are left alone:
// the plugin takes them out of the groups on its own.
func groupMembershipUpdates(current, desired Group, deleted map[string]bool) []Change {
	var changes []Change
	for _, ref := range desired.Clients {
		if hasClient(current.Clients, ref.Username) {
			continue
		}
		changes = append(changes, Change{
			Kind:    ObjectGroup,
			Name:    desired.GroupName,
			Action:  ActionUpdate,
			Summary: fmt.Sprintf("add the client %q to the group %q", ref.Username, desired.GroupName),
			Commands: []Command{{
				Name:      CommandAddGroupClient,
				GroupName: desired.GroupName,
				Username:  ref.Username,
				Priority:  IntPtr(ref.Priority),
			}},
		})
	}
	for _, ref := range current.Clients {
		if hasClient(desired.Clients, ref.Username) || deleted[ref.Username] {
			continue
		}
		changes = append(changes, Change{
			Kind:    ObjectGroup,
			Name:    desired.GroupName,
			Action:  ActionUpdate,
			Summary: fmt.Sprintf("remove the client %q from the group %q", ref.Username, desired.GroupName),
			Commands: []Command{{
				Name:      CommandRemoveGroupClient,
				GroupName: desired.GroupName,
				Username:  ref.Username,
			}},
		})
	}
	return changes
}

// clientUpdates returns the changes that turn one client into another.
func clientUpdates(current, desired Client) []Change {
	var changes []Change

	if current.TextName != desired.TextName || current.TextDescription != desired.TextDescription || current.ClientID != desired.ClientID {
		changes = append(changes, Change{
			Kind:    ObjectClient,
			Name:    desired.Username,
			Action:  ActionUpdate,
			Summary: fmt.Sprintf("change the details of the client %q", desired.Username),
			Commands: []Command{{
				Name:            CommandModifyClient,
				Username:        desired.Username,
				ClientID:        desired.ClientID,
				TextName:        desired.TextName,
				TextDescription: desired.TextDescription,
			}},
		})
	}

	if desired.Password != "" {
		changes = append(changes, Change{
			Kind:    ObjectClient,
			Name:    desired.Username,
			Action:  ActionUpdate,
			Summary: fmt.Sprintf("set a new password for the client %q", desired.Username),
			Commands: []Command{{
				Name:     CommandSetClientPassword,
				Username: desired.Username,
				Password: desired.Password,
			}},
		})
	}

	if current.Disabled != desired.Disabled {
		command := CommandEnableClient
		summary := fmt.Sprintf("enable the client %q", desired.Username)
		if desired.Disabled {
			command = CommandDisableClient
			summary = fmt.Sprintf("disable the client %q", desired.Username)
		}
		changes = append(changes, Change{
			Kind:    ObjectClient,
			Name:    desired.Username,
			Action:  ActionUpdate,
			Summary: summary,
			Commands: []Command{{
				Name:     command,
				Username: desired.Username,
			}},
		})
	}

	for _, ref := range desired.Roles {
		if hasRole(current.Roles, ref.RoleName) {
			continue
		}
		changes = append(changes, Change{
			Kind:    ObjectClient,
			Name:    desired.Username,
			Action:  ActionUpdate,
			Summary: fmt.Sprintf("give the client %q the role %q", desired.Username, ref.RoleName),
			Commands: []Command{{
				Name:     CommandAddClientRole,
				Username: desired.Username,
				RoleName: ref.RoleName,
				Priority: IntPtr(ref.Priority),
			}},
		})
	}
	for _, ref := range current.Roles {
		if hasRole(desired.Roles, ref.RoleName) {
			continue
		}
		changes = append(changes, Change{
			Kind:    ObjectClient,
			Name:    desired.Username,
			Action:  ActionUpdate,
			Summary: fmt.Sprintf("take the role %q away from the client %q", ref.RoleName, desired.Username),
			Commands: []Command{{
				Name:     CommandRemoveClientRole,
				Username: desired.Username,
				RoleName: ref.RoleName,
			}},
		})
	}

	// The membership of the groups is not changed here: the plugin
	// stores it in the group, and the phase that changes the membership
	// owns it.
	return changes
}

// diffACLs compares the access entries of two roles.
func diffACLs(current, desired []ACL) (added []ACL, removed []ACL) {
	for _, entry := range desired {
		previous, ok := findACL(current, entry)
		if !ok {
			added = append(added, entry)
			continue
		}
		if previous.Allow != entry.Allow || previous.Priority != entry.Priority {
			added = append(added, entry)
		}
	}
	for _, entry := range current {
		if _, ok := findACL(desired, entry); !ok {
			removed = append(removed, entry)
		}
	}
	return added, removed
}

// findACL returns the entry of the list that has the same type and
// topic.
func findACL(list []ACL, entry ACL) (ACL, bool) {
	for _, candidate := range list {
		if candidate.Type == entry.Type && candidate.Topic == entry.Topic {
			return candidate, true
		}
	}
	return ACL{}, false
}

// accessVerb describes an access entry in operator language.
func accessVerb(entry ACL) string {
	if entry.Allow {
		return "granted"
	}
	return "denied"
}

func hasRole(refs []RoleRef, name string) bool {
	for _, ref := range refs {
		if ref.RoleName == name {
			return true
		}
	}
	return false
}

func hasClient(refs []ClientRef, name string) bool {
	for _, ref := range refs {
		if ref.Username == name {
			return true
		}
	}
	return false
}

// roleACLs converts the access entries of a role into their wire form.
func roleACLs(entries []ACL) []CommandACL {
	out := make([]CommandACL, 0, len(entries))
	for _, entry := range entries {
		out = append(out, CommandACL{
			ACLType:  string(entry.Type),
			Topic:    entry.Topic,
			Priority: IntPtr(entry.Priority),
			Allow:    BoolPtr(entry.Allow),
		})
	}
	return out
}

// roleRefs converts the role references into their wire form.
func roleRefs(refs []RoleRef) []CommandRoleRef {
	out := make([]CommandRoleRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, CommandRoleRef{RoleName: ref.RoleName, Priority: IntPtr(ref.Priority)})
	}
	return out
}

// GuardReport describes who may reach the control channel of the plugin.
//
// The control channel is the only way to administer the security
// database. A client that may reach it can change every permission of
// the broker, so the report names every principal that must not.
type GuardReport struct {
	// OK reports whether the control channel is limited to the
	// management account.
	OK bool `json:"ok"`
	// ManagementClient is the account that administers the broker.
	ManagementClient string `json:"management_client,omitempty"`
	// Allowed lists the principals that may reach the channel.
	Allowed []string `json:"allowed,omitempty"`
	// Violations lists the principals that must not reach the channel.
	Violations []string `json:"violations,omitempty"`
	// Reasons explains the findings in operator language.
	Reasons []string `json:"reasons,omitempty"`
}

// CheckControlChannel reports which principals may reach the control
// channel of the plugin.
//
// A principal may reach the channel when one of its roles grants access
// to a topic under the control prefix, or when the default access lets
// every client publish or subscribe. The management account is allowed
// to reach it; every other principal is a violation.
func CheckControlChannel(doc Document, managementClient string) GuardReport {
	report := GuardReport{ManagementClient: managementClient}

	// The principals that may reach the channel, resolved through the
	// roles they carry.
	granted := map[string]bool{}
	roleReaches := map[string]bool{}
	for _, role := range doc.Roles {
		if roleGrantsControlChannel(role) {
			roleReaches[role.RoleName] = true
		}
	}

	for _, client := range doc.Clients {
		reaches := false
		for _, ref := range client.Roles {
			if roleReaches[ref.RoleName] {
				reaches = true
			}
		}
		for _, ref := range client.Groups {
			group, ok := doc.FindGroup(ref.GroupName)
			if !ok {
				continue
			}
			for _, roleRef := range group.Roles {
				if roleReaches[roleRef.RoleName] {
					reaches = true
				}
			}
		}
		if reaches {
			granted[client.Username] = true
		}
	}

	// The anonymous clients belong to a group as well.
	anonymousReaches := false
	if doc.AnonymousGroup != "" {
		if group, ok := doc.FindGroup(doc.AnonymousGroup); ok {
			for _, roleRef := range group.Roles {
				if roleReaches[roleRef.RoleName] {
					anonymousReaches = true
				}
			}
		}
	}

	// A default that grants publishing or subscribing opens the channel
	// to every client, the anonymous ones included.
	openDefaults := doc.DefaultAccess.PublishClientSend || doc.DefaultAccess.Subscribe
	if openDefaults {
		report.Reasons = append(report.Reasons, fmt.Sprintf(
			"the default access lets every client %s on the control channel; publish defaults to %t and subscribe defaults to %t",
			accessVerbOf(doc.DefaultAccess.PublishClientSend || doc.DefaultAccess.Subscribe),
			doc.DefaultAccess.PublishClientSend,
			doc.DefaultAccess.Subscribe,
		))
		if anonymousReaches {
			report.Violations = append(report.Violations, "anonymous clients")
		}
	}

	for _, client := range doc.Clients {
		if !granted[client.Username] {
			continue
		}
		if managementClient != "" && client.Username == managementClient {
			report.Allowed = append(report.Allowed, client.Username)
			continue
		}
		report.Violations = append(report.Violations, client.Username)
		report.Reasons = append(report.Reasons, fmt.Sprintf(
			"the client %q may reach the control channel of the plugin", client.Username,
		))
	}

	// The management account must keep its access. Without it nobody
	// could administer the database of the plugin any more, and the
	// change that removes it could not be undone through the control
	// channel.
	if managementClient != "" && !granted[managementClient] {
		report.Reasons = append(report.Reasons, fmt.Sprintf(
			"the management account %q may not reach the control channel of the plugin", managementClient,
		))
	}
	if anonymousReaches {
		report.Reasons = append(report.Reasons, "the anonymous clients may reach the control channel of the plugin")
	}

	sort.Strings(report.Allowed)
	sort.Strings(report.Violations)
	sort.Strings(report.Reasons)
	managementLocked := managementClient != "" && !granted[managementClient]
	report.OK = len(report.Violations) == 0 && !anonymousReaches && !openDefaults && !managementLocked
	return report
}

// roleGrantsControlChannel reports whether a role may reach the control
// channel of the plugin.
func roleGrantsControlChannel(role Role) bool {
	for _, entry := range role.ACLs {
		if !entry.Allow {
			continue
		}
		switch entry.Type {
		case ACLPublishClientSend, ACLPublishClientReceive, ACLSubscribeLiteral, ACLSubscribePattern:
		default:
			continue
		}
		if topicReachesControlChannel(entry.Topic) {
			return true
		}
	}
	return false
}

// topicReachesControlChannel reports whether a topic filter covers the
// control channel of the plugin.
func topicReachesControlChannel(topic string) bool {
	trimmed := strings.TrimSpace(topic)
	if trimmed == "" {
		return false
	}
	if trimmed == "$CONTROL" || trimmed == "$CONTROL/#" {
		return true
	}
	return strings.HasPrefix(trimmed, ControlTopicPrefix) || trimmed == ControlTopic
}

// accessVerbOf describes a boolean access in operator language.
func accessVerbOf(allowed bool) string {
	if allowed {
		return "publish"
	}
	return "subscribe"
}
