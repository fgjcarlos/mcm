// Package dynsec models the security database of the Mosquitto dynamic
// security plugin (issue #304).
//
// The plugin owns the clients, groups, roles and topic permissions of
// the broker and keeps them in a JSON database of its own. MCM talks to
// the plugin over its control channel and never edits the database file
// directly, so the state MCM shows is the state the broker enforces.
//
// The vocabulary of the package is the one of the plugin: a role holds
// access control entries (ACLs) that say what a client may publish,
// receive, subscribe to and unsubscribe from, and a client or a group
// carries the roles that apply to it.
package dynsec

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fgjcarlos/mcm/internal/acl"
)

// ControlTopic is the topic the plugin listens on for commands.
const ControlTopic = "$CONTROL/dynamic-security/v1"

// ControlTopicPrefix is the topic prefix the plugin owns. No client
// except the management account may reach it.
const ControlTopicPrefix = "$CONTROL/dynamic-security/"

// ACLType is the kind of access an ACL entry grants or denies.
//
// The plugin evaluates the events of the MQTT protocol separately, so a
// role that may subscribe to a topic filter is not automatically allowed
// to receive the messages published to it.
type ACLType string

const (
	// ACLPublishClientSend allows the client to publish to the topic.
	ACLPublishClientSend ACLType = "publishClientSend"
	// ACLPublishClientReceive allows the client to receive the messages
	// published to the topic.
	ACLPublishClientReceive ACLType = "publishClientReceive"
	// ACLSubscribeLiteral allows the client to subscribe to the exact
	// topic.
	ACLSubscribeLiteral ACLType = "subscribeLiteral"
	// ACLSubscribePattern allows the client to subscribe to the topic
	// filter.
	ACLSubscribePattern ACLType = "subscribePattern"
	// ACLUnsubscribeLiteral allows the client to unsubscribe from the
	// exact topic.
	ACLUnsubscribeLiteral ACLType = "unsubscribeLiteral"
	// ACLUnsubscribePattern allows the client to unsubscribe from the
	// topic filter.
	ACLUnsubscribePattern ACLType = "unsubscribePattern"
)

// ACLTypes lists every accepted ACL type, in a stable order.
func ACLTypes() []ACLType {
	return []ACLType{
		ACLPublishClientSend,
		ACLPublishClientReceive,
		ACLSubscribeLiteral,
		ACLSubscribePattern,
		ACLUnsubscribeLiteral,
		ACLUnsubscribePattern,
	}
}

// Valid reports whether t is an ACL type the plugin accepts.
func (t ACLType) Valid() bool {
	for _, known := range ACLTypes() {
		if t == known {
			return true
		}
	}
	return false
}

// NeedsTopicFilter reports whether the ACL type is applied to a topic
// filter rather than to an exact topic.
func (t ACLType) NeedsTopicFilter() bool {
	return t == ACLSubscribePattern || t == ACLUnsubscribePattern
}

// DefaultType is one of the default access behaviours of the plugin.
//
// The defaults apply to every topic that no ACL matches. They are the
// reason a client that has no role at all cannot reach the control
// channel: publishing and subscribing default to deny.
type DefaultType string

const (
	// DefaultPublishClientSend is the default of a publish.
	DefaultPublishClientSend DefaultType = "publishClientSend"
	// DefaultPublishClientReceive is the default of a delivery.
	DefaultPublishClientReceive DefaultType = "publishClientReceive"
	// DefaultSubscribe is the default of a subscription.
	DefaultSubscribe DefaultType = "subscribe"
	// DefaultUnsubscribe is the default of an unsubscription.
	DefaultUnsubscribe DefaultType = "unsubscribe"
)

// DefaultTypes lists every accepted default type, in a stable order.
func DefaultTypes() []DefaultType {
	return []DefaultType{
		DefaultPublishClientSend,
		DefaultPublishClientReceive,
		DefaultSubscribe,
		DefaultUnsubscribe,
	}
}

// Valid reports whether t is a default type the plugin accepts.
func (t DefaultType) Valid() bool {
	for _, known := range DefaultTypes() {
		if t == known {
			return true
		}
	}
	return false
}

// ACL is one access control entry of a role.
type ACL struct {
	// Type is the event the entry applies to.
	Type ACLType `json:"acltype"`
	// Topic is the topic or topic filter the entry applies to.
	Topic string `json:"topic"`
	// Allow reports whether the entry grants or denies the access.
	Allow bool `json:"allow"`
	// Priority orders the entries of a role. The plugin evaluates the
	// entries with the highest priority first.
	Priority int `json:"priority"`
}

// RoleRef names a role and the priority it has for the client or group
// that references it.
type RoleRef struct {
	// RoleName is the name of the role.
	RoleName string `json:"rolename"`
	// Priority orders the roles of a client or group.
	Priority int `json:"priority"`
}

// GroupRef names a group and the priority it has for the client that
// references it.
type GroupRef struct {
	// GroupName is the name of the group.
	GroupName string `json:"groupname"`
	// Priority orders the groups of a client.
	Priority int `json:"priority"`
}

// ClientRef names a client and the priority it has for the group that
// references it.
type ClientRef struct {
	// Username is the name of the client.
	Username string `json:"username"`
	// Priority orders the clients of a group.
	Priority int `json:"priority"`
}

// Role is a set of access control entries that can be granted to clients
// and groups.
type Role struct {
	// RoleName is the name of the role.
	RoleName string `json:"rolename"`
	// TextName is the human readable name of the role.
	TextName string `json:"textname,omitempty"`
	// TextDescription describes the role.
	TextDescription string `json:"textdescription,omitempty"`
	// ACLs are the access control entries of the role.
	ACLs []ACL `json:"acls"`
}

// Client is a broker client the plugin authenticates.
type Client struct {
	// Username is the name of the client.
	Username string `json:"username"`
	// TextName is the human readable name of the client.
	TextName string `json:"textname,omitempty"`
	// TextDescription describes the client.
	TextDescription string `json:"textdescription,omitempty"`
	// ClientID pins the MQTT client id of the client.
	ClientID string `json:"clientid,omitempty"`
	// Password is the password of the client. It is write-only: the
	// server never returns it, only the flag that says whether one is
	// set.
	Password string `json:"password,omitempty"`
	// PasswordSet reports whether a password is configured. It is
	// derived by the server and is never sent by the client.
	PasswordSet bool `json:"password_set,omitempty"`
	// Roles are the roles granted to the client directly.
	Roles []RoleRef `json:"roles"`
	// Groups are the groups the client belongs to.
	Groups []GroupRef `json:"groups"`
	// Disabled reports whether the client is refused by the plugin.
	Disabled bool `json:"disabled,omitempty"`
}

// Redacted returns a copy of the client with the password removed and
// the matching flag raised.
func (c Client) Redacted() Client {
	out := c
	if out.Password != "" {
		out.PasswordSet = true
	}
	out.Password = ""
	return out
}

// Group is a set of clients that share roles.
type Group struct {
	// GroupName is the name of the group.
	GroupName string `json:"groupname"`
	// TextName is the human readable name of the group.
	TextName string `json:"textname,omitempty"`
	// TextDescription describes the group.
	TextDescription string `json:"textdescription,omitempty"`
	// Roles are the roles granted to the group.
	Roles []RoleRef `json:"roles"`
	// Clients are the clients that belong to the group.
	Clients []ClientRef `json:"clients"`
}

// DefaultAccess holds the access the plugin grants when no ACL matches.
type DefaultAccess struct {
	// PublishClientSend is the default of a publish.
	PublishClientSend bool `json:"publish_client_send"`
	// PublishClientReceive is the default of a delivery.
	PublishClientReceive bool `json:"publish_client_receive"`
	// Subscribe is the default of a subscription.
	Subscribe bool `json:"subscribe"`
	// Unsubscribe is the default of an unsubscription.
	Unsubscribe bool `json:"unsubscribe"`
}

// Document is the complete security database of the plugin.
type Document struct {
	// Clients are the broker clients.
	Clients []Client `json:"clients"`
	// Groups are the groups of clients.
	Groups []Group `json:"groups"`
	// Roles are the roles with their access control entries.
	Roles []Role `json:"roles"`
	// DefaultAccess is the access granted when no ACL matches.
	DefaultAccess DefaultAccess `json:"default_access"`
	// AnonymousGroup is the group the anonymous clients belong to. It is
	// empty when anonymous clients have no group.
	AnonymousGroup string `json:"anonymous_group,omitempty"`
}

// Clone returns a deep copy of the document.
func (d Document) Clone() Document {
	out := Document{
		Clients:        make([]Client, 0, len(d.Clients)),
		Groups:         make([]Group, 0, len(d.Groups)),
		Roles:          make([]Role, 0, len(d.Roles)),
		DefaultAccess:  d.DefaultAccess,
		AnonymousGroup: d.AnonymousGroup,
	}
	for _, c := range d.Clients {
		clone := c
		clone.Roles = append([]RoleRef(nil), c.Roles...)
		clone.Groups = append([]GroupRef(nil), c.Groups...)
		out.Clients = append(out.Clients, clone)
	}
	for _, g := range d.Groups {
		clone := g
		clone.Roles = append([]RoleRef(nil), g.Roles...)
		clone.Clients = append([]ClientRef(nil), g.Clients...)
		out.Groups = append(out.Groups, clone)
	}
	for _, r := range d.Roles {
		clone := r
		clone.ACLs = append([]ACL(nil), r.ACLs...)
		out.Roles = append(out.Roles, clone)
	}
	return out
}

// FindClient returns the client with the given username.
func (d Document) FindClient(username string) (Client, bool) {
	for _, c := range d.Clients {
		if c.Username == username {
			return c, true
		}
	}
	return Client{}, false
}

// FindGroup returns the group with the given name.
func (d Document) FindGroup(name string) (Group, bool) {
	for _, g := range d.Groups {
		if g.GroupName == name {
			return g, true
		}
	}
	return Group{}, false
}

// FindRole returns the role with the given name.
func (d Document) FindRole(name string) (Role, bool) {
	for _, r := range d.Roles {
		if r.RoleName == name {
			return r, true
		}
	}
	return Role{}, false
}

// Normalize makes the membership of the groups and the group list of the
// clients agree.
//
// The plugin stores the membership of a group in the group itself, so
// the group is the authority: a client that a group lists belongs to it,
// and a client that no group lists does not. The two views of the same
// membership otherwise drift apart, and a document that carries both
// would describe a membership that does not exist.
func (d *Document) Normalize() {
	members := map[string]map[string]bool{}
	for _, group := range d.Groups {
		for _, ref := range group.Clients {
			if members[ref.Username] == nil {
				members[ref.Username] = map[string]bool{}
			}
			members[ref.Username][group.GroupName] = true
		}
	}
	for i := range d.Clients {
		client := &d.Clients[i]
		groups := make([]GroupRef, 0, len(client.Groups))
		for name := range members[client.Username] {
			groups = append(groups, GroupRef{GroupName: name})
		}
		client.Groups = groups
	}
}

// Redacted returns a copy of the document with every password removed.
func (d Document) Redacted() Document {
	out := d.Clone()
	for i := range out.Clients {
		out.Clients[i] = out.Clients[i].Redacted()
	}
	return out
}

// Sort orders the collections of the document so the API and the UI
// always present them in the same order.
func (d *Document) Sort() {
	sort.SliceStable(d.Clients, func(i, j int) bool { return d.Clients[i].Username < d.Clients[j].Username })
	sort.SliceStable(d.Groups, func(i, j int) bool { return d.Groups[i].GroupName < d.Groups[j].GroupName })
	sort.SliceStable(d.Roles, func(i, j int) bool { return d.Roles[i].RoleName < d.Roles[j].RoleName })
	for i := range d.Roles {
		sort.SliceStable(d.Roles[i].ACLs, func(a, b int) bool {
			left, right := d.Roles[i].ACLs[a], d.Roles[i].ACLs[b]
			if left.Type != right.Type {
				return left.Type < right.Type
			}
			return left.Topic < right.Topic
		})
	}
	for i := range d.Clients {
		sort.SliceStable(d.Clients[i].Roles, func(a, b int) bool {
			return d.Clients[i].Roles[a].RoleName < d.Clients[i].Roles[b].RoleName
		})
		sort.SliceStable(d.Clients[i].Groups, func(a, b int) bool {
			return d.Clients[i].Groups[a].GroupName < d.Clients[i].Groups[b].GroupName
		})
	}
	for i := range d.Groups {
		sort.SliceStable(d.Groups[i].Roles, func(a, b int) bool {
			return d.Groups[i].Roles[a].RoleName < d.Groups[i].Roles[b].RoleName
		})
		sort.SliceStable(d.Groups[i].Clients, func(a, b int) bool {
			return d.Groups[i].Clients[a].Username < d.Groups[i].Clients[b].Username
		})
	}
}

// Issue kinds.
const (
	// IssueKindName marks a name the plugin does not accept.
	IssueKindName = "name"
	// IssueKindTopic marks a topic filter the plugin does not accept.
	IssueKindTopic = "topic"
	// IssueKindValue marks a value outside the accepted set.
	IssueKindValue = "value"
	// IssueKindReference marks a reference to an object that does not
	// exist.
	IssueKindReference = "reference"
	// IssueKindDuplicate marks an object listed more than once.
	IssueKindDuplicate = "duplicate"
)

// Issue describes one problem with a security database.
type Issue struct {
	// Kind groups the finding.
	Kind string `json:"kind"`
	// Object is the client, group or role the finding belongs to.
	Object string `json:"object,omitempty"`
	// Field is the offending field.
	Field string `json:"field,omitempty"`
	// Message explains the finding in operator language.
	Message string `json:"message"`
}

// HasErrors reports whether the findings contain at least one problem.
func HasErrors(issues []Issue) bool {
	return len(issues) > 0
}

// Validate checks a security database before it is sent to the plugin.
//
// It never returns an error; every problem is an Issue so the operator
// sees all of them at once.
func Validate(doc Document) []Issue {
	var issues []Issue

	clients := map[string]bool{}
	for _, c := range doc.Clients {
		if strings.TrimSpace(c.Username) == "" {
			issues = append(issues, Issue{
				Kind:    IssueKindName,
				Object:  c.Username,
				Field:   "username",
				Message: "a client needs a username",
			})
			continue
		}
		if !validObjectName(c.Username) {
			issues = append(issues, Issue{
				Kind:    IssueKindName,
				Object:  c.Username,
				Field:   "username",
				Message: fmt.Sprintf("the client name %q must not contain a wildcard or a control character", c.Username),
			})
		}
		if clients[c.Username] {
			issues = append(issues, Issue{
				Kind:    IssueKindDuplicate,
				Object:  c.Username,
				Field:   "username",
				Message: fmt.Sprintf("the client %q is listed more than once", c.Username),
			})
		}
		clients[c.Username] = true
	}

	groups := map[string]bool{}
	for _, g := range doc.Groups {
		if strings.TrimSpace(g.GroupName) == "" {
			issues = append(issues, Issue{
				Kind:    IssueKindName,
				Object:  g.GroupName,
				Field:   "group_name",
				Message: "a group needs a name",
			})
			continue
		}
		if !validObjectName(g.GroupName) {
			issues = append(issues, Issue{
				Kind:    IssueKindName,
				Object:  g.GroupName,
				Field:   "group_name",
				Message: fmt.Sprintf("the group name %q must not contain a wildcard or a control character", g.GroupName),
			})
		}
		if groups[g.GroupName] {
			issues = append(issues, Issue{
				Kind:    IssueKindDuplicate,
				Object:  g.GroupName,
				Field:   "group_name",
				Message: fmt.Sprintf("the group %q is listed more than once", g.GroupName),
			})
		}
		groups[g.GroupName] = true
	}

	roles := map[string]bool{}
	for _, r := range doc.Roles {
		if strings.TrimSpace(r.RoleName) == "" {
			issues = append(issues, Issue{
				Kind:    IssueKindName,
				Object:  r.RoleName,
				Field:   "role_name",
				Message: "a role needs a name",
			})
			continue
		}
		if !validObjectName(r.RoleName) {
			issues = append(issues, Issue{
				Kind:    IssueKindName,
				Object:  r.RoleName,
				Field:   "role_name",
				Message: fmt.Sprintf("the role name %q must not contain a wildcard or a control character", r.RoleName),
			})
		}
		if roles[r.RoleName] {
			issues = append(issues, Issue{
				Kind:    IssueKindDuplicate,
				Object:  r.RoleName,
				Field:   "role_name",
				Message: fmt.Sprintf("the role %q is listed more than once", r.RoleName),
			})
		}
		roles[r.RoleName] = true
		for _, entry := range r.ACLs {
			if !entry.Type.Valid() {
				issues = append(issues, Issue{
					Kind:    IssueKindValue,
					Object:  r.RoleName,
					Field:   "acls",
					Message: fmt.Sprintf("the role %q carries the unknown access type %q", r.RoleName, entry.Type),
				})
				continue
			}
			if strings.TrimSpace(entry.Topic) == "" {
				issues = append(issues, Issue{
					Kind:    IssueKindTopic,
					Object:  r.RoleName,
					Field:   "acls",
					Message: fmt.Sprintf("the role %q carries an access entry without a topic", r.RoleName),
				})
				continue
			}
			if err := acl.ValidateTopicFilter(entry.Topic); err != nil {
				issues = append(issues, Issue{
					Kind:    IssueKindTopic,
					Object:  r.RoleName,
					Field:   "acls",
					Message: fmt.Sprintf("the role %q carries the invalid topic %q: %s", r.RoleName, entry.Topic, err.Error()),
				})
			}
		}
	}

	// References must point at an object that exists.
	for _, c := range doc.Clients {
		for _, ref := range c.Roles {
			if !roles[ref.RoleName] {
				issues = append(issues, Issue{
					Kind:    IssueKindReference,
					Object:  c.Username,
					Field:   "roles",
					Message: fmt.Sprintf("the client %q references the unknown role %q", c.Username, ref.RoleName),
				})
			}
		}
		for _, ref := range c.Groups {
			if !groups[ref.GroupName] {
				issues = append(issues, Issue{
					Kind:    IssueKindReference,
					Object:  c.Username,
					Field:   "groups",
					Message: fmt.Sprintf("the client %q references the unknown group %q", c.Username, ref.GroupName),
				})
			}
		}
	}
	for _, g := range doc.Groups {
		for _, ref := range g.Roles {
			if !roles[ref.RoleName] {
				issues = append(issues, Issue{
					Kind:    IssueKindReference,
					Object:  g.GroupName,
					Field:   "roles",
					Message: fmt.Sprintf("the group %q references the unknown role %q", g.GroupName, ref.RoleName),
				})
			}
		}
		for _, ref := range g.Clients {
			if !clients[ref.Username] {
				issues = append(issues, Issue{
					Kind:    IssueKindReference,
					Object:  g.GroupName,
					Field:   "clients",
					Message: fmt.Sprintf("the group %q references the unknown client %q", g.GroupName, ref.Username),
				})
			}
		}
	}
	if doc.AnonymousGroup != "" && !groups[doc.AnonymousGroup] {
		issues = append(issues, Issue{
			Kind:    IssueKindReference,
			Object:  doc.AnonymousGroup,
			Field:   "anonymous_group",
			Message: fmt.Sprintf("the anonymous group %q does not exist", doc.AnonymousGroup),
		})
	}
	return issues
}

// validObjectName reports whether a client, group or role name is one
// the plugin accepts.
func validObjectName(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	if strings.ContainsAny(name, "#+\x00\n\r") {
		return false
	}
	return true
}
