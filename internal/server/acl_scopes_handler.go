package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// aclScopeLister lists the listeners that can own an ACL scope.
type aclScopeLister interface {
	ListListenerSpecs(ctx context.Context) ([]storage.ListenerSpecRow, error)
}

// aclMQTTUserLister lists the MQTT users the effective-permission view
// resolves.
type aclMQTTUserLister interface {
	ListMQTTUsers(ctx context.Context) ([]storage.MQTTUser, error)
}

// aclSourceLister reads and writes the adopted ACL sources.
//
// An adopted source is the verbatim body of an ACL file MCM took over. The
// deploy service composes the managed rules into it so the content MCM
// does not model survives the deploy.
type aclSourceLister interface {
	GetACLSource(ctx context.Context, path string) (storage.ACLSourceRow, error)
	SaveACLSource(ctx context.Context, path string, body string) (storage.ACLSourceRow, error)
	DeleteACLSource(ctx context.Context, path string) error
}

// aclScopeAPI groups the handlers that expose the native ACL surface beyond
// the rule CRUD: the scopes a rule can belong to, the adopted ACL sources
// and the effective permissions of an identity.
//
// The effective-permission view and the simulator are the answer to "what
// can this client actually do". They follow the documented Mosquitto
// semantics — deny before grant, the anonymous section for unauthenticated
// clients only, and pattern rules for every client — instead of listing the
// rules back.
type aclScopeAPI struct {
	store     acl.Store
	listeners aclScopeLister
	users     aclMQTTUserLister
	sources   aclSourceLister
	// aclPath is the path of the broker-wide ACL file. It is the anchor
	// for the per-listener file paths.
	aclPath string
	// perListenerSettings reports whether the broker configuration
	// resolves the access control options per listener.
	perListenerSettings   func(ctx context.Context) (bool, error)
	audit                 func(r *http.Request, action string, resourceType string, resourceID string, result string, metadata map[string]any)
	recordSecurityFailure func(*http.Request, string, string, string)
}

// aclScopeDescriptor describes one ACL file MCM manages.
type aclScopeDescriptor struct {
	// Scope is the scope of the file: "global" or "listener".
	Scope acl.Scope `json:"scope"`
	// ListenerID names the listener whose file this is. It is empty for
	// the broker-wide file.
	ListenerID string `json:"listener_id,omitempty"`
	// Path is the absolute path of the file.
	Path string `json:"path"`
	// RuleCount is the number of managed rules the file carries.
	RuleCount int `json:"rule_count"`
	// Wired reports whether the broker configuration actually applies
	// this file: the listener must declare it as its acl_file and
	// Mosquitto must resolve the access control options per listener.
	Wired bool `json:"wired"`
	// WiringIssue explains why the file is not wired.
	WiringIssue string `json:"wiring_issue,omitempty"`
}

// aclScopesResponse is the response of GET /api/v1/acls/scopes.
type aclScopesResponse struct {
	Scopes []aclScopeDescriptor `json:"scopes"`
}

// handleListACLScopes serves GET /api/v1/acls/scopes.
//
// It reports every ACL file MCM manages: the broker-wide file and the
// per-listener files of the listeners that own scoped rules. The response
// also says whether the broker configuration applies the file, so an
// operator sees the missing wiring before a deploy refuses it.
func (a *aclScopeAPI) handleListScopes(w http.ResponseWriter, r *http.Request) {
	scopes, err := a.describeScopes(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, aclErrorResponse{Error: "failed to list acl scopes"})
		return
	}
	writeJSON(w, http.StatusOK, aclScopesResponse{Scopes: scopes})
}

// describeScopes returns the ACL files MCM manages.
func (a *aclScopeAPI) describeScopes(ctx context.Context) ([]aclScopeDescriptor, error) {
	rules, err := a.store.ListRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("list acl rules: %w", err)
	}
	counts := map[string]int{}
	globalCount := 0
	for _, rule := range rules {
		if rule.IsListenerScoped() {
			counts[rule.ListenerID]++
			continue
		}
		globalCount++
	}

	perListener, err := a.perListenerEnabled(ctx)
	if err != nil {
		return nil, err
	}
	specs, err := a.listenerSpecs(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]storage.ListenerSpecRow, len(specs))
	for _, spec := range specs {
		byID[spec.ID] = spec
	}

	out := []aclScopeDescriptor{{
		Scope:     acl.ScopeGlobal,
		Path:      a.aclPath,
		RuleCount: globalCount,
		Wired:     true,
	}}
	listenerIDs := make([]string, 0, len(counts))
	for id := range counts {
		listenerIDs = append(listenerIDs, id)
	}
	sort.Strings(listenerIDs)
	for _, id := range listenerIDs {
		path := mosquitto.ListenerACLPath(a.aclPath, id)
		descriptor := aclScopeDescriptor{
			Scope:      acl.ScopeListener,
			ListenerID: id,
			Path:       path,
			RuleCount:  counts[id],
		}
		spec, known := byID[id]
		switch {
		case !known:
			descriptor.WiringIssue = fmt.Sprintf("listener %q does not exist", id)
		case strings.TrimSpace(spec.Options["acl_file"]) != path:
			declared := strings.TrimSpace(spec.Options["acl_file"])
			if declared == "" {
				descriptor.WiringIssue = fmt.Sprintf("listener %q does not declare acl_file %q", id, path)
			} else {
				descriptor.WiringIssue = fmt.Sprintf("listener %q declares acl_file %q but MCM writes %q", id, declared, path)
			}
		case !perListener:
			descriptor.WiringIssue = "per_listener_settings is not enabled; Mosquitto would ignore the acl_file of the listener"
		default:
			descriptor.Wired = true
		}
		out = append(out, descriptor)
	}
	return out, nil
}

// listenerSpecs returns the listener specs MCM manages.
func (a *aclScopeAPI) listenerSpecs(ctx context.Context) ([]storage.ListenerSpecRow, error) {
	if a.listeners == nil {
		return nil, nil
	}
	specs, err := a.listeners.ListListenerSpecs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list listener specs: %w", err)
	}
	return specs, nil
}

// perListenerEnabled reports whether the broker configuration resolves the
// access control options per listener.
func (a *aclScopeAPI) perListenerEnabled(ctx context.Context) (bool, error) {
	if a.perListenerSettings == nil {
		return true, nil
	}
	return a.perListenerSettings(ctx)
}

// aclSourceResponse is the response of GET /api/v1/acls/source.
type aclSourceResponse struct {
	Path        string `json:"path"`
	Adopted     bool   `json:"adopted"`
	Body        string `json:"body,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
}

// handleGetACLSource serves GET /api/v1/acls/source.
//
// It returns the ACL source MCM adopted for the broker-wide ACL file. The
// source is the verbatim content of the file MCM took over; the deploy
// composes the managed rules into it so nothing is lost.
func (a *aclScopeAPI) handleGetSource(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		path = a.aclPath
	}
	response := aclSourceResponse{Path: path}
	if a.sources != nil {
		row, err := a.sources.GetACLSource(r.Context(), path)
		switch {
		case err == nil:
			response.Adopted = true
			response.Body = row.Body
			response.ContentHash = row.ContentHash
		case errors.Is(err, storage.ErrACLSourceNotFound):
			response.Adopted = false
		default:
			writeJSON(w, http.StatusInternalServerError, aclErrorResponse{Error: "failed to read acl source"})
			return
		}
	}
	writeJSON(w, http.StatusOK, response)
}

// aclSourceAdoptRequest is the body of POST /api/v1/acls/source/adopt.
type aclSourceAdoptRequest struct {
	// Path is the ACL file MCM takes over. It defaults to the
	// broker-wide ACL file.
	Path string `json:"path,omitempty"`
	// Body is the verbatim content of the ACL file. It is optional when
	// the file exists on disk: MCM then reads it from there.
	Body string `json:"body,omitempty"`
}

// handleAdoptACLSource serves POST /api/v1/acls/source/adopt.
//
// Adopting an ACL file is how an operator brings an existing Mosquitto ACL
// file under MCM's management without losing a single line of it: the
// source is stored verbatim and the deploy composes the managed rules into
// it. The preview reports the access the composition would drop, and an
// apply that would drop access MCM can neither reproduce nor attribute
// refuses.
func (a *aclScopeAPI) handleAdoptSource(w http.ResponseWriter, r *http.Request) {
	if a.sources == nil {
		writeJSON(w, http.StatusServiceUnavailable, aclErrorResponse{Error: "acl sources are not available"})
		return
	}
	defer r.Body.Close() //nolint:errcheck

	var input aclSourceAdoptRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, aclErrorResponse{Error: "invalid request body"})
		return
	}
	path := strings.TrimSpace(input.Path)
	if path == "" {
		path = a.aclPath
	}
	if path == "" {
		writeJSON(w, http.StatusBadRequest, aclErrorResponse{Error: "acl path is not configured"})
		return
	}

	body := input.Body
	if body == "" {
		content, err := os.ReadFile(path)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, aclErrorResponse{
				Error:   "failed to read the acl file",
				Details: []string{err.Error()},
			})
			return
		}
		body = string(content)
	}

	row, err := a.sources.SaveACLSource(r.Context(), path, body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, aclErrorResponse{Error: "failed to adopt the acl source"})
		return
	}
	if a.audit != nil {
		a.audit(r, "acl.source_adopted", "acl_source", path, "success", map[string]any{
			"content_hash": row.ContentHash,
		})
	}
	writeJSON(w, http.StatusOK, aclSourceResponse{
		Path:        row.Path,
		Adopted:     true,
		Body:        row.Body,
		ContentHash: row.ContentHash,
	})
}

// aclIdentityView is the effective permission view of one identity.
type aclIdentityView struct {
	// Identity is the username of the client, or "anonymous".
	Identity string `json:"identity"`
	// Kind is "user" or "anonymous".
	Kind string `json:"kind"`
	// Grants are the grants that apply to the identity, with the verdict
	// the deny rules have on them.
	Grants []acl.EffectiveGrant `json:"grants"`
}

// aclScopeEffectiveView is the effective permission view of one ACL file.
type aclScopeEffectiveView struct {
	Scope      acl.Scope         `json:"scope"`
	ListenerID string            `json:"listener_id,omitempty"`
	Path       string            `json:"path"`
	Identities []aclIdentityView `json:"identities"`
}

// aclEffectiveResponse is the response of GET /api/v1/acls/effective.
type aclEffectiveResponse struct {
	Scopes []aclScopeEffectiveView `json:"scopes"`
}

// handleEffectivePermissions serves GET /api/v1/acls/effective.
//
// The view resolves the rules into the permissions an identity really has:
// the deny rules are applied before the grants, the anonymous section only
// counts for unauthenticated clients, and the pattern rules count for
// everyone.
func (a *aclScopeAPI) handleEffective(w http.ResponseWriter, r *http.Request) {
	rules, err := a.store.ListRules(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, aclErrorResponse{Error: "failed to list acl rules"})
		return
	}
	identities, err := a.identities(r.Context(), rules)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, aclErrorResponse{Error: "failed to list the identities"})
		return
	}

	views := make([]aclScopeEffectiveView, 0, 1)
	scoped := make(map[string][]acl.Rule)
	for _, rule := range rules {
		if rule.IsListenerScoped() {
			scoped[rule.ListenerID] = append(scoped[rule.ListenerID], rule)
		}
	}

	views = append(views, a.effectiveView(acl.ScopeGlobal, "", a.aclPath, rules, identities))

	listenerIDs := make([]string, 0, len(scoped))
	for id := range scoped {
		listenerIDs = append(listenerIDs, id)
	}
	sort.Strings(listenerIDs)
	for _, id := range listenerIDs {
		views = append(views, a.effectiveView(
			acl.ScopeListener,
			id,
			mosquitto.ListenerACLPath(a.aclPath, id),
			scoped[id],
			identities,
		))
	}

	writeJSON(w, http.StatusOK, aclEffectiveResponse{Scopes: views})
}

// effectiveView builds the effective permission view of one scope.
func (a *aclScopeAPI) effectiveView(scope acl.Scope, listenerID, path string, rules []acl.Rule, identities []acl.Identity) aclScopeEffectiveView {
	views := make([]aclIdentityView, 0, len(identities))
	for _, identity := range identities {
		kind := "user"
		if identity.IsAnonymous() {
			kind = "anonymous"
		}
		views = append(views, aclIdentityView{
			Identity: identity.Label(),
			Kind:     kind,
			Grants:   acl.EffectiveGrants(rules, identity),
		})
	}
	return aclScopeEffectiveView{
		Scope:      scope,
		ListenerID: listenerID,
		Path:       path,
		Identities: views,
	}
}

// identities returns the identities the effective view resolves.
//
// The list holds every enabled MQTT user plus the anonymous client, and it
// also holds the principals of the user-scoped rules: a rule whose user was
// removed is an orphan, and the operator has to see the access it still
// describes.
func (a *aclScopeAPI) identities(ctx context.Context, rules []acl.Rule) ([]acl.Identity, error) {
	seen := map[string]struct{}{}
	out := []acl.Identity{{Username: "", ClientID: ""}}
	if a.users != nil {
		users, err := a.users.ListMQTTUsers(ctx)
		if err != nil {
			return nil, fmt.Errorf("list mqtt users: %w", err)
		}
		for _, user := range users {
			if user.Disabled {
				continue
			}
			if _, duplicate := seen[user.Username]; duplicate {
				continue
			}
			seen[user.Username] = struct{}{}
			out = append(out, acl.Identity{Username: user.Username})
		}
	}
	for _, rule := range rules {
		if rule.EffectiveSelector() != acl.SelectorUser || rule.Principal == "" {
			continue
		}
		if _, duplicate := seen[rule.Principal]; duplicate {
			continue
		}
		seen[rule.Principal] = struct{}{}
		out = append(out, acl.Identity{Username: rule.Principal})
	}
	sort.SliceStable(out[1:], func(i, j int) bool {
		return out[1+i].Username < out[1+j].Username
	})
	return out, nil
}

// aclSimulateRequest is the body of POST /api/v1/acls/effective/simulate.
type aclSimulateRequest struct {
	// Scope selects the ACL file the question is about. It defaults to
	// the broker-wide file.
	Scope acl.Scope `json:"scope,omitempty"`
	// ListenerID names the listener whose file the question is about. It
	// is required for the listener scope.
	ListenerID string `json:"listener_id,omitempty"`
	// Username is the MQTT username of the client. An empty username
	// means an anonymous client.
	Username string `json:"username,omitempty"`
	// ClientID is the client id of the client. It is the value the %c
	// placeholder substitutes.
	ClientID string `json:"client_id,omitempty"`
	// Topic is the topic the client wants to access.
	Topic string `json:"topic"`
	// Access is the action the client wants to perform: "read" or
	// "write".
	Access acl.Access `json:"access"`
}

// handleSimulateAccess serves POST /api/v1/acls/effective/simulate.
//
// The simulator answers the question an operator asks when a rule looks
// wrong: "may this client do this on this topic?". It resolves the rules
// exactly like Mosquitto does — deny before grant, the anonymous section
// for unauthenticated clients only, and the pattern rules with the %u and
// %c placeholders substituted.
func (a *aclScopeAPI) handleSimulate(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close() //nolint:errcheck

	var input aclSimulateRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, aclErrorResponse{Error: "invalid request body"})
		return
	}
	if strings.TrimSpace(input.Topic) == "" {
		writeJSON(w, http.StatusBadRequest, aclErrorResponse{Error: "topic is required"})
		return
	}
	if !input.Access.IsValid() {
		writeJSON(w, http.StatusBadRequest, aclErrorResponse{Error: `access must be one of: "read", "write"`})
		return
	}

	rules, err := a.store.ListRules(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, aclErrorResponse{Error: "failed to list acl rules"})
		return
	}

	scoped := make([]acl.Rule, 0, len(rules))
	for _, rule := range rules {
		if rule.IsListenerScoped() {
			if input.Scope == acl.ScopeListener && rule.ListenerID == input.ListenerID {
				scoped = append(scoped, rule)
			}
			continue
		}
		if input.Scope != acl.ScopeListener {
			scoped = append(scoped, rule)
		}
	}
	if input.Scope == acl.ScopeListener && input.ListenerID == "" {
		writeJSON(w, http.StatusBadRequest, aclErrorResponse{Error: "listener_id is required for the listener scope"})
		return
	}

	identity := acl.Identity{Username: input.Username, ClientID: input.ClientID}
	decision := acl.EffectivePermission(scoped, identity, input.Topic, input.Access)
	writeJSON(w, http.StatusOK, decision)
}
