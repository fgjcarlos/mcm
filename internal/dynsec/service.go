package dynsec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Service reads and writes the security database of the dynamic security
// plugin.
//
// Every change is verified: after the plugin applied the commands, MCM
// reads the database back and compares it with the desired state. A
// change that does not show up in the broker is reported as a failure
// instead of being assumed to have worked.
type Service struct {
	transport Transport
}

// NewService creates a service on top of a control channel.
func NewService(transport Transport) *Service {
	return &Service{transport: transport}
}

// Read returns the current state of the security database.
func (s *Service) Read(ctx context.Context) (Document, error) {
	if s == nil || s.transport == nil {
		return Document{}, fmt.Errorf("dynamic security control channel is not configured")
	}
	responses, err := s.transport.Do(ctx,
		Command{Name: CommandListClients, Verbose: true, Count: -1},
		Command{Name: CommandListGroups, Verbose: true, Count: -1},
		Command{Name: CommandListRoles, Verbose: true, Count: -1},
		Command{Name: CommandGetDefaultACLAccess},
		Command{Name: CommandGetAnonymousGroup},
	)
	if err != nil {
		return Document{}, fmt.Errorf("read the dynamic security database: %w", err)
	}
	if err := CheckResponses(responses, CommandListClients, CommandListGroups, CommandListRoles, CommandGetDefaultACLAccess, CommandGetAnonymousGroup); err != nil {
		return Document{}, err
	}

	doc := Document{}
	for _, response := range responses {
		switch response.Command {
		case CommandListClients:
			clients, _, err := ParseClients(response.Data)
			if err != nil {
				return Document{}, err
			}
			doc.Clients = clients
		case CommandListGroups:
			groups, _, err := ParseGroups(response.Data)
			if err != nil {
				return Document{}, err
			}
			doc.Groups = groups
		case CommandListRoles:
			roles, _, err := ParseRoles(response.Data)
			if err != nil {
				return Document{}, err
			}
			doc.Roles = roles
		case CommandGetDefaultACLAccess:
			access, err := ParseDefaultAccess(response.Data)
			if err != nil {
				return Document{}, err
			}
			doc.DefaultAccess = access
		case CommandGetAnonymousGroup:
			group, err := ParseAnonymousGroup(response.Data)
			if err != nil {
				return Document{}, err
			}
			doc.AnonymousGroup = group
		}
	}
	doc.Sort()
	return doc, nil
}

// Apply executes the plan and verifies that the broker ended up in the
// desired state.
//
// The commands of a change are sent together, so the plugin answers
// with one response per command. A refused command stops the apply: the
// rest of the plan is not sent, because the state of the database would
// no longer match the plan the operator reviewed.
func (s *Service) Apply(ctx context.Context, plan Plan, desired Document) (Document, error) {
	if s == nil || s.transport == nil {
		return Document{}, fmt.Errorf("dynamic security control channel is not configured")
	}
	// The commands of the whole plan travel in one request. The plugin
	// applies them in the order they are in and answers with one
	// response per command.
	//
	// The answer is not the only proof that the change landed. The
	// plugin disconnects the clients it just changed, and the answer is
	// published after that disconnect, so a change that touches the
	// management account never reaches MCM. The state of the database is
	// therefore read back and compared with the requested state, which
	// is what decides whether the change was applied.
	commands := plan.Commands()
	if len(commands) > 0 {
		responses, err := s.transport.Do(ctx, commands...)
		switch {
		case err == nil:
			for _, response := range responses {
				if response.Failed() {
					return Document{}, fmt.Errorf("%s: %w", response.Command, response.Err())
				}
			}
		case errors.Is(err, ErrConnectionLost), errors.Is(err, ErrResponseMismatch):
			// The answer was lost. The state is read back below.
		default:
			return Document{}, err
		}
	}

	// The change is only applied when the broker agrees with it. The
	// read is retried: the plugin disconnects the client it just
	// changed, so the connection may need a moment to come back.
	actual, err := s.readWithRetry(ctx)
	if err != nil {
		return Document{}, err
	}
	if issues := Verify(desired, actual); len(issues) > 0 {
		return actual, &VerificationError{Issues: issues}
	}
	return actual, nil
}

// readWithRetry reads the security database and retries while the
// control channel is reconnecting.
//
// The plugin disconnects the client it just changed. The connection
// needs a moment to come back, and the read must not fail because of it.
func (s *Service) readWithRetry(ctx context.Context) (Document, error) {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		var doc Document
		doc, err = s.Read(ctx)
		if err == nil {
			return doc, nil
		}
		if !errors.Is(err, ErrConnectionLost) {
			return Document{}, err
		}
		select {
		case <-ctx.Done():
			return Document{}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return Document{}, err
}

// CheckResponses reports the first refused command of a set of
// responses.
//
// The plugin answers every command of a request, so a refusal is always
// a finding about a specific command.
func CheckResponses(responses []CommandResponse, expected ...string) error {
	for _, response := range responses {
		if response.Failed() {
			return fmt.Errorf("the dynamic security plugin refused %q: %s", response.Command, response.Error)
		}
	}
	for _, name := range expected {
		found := false
		for _, response := range responses {
			if response.Command == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: the plugin did not answer %q", ErrResponseMismatch, name)
		}
	}
	return nil
}

// VerificationError carries the differences between the desired state
// and the state the broker reports.
type VerificationError struct {
	Issues []Issue
}

func (e *VerificationError) Error() string {
	if len(e.Issues) == 0 {
		return "the dynamic security database does not match the requested state"
	}
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		parts = append(parts, issue.Message)
	}
	return "the dynamic security database does not match the requested state: " + strings.Join(parts, "; ")
}

// Verify compares the desired state with the state the broker reports.
//
// The comparison covers the clients, the groups, the roles with their
// access entries, the default access and the group of the anonymous
// clients. A password is not compared: the plugin never returns it.
func Verify(desired, actual Document) []Issue {
	var issues []Issue

	actualClients := map[string]Client{}
	for _, client := range actual.Clients {
		actualClients[client.Username] = client
	}
	for _, client := range desired.Clients {
		found, ok := actualClients[client.Username]
		if !ok {
			issues = append(issues, Issue{
				Kind:    ObjectClient,
				Object:  client.Username,
				Message: fmt.Sprintf("the client %q is missing after the change", client.Username),
			})
			continue
		}
		delete(actualClients, client.Username)
		if found.Disabled != client.Disabled {
			issues = append(issues, Issue{
				Kind:    ObjectClient,
				Object:  client.Username,
				Field:   "disabled",
				Message: fmt.Sprintf("the client %q is %s after the change", client.Username, disabledState(client.Disabled)),
			})
		}
		if !sameRoles(found.Roles, client.Roles) {
			issues = append(issues, Issue{
				Kind:    ObjectClient,
				Object:  client.Username,
				Field:   "roles",
				Message: fmt.Sprintf("the roles of the client %q do not match the requested ones", client.Username),
			})
		}
		if !sameGroups(found.Groups, client.Groups) {
			issues = append(issues, Issue{
				Kind:    ObjectClient,
				Object:  client.Username,
				Field:   "groups",
				Message: fmt.Sprintf("the groups of the client %q do not match the requested ones", client.Username),
			})
		}
	}
	for username := range actualClients {
		issues = append(issues, Issue{
			Kind:    ObjectClient,
			Object:  username,
			Message: fmt.Sprintf("the client %q is still there after the change", username),
		})
	}

	actualGroups := map[string]Group{}
	for _, group := range actual.Groups {
		actualGroups[group.GroupName] = group
	}
	for _, group := range desired.Groups {
		found, ok := actualGroups[group.GroupName]
		if !ok {
			issues = append(issues, Issue{
				Kind:    ObjectGroup,
				Object:  group.GroupName,
				Message: fmt.Sprintf("the group %q is missing after the change", group.GroupName),
			})
			continue
		}
		delete(actualGroups, group.GroupName)
		if !sameRoles(found.Roles, group.Roles) {
			issues = append(issues, Issue{
				Kind:    ObjectGroup,
				Object:  group.GroupName,
				Field:   "roles",
				Message: fmt.Sprintf("the roles of the group %q do not match the requested ones", group.GroupName),
			})
		}
		if !sameClients(found.Clients, group.Clients) {
			issues = append(issues, Issue{
				Kind:    ObjectGroup,
				Object:  group.GroupName,
				Field:   "clients",
				Message: fmt.Sprintf("the clients of the group %q do not match the requested ones", group.GroupName),
			})
		}
	}
	for name := range actualGroups {
		issues = append(issues, Issue{
			Kind:    ObjectGroup,
			Object:  name,
			Message: fmt.Sprintf("the group %q is still there after the change", name),
		})
	}

	actualRoles := map[string]Role{}
	for _, role := range actual.Roles {
		actualRoles[role.RoleName] = role
	}
	for _, role := range desired.Roles {
		found, ok := actualRoles[role.RoleName]
		if !ok {
			issues = append(issues, Issue{
				Kind:    ObjectRole,
				Object:  role.RoleName,
				Message: fmt.Sprintf("the role %q is missing after the change", role.RoleName),
			})
			continue
		}
		delete(actualRoles, role.RoleName)
		if !sameACLs(found.ACLs, role.ACLs) {
			issues = append(issues, Issue{
				Kind:    ObjectRole,
				Object:  role.RoleName,
				Field:   "acls",
				Message: fmt.Sprintf("the access entries of the role %q do not match the requested ones", role.RoleName),
			})
		}
	}
	for name := range actualRoles {
		issues = append(issues, Issue{
			Kind:    ObjectRole,
			Object:  name,
			Message: fmt.Sprintf("the role %q is still there after the change", name),
		})
	}

	if desired.DefaultAccess != actual.DefaultAccess {
		issues = append(issues, Issue{
			Kind:    ObjectDefaultAccess,
			Object:  "default_access",
			Message: "the default access does not match the requested one",
		})
	}
	if desired.AnonymousGroup != actual.AnonymousGroup {
		issues = append(issues, Issue{
			Kind:    ObjectAnonymousGroup,
			Object:  "anonymous_group",
			Message: "the group of the anonymous clients does not match the requested one",
		})
	}
	return issues
}

// disabledState describes whether a client may log in.
func disabledState(disabled bool) string {
	if disabled {
		return "disabled"
	}
	return "enabled"
}

// sameRoles compares two lists of role references by name.
func sameRoles(a, b []RoleRef) bool {
	left := map[string]bool{}
	for _, ref := range a {
		left[ref.RoleName] = true
	}
	for _, ref := range b {
		if !left[ref.RoleName] {
			return false
		}
		delete(left, ref.RoleName)
	}
	return len(left) == 0
}

// sameGroups compares two lists of group references by name.
func sameGroups(a, b []GroupRef) bool {
	left := map[string]bool{}
	for _, ref := range a {
		left[ref.GroupName] = true
	}
	for _, ref := range b {
		if !left[ref.GroupName] {
			return false
		}
		delete(left, ref.GroupName)
	}
	return len(left) == 0
}

// sameClients compares two lists of client references by name.
func sameClients(a, b []ClientRef) bool {
	left := map[string]bool{}
	for _, ref := range a {
		left[ref.Username] = true
	}
	for _, ref := range b {
		if !left[ref.Username] {
			return false
		}
		delete(left, ref.Username)
	}
	return len(left) == 0
}

// sameACLs compares two lists of access entries. The priority is not
// compared: the plugin applies its own default when an entry does not
// carry one.
func sameACLs(a, b []ACL) bool {
	left := map[string]ACL{}
	for _, entry := range a {
		left[aclKey(entry)] = entry
	}
	for _, entry := range b {
		found, ok := left[aclKey(entry)]
		if !ok || found.Allow != entry.Allow {
			return false
		}
		delete(left, aclKey(entry))
	}
	return len(left) == 0
}

// aclKey identifies an access entry by its type and topic, which is how
// the plugin identifies it too.
func aclKey(entry ACL) string {
	return string(entry.Type) + "\x00" + entry.Topic
}

// MarshalCommands renders the commands of a plan, for the audit trail
// and for the review the operator sees.
//
// A password never reaches the rendering: the command is replaced by a
// marker so the trail shows that a password was set without carrying
// the credential.
func MarshalCommands(commands []Command) string {
	if len(commands) == 0 {
		return ""
	}
	safe := make([]Command, len(commands))
	for i, command := range commands {
		safe[i] = command
		if command.Password != "" {
			safe[i].Password = "********"
		}
	}
	body, err := json.Marshal(safe)
	if err != nil {
		return ""
	}
	return string(body)
}
