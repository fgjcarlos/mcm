package dynsec

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Command names of the dynamic security plugin. The plugin accepts them
// on the control topic; MCM only uses the ones it needs.
const (
	CommandListClients         = "listClients"
	CommandCreateClient        = "createClient"
	CommandDeleteClient        = "deleteClient"
	CommandModifyClient        = "modifyClient"
	CommandGetClient           = "getClient"
	CommandEnableClient        = "enableClient"
	CommandDisableClient       = "disableClient"
	CommandSetClientPassword   = "setClientPassword"
	CommandSetClientID         = "setClientId"
	CommandAddClientRole       = "addClientRole"
	CommandRemoveClientRole    = "removeClientRole"
	CommandListGroups          = "listGroups"
	CommandCreateGroup         = "createGroup"
	CommandDeleteGroup         = "deleteGroup"
	CommandModifyGroup         = "modifyGroup"
	CommandGetGroup            = "getGroup"
	CommandAddGroupClient      = "addGroupClient"
	CommandRemoveGroupClient   = "removeGroupClient"
	CommandAddGroupRole        = "addGroupRole"
	CommandRemoveGroupRole     = "removeGroupRole"
	CommandSetAnonymousGroup   = "setAnonymousGroup"
	CommandGetAnonymousGroup   = "getAnonymousGroup"
	CommandListRoles           = "listRoles"
	CommandCreateRole          = "createRole"
	CommandDeleteRole          = "deleteRole"
	CommandModifyRole          = "modifyRole"
	CommandGetRole             = "getRole"
	CommandAddRoleACL          = "addRoleACL"
	CommandRemoveRoleACL       = "removeRoleACL"
	CommandSetDefaultACLAccess = "setDefaultACLAccess"
	CommandGetDefaultACLAccess = "getDefaultACLAccess"
)

// CommandRoleRef is the wire form of a role reference. The priority is
// optional: the plugin uses its own default when it is missing.
type CommandRoleRef struct {
	RoleName string `json:"rolename"`
	Priority *int   `json:"priority,omitempty"`
}

// CommandGroupRef is the wire form of a group reference.
type CommandGroupRef struct {
	GroupName string `json:"groupname"`
	Priority  *int   `json:"priority,omitempty"`
}

// CommandClientRef is the wire form of a client reference.
type CommandClientRef struct {
	Username string `json:"username"`
	Priority *int   `json:"priority,omitempty"`
}

// CommandACL is the wire form of an access control entry.
//
// Allow is a pointer so a denied entry can be told apart from an entry
// that says nothing about the access at all.
type CommandACL struct {
	ACLType  string `json:"acltype"`
	Topic    string `json:"topic"`
	Priority *int   `json:"priority,omitempty"`
	Allow    *bool  `json:"allow,omitempty"`
}

// Command is one command of the plugin's control protocol.
//
// The field names are the ones the plugin expects, so a Command can be
// serialized onto the control topic as it is.
type Command struct {
	// Name is the command, e.g. "createClient".
	Name string `json:"command"`
	// Username addresses a client.
	Username string `json:"username,omitempty"`
	// Password carries a new password. It never leaves the process that
	// sends it.
	Password string `json:"password,omitempty"`
	// ClientID pins the MQTT client id of a client.
	ClientID string `json:"clientid,omitempty"`
	// TextName is the human readable name of a client, group or role.
	TextName string `json:"textname,omitempty"`
	// TextDescription describes a client, group or role.
	TextDescription string `json:"textdescription,omitempty"`
	// GroupName addresses a group.
	GroupName string `json:"groupname,omitempty"`
	// RoleName addresses a role.
	RoleName string `json:"rolename,omitempty"`
	// ACLType is the event an access control entry applies to.
	ACLType string `json:"acltype,omitempty"`
	// Topic is the topic or topic filter an access control entry applies
	// to.
	Topic string `json:"topic,omitempty"`
	// Allow reports whether an access control entry grants or denies.
	Allow *bool `json:"allow,omitempty"`
	// Priority orders the entries of a role and the roles of a client or
	// group.
	Priority *int `json:"priority,omitempty"`
	// Verbose asks the plugin for the full description of every object
	// instead of only their names.
	Verbose bool `json:"verbose,omitempty"`
	// Count and Offset page through a listing. A negative count asks
	// for every object.
	Count  int `json:"count,omitempty"`
	Offset int `json:"offset,omitempty"`
	// Roles, Groups and Clients carry the references of an object.
	Roles   []CommandRoleRef   `json:"roles,omitempty"`
	Groups  []CommandGroupRef  `json:"groups,omitempty"`
	Clients []CommandClientRef `json:"clients,omitempty"`
	// ACLs carries the access control entries of a role.
	ACLs []CommandACL `json:"acls,omitempty"`
	// CorrelationData is echoed back by the plugin, so the answer of a
	// request can be matched with the request itself.
	CorrelationData string `json:"correlationData,omitempty"`
}

// Request is the envelope MCM publishes on the control topic.
type Request struct {
	Commands []Command `json:"commands"`
}

// MarshalJSON encodes the request.
func (r Request) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Commands []Command `json:"commands"`
	}{Commands: r.Commands})
}

// CommandResponse is one answer of the plugin.
//
// The plugin answers every command of a request with one response, in
// the same order. A response that carries an Error means the command was
// refused; MCM never treats a refused command as applied.
type CommandResponse struct {
	// Command is the name of the command the answer belongs to.
	Command string `json:"command"`
	// Data carries the payload of a successful command.
	Data json.RawMessage `json:"data,omitempty"`
	// Error explains why the command was refused.
	Error string `json:"error,omitempty"`
	// CorrelationData is the value the request carried.
	CorrelationData string `json:"correlationData,omitempty"`
}

// Response is the envelope the plugin publishes on the response topic.
type Response struct {
	Responses []CommandResponse `json:"responses"`
}

// Failed reports whether the response carries an error.
func (r CommandResponse) Failed() bool {
	return strings.TrimSpace(r.Error) != ""
}

// Err returns the error of a refused command.
func (r CommandResponse) Err() error {
	if !r.Failed() {
		return nil
	}
	return fmt.Errorf("%s: %s", r.Command, r.Error)
}

// clientPayload is the wire form of a client.
type clientPayload struct {
	Username        string     `json:"username"`
	TextName        string     `json:"textname"`
	TextDescription string     `json:"textdescription"`
	ClientID        string     `json:"clientid"`
	Disabled        bool       `json:"disabled"`
	Roles           []RoleRef  `json:"roles"`
	Groups          []GroupRef `json:"groups"`
}

// groupPayload is the wire form of a group.
type groupPayload struct {
	GroupName       string      `json:"groupname"`
	TextName        string      `json:"textname"`
	TextDescription string      `json:"textdescription"`
	Roles           []RoleRef   `json:"roles"`
	Clients         []ClientRef `json:"clients"`
}

// rolePayload is the wire form of a role.
type rolePayload struct {
	RoleName        string `json:"rolename"`
	TextName        string `json:"textname"`
	TextDescription string `json:"textdescription"`
	ACLs            []ACL  `json:"acls"`
}

// clientListPayload is the answer of listClients. Without the verbose
// flag the plugin answers with the names only, so the list accepts both
// shapes.
type clientListPayload struct {
	TotalCount int             `json:"totalCount"`
	Clients    []clientPayload `json:"-"`
}

// UnmarshalJSON accepts both the verbose and the short form of the
// client list.
func (p *clientListPayload) UnmarshalJSON(data []byte) error {
	var raw struct {
		TotalCount int             `json:"totalCount"`
		Clients    json.RawMessage `json:"clients"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.TotalCount = raw.TotalCount
	if len(raw.Clients) == 0 {
		return nil
	}
	var verbose []clientPayload
	if err := json.Unmarshal(raw.Clients, &verbose); err == nil {
		p.Clients = verbose
		return nil
	}
	var names []string
	if err := json.Unmarshal(raw.Clients, &names); err != nil {
		return fmt.Errorf("decode client list: %w", err)
	}
	p.Clients = make([]clientPayload, 0, len(names))
	for _, name := range names {
		p.Clients = append(p.Clients, clientPayload{Username: name})
	}
	return nil
}

// groupListPayload is the answer of listGroups.
type groupListPayload struct {
	TotalCount int            `json:"totalCount"`
	Groups     []groupPayload `json:"groups"`
}

// roleListPayload is the answer of listRoles.
type roleListPayload struct {
	TotalCount int           `json:"totalCount"`
	Roles      []rolePayload `json:"roles"`
}

// defaultAccessPayload is the answer of getDefaultACLAccess. The plugin
// answers with a list of entries, one per default type.
type defaultAccessPayload struct {
	ACLs []defaultACLEntry `json:"acls"`
}

// defaultACLEntry is the wire form of a default access entry. The
// priority is not part of it.
type defaultACLEntry struct {
	ACLType string `json:"acltype"`
	Allow   *bool  `json:"allow,omitempty"`
}

// anonymousGroupPayload is the answer of getAnonymousGroup.
type anonymousGroupPayload struct {
	Group groupPayload `json:"group"`
}

// ParseClients decodes the answer of listClients.
func ParseClients(data []byte) ([]Client, int, error) {
	var payload clientListPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, 0, fmt.Errorf("decode clients: %w", err)
	}
	out := make([]Client, 0, len(payload.Clients))
	for _, c := range payload.Clients {
		out = append(out, Client{
			Username:        c.Username,
			TextName:        c.TextName,
			TextDescription: c.TextDescription,
			ClientID:        c.ClientID,
			Disabled:        c.Disabled,
			Roles:           convertRoleRefs(c.Roles),
			Groups:          convertGroupRefs(c.Groups),
		})
	}
	return out, payload.TotalCount, nil
}

// ParseGroups decodes the answer of listGroups.
func ParseGroups(data []byte) ([]Group, int, error) {
	var payload groupListPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, 0, fmt.Errorf("decode groups: %w", err)
	}
	out := make([]Group, 0, len(payload.Groups))
	for _, g := range payload.Groups {
		out = append(out, Group{
			GroupName:       g.GroupName,
			TextName:        g.TextName,
			TextDescription: g.TextDescription,
			Roles:           convertRoleRefs(g.Roles),
			Clients:         convertClientRefs(g.Clients),
		})
	}
	return out, payload.TotalCount, nil
}

// ParseRoles decodes the answer of listRoles.
func ParseRoles(data []byte) ([]Role, int, error) {
	var payload roleListPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, 0, fmt.Errorf("decode roles: %w", err)
	}
	out := make([]Role, 0, len(payload.Roles))
	for _, r := range payload.Roles {
		out = append(out, Role{
			RoleName:        r.RoleName,
			TextName:        r.TextName,
			TextDescription: r.TextDescription,
			ACLs:            convertACLs(r.ACLs),
		})
	}
	return out, payload.TotalCount, nil
}

// ParseDefaultAccess decodes the answer of getDefaultACLAccess.
func ParseDefaultAccess(data []byte) (DefaultAccess, error) {
	var payload defaultAccessPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return DefaultAccess{}, fmt.Errorf("decode default access: %w", err)
	}
	out := DefaultAccess{}
	for _, entry := range payload.ACLs {
		allow := entry.Allow != nil && *entry.Allow
		switch DefaultType(entry.ACLType) {
		case DefaultPublishClientSend:
			out.PublishClientSend = allow
		case DefaultPublishClientReceive:
			out.PublishClientReceive = allow
		case DefaultSubscribe:
			out.Subscribe = allow
		case DefaultUnsubscribe:
			out.Unsubscribe = allow
		}
	}
	return out, nil
}

// ParseAnonymousGroup decodes the answer of getAnonymousGroup.
func ParseAnonymousGroup(data []byte) (string, error) {
	var payload anonymousGroupPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("decode anonymous group: %w", err)
	}
	return payload.Group.GroupName, nil
}

// ParseClient decodes the answer of getClient.
func ParseClient(data []byte) (Client, error) {
	var payload struct {
		Client clientPayload `json:"client"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return Client{}, fmt.Errorf("decode client: %w", err)
	}
	return Client{
		Username:        payload.Client.Username,
		TextName:        payload.Client.TextName,
		TextDescription: payload.Client.TextDescription,
		ClientID:        payload.Client.ClientID,
		Disabled:        payload.Client.Disabled,
		Roles:           convertRoleRefs(payload.Client.Roles),
		Groups:          convertGroupRefs(payload.Client.Groups),
	}, nil
}

// ParseGroup decodes the answer of getGroup.
func ParseGroup(data []byte) (Group, error) {
	var payload struct {
		Group groupPayload `json:"group"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return Group{}, fmt.Errorf("decode group: %w", err)
	}
	return Group{
		GroupName:       payload.Group.GroupName,
		TextName:        payload.Group.TextName,
		TextDescription: payload.Group.TextDescription,
		Roles:           convertRoleRefs(payload.Group.Roles),
		Clients:         convertClientRefs(payload.Group.Clients),
	}, nil
}

// ParseRole decodes the answer of getRole.
func ParseRole(data []byte) (Role, error) {
	var payload struct {
		Role rolePayload `json:"role"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return Role{}, fmt.Errorf("decode role: %w", err)
	}
	return Role{
		RoleName:        payload.Role.RoleName,
		TextName:        payload.Role.TextName,
		TextDescription: payload.Role.TextDescription,
		ACLs:            convertACLs(payload.Role.ACLs),
	}, nil
}

func convertRoleRefs(in []RoleRef) []RoleRef {
	if in == nil {
		return []RoleRef{}
	}
	return append([]RoleRef(nil), in...)
}

func convertGroupRefs(in []GroupRef) []GroupRef {
	if in == nil {
		return []GroupRef{}
	}
	return append([]GroupRef(nil), in...)
}

func convertClientRefs(in []ClientRef) []ClientRef {
	if in == nil {
		return []ClientRef{}
	}
	return append([]ClientRef(nil), in...)
}

func convertACLs(in []ACL) []ACL {
	if in == nil {
		return []ACL{}
	}
	return append([]ACL(nil), in...)
}

// IntPtr returns a pointer to an int, for the optional wire fields.
func IntPtr(v int) *int { return &v }

// BoolPtr returns a pointer to a bool, for the optional wire fields.
func BoolPtr(v bool) *bool { return &v }
