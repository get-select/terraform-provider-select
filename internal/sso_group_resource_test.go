// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"terraform-provider-select/internal/provider/resource_sso_group"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// cannedResponse is what a routedServer answers on one route.
type cannedResponse struct {
	status int
	body   string
}

// routedServer answers each request by its method and escaped path, without
// the query, and records every request in order. A route key with the suffix
// "#n" answers the n-th request to that route, and the key without a suffix
// answers the others. A request with no route answers 500 and fails the test.
type routedServer struct {
	*httptest.Server
	requests []recordedRequest
}

func newRoutedServer(t *testing.T, routes map[string]cannedResponse) *routedServer {
	t.Helper()
	s := &routedServer{}
	hits := map[string]int{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_, hasIfMatch := r.Header["If-Match"]
		s.requests = append(s.requests, recordedRequest{
			Method: r.Method, Path: r.URL.Path, EscapedPath: r.URL.EscapedPath(),
			IfMatch: r.Header.Get("If-Match"), HasIfMatch: hasIfMatch, Body: string(raw),
		})
		w.Header().Set("Content-Type", "application/json")
		key := r.Method + " " + r.URL.EscapedPath()
		hits[key]++
		route, ok := routes[fmt.Sprintf("%s#%d", key, hits[key])]
		if !ok {
			route, ok = routes[key]
		}
		if !ok {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.EscapedPath())
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(route.status)
		_, _ = w.Write([]byte(route.body))
	}))
	t.Cleanup(s.Close)
	return s
}

// calls lists the requests as "METHOD escaped-path".
func (s *routedServer) calls() []string {
	var calls []string
	for _, r := range s.requests {
		calls = append(calls, r.Method+" "+r.EscapedPath)
	}
	return calls
}

func groupJSON(name, etag string) string {
	return `{"id":"` + name + `","name":"` + name + `","create_time":"2026-10-01T00:00:00Z","update_time":"2026-10-02T00:00:00Z","etag":"` + etag + `"}`
}

func ssoGroupState(t *testing.T, name, etag string, roles ...ssoGroupRoleModel) ssoGroupModel {
	return ssoGroupModel{
		SsoGroupModel: resource_sso_group.SsoGroupModel{
			Id:         types.StringValue(name),
			Name:       types.StringValue(name),
			Etag:       types.StringValue(etag),
			CreateTime: types.StringValue("2026-10-01T00:00:00Z"),
			UpdateTime: types.StringValue("2026-10-01T00:00:00Z"),
		},
		Roles: ssoRolesSet(t, roles...),
	}
}

// ssoGroupPlan is the plan Terraform makes for a change: id follows name, and
// the values that change on every update are unknown.
func ssoGroupPlan(t *testing.T, name string, roles ...ssoGroupRoleModel) ssoGroupModel {
	model := ssoGroupState(t, name, "", roles...)
	model.Etag = types.StringUnknown()
	model.CreateTime = types.StringUnknown()
	model.UpdateTime = types.StringUnknown()
	return model
}

func newSsoGroupForTest(serverURL string) *ssoGroupResource {
	return &ssoGroupResource{client: NewAPIClient("key", "org", serverURL)}
}

func updateSsoGroup(t *testing.T, serverURL string, plan, state ssoGroupModel) (resource.UpdateResponse, ssoGroupModel) {
	t.Helper()
	r := newSsoGroupForTest(serverURL)
	s := ssoGroupResourceSchema(context.Background())
	// The framework fills the response with the prior state before it calls
	// Update.
	resp := resource.UpdateResponse{State: v2State(t, s, &state)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: tfPlan(t, s, &plan), State: v2State(t, s, &state)}, &resp)
	var got ssoGroupModel
	resp.State.Get(context.Background(), &got)
	return resp, got
}

func readSsoGroup(t *testing.T, serverURL string, state ssoGroupModel) (resource.ReadResponse, ssoGroupModel) {
	t.Helper()
	r := newSsoGroupForTest(serverURL)
	s := ssoGroupResourceSchema(context.Background())
	resp := resource.ReadResponse{State: v2State(t, s, &state)}
	r.Read(context.Background(), resource.ReadRequest{State: v2State(t, s, &state)}, &resp)
	var got ssoGroupModel
	if !resp.State.Raw.IsNull() {
		resp.State.Get(context.Background(), &got)
	}
	return resp, got
}

func TestSsoGroupCreateSendsTheNameAndEveryRole(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"POST /v2/sso-groups": {http.StatusCreated, groupJSON("Data Team", "e-1")},
	})
	plan := ssoGroupPlan(t, "Data Team",
		ssoRole("admin", nullScope()),
		ssoRole("viewer", scopeObject(t, "usage_group", types.StringValue("ug-1"))),
	)

	r := newSsoGroupForTest(server.URL)
	s := ssoGroupResourceSchema(context.Background())
	resp := resource.CreateResponse{State: v2State[ssoGroupModel](t, s, nil)}
	r.Create(context.Background(), resource.CreateRequest{Plan: tfPlan(t, s, &plan)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	if len(server.requests) != 1 {
		t.Fatalf("create should be one request, got %v", server.calls())
	}
	var body ssoGroupCreatePayload
	if err := json.Unmarshal([]byte(server.requests[0].Body), &body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if body.Name != "Data Team" || len(body.Roles) != 2 {
		t.Errorf("the create should carry the name and both roles, got %s", server.requests[0].Body)
	}

	var got ssoGroupModel
	resp.State.Get(context.Background(), &got)
	if got.Id.ValueString() != "Data Team" || got.Etag.ValueString() != "e-1" || !got.Roles.Equal(plan.Roles) {
		t.Errorf("state should hold the response's id and etag, and the planned roles, got %+v", got)
	}
}

func TestSsoGroupCreateConflictSuggestsImport(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"POST /v2/sso-groups": {http.StatusConflict, `{"detail":"exists"}`},
	})
	plan := ssoGroupPlan(t, "Data Team", ssoRole("admin", nullScope()))

	r := newSsoGroupForTest(server.URL)
	s := ssoGroupResourceSchema(context.Background())
	resp := resource.CreateResponse{State: v2State[ssoGroupModel](t, s, nil)}
	r.Create(context.Background(), resource.CreateRequest{Plan: tfPlan(t, s, &plan)}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("a 409 should fail the create")
	}
	if d := resp.Diagnostics[0].Detail(); !strings.Contains(d, "terraform import select_sso_group.<name> <group name>") {
		t.Errorf("the conflict should explain how to import the group, got:\n%s", d)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("a failed create should leave no state")
	}
}

// One apply renames the group, adds a role and removes a role. The rename
// comes first and uses If-Match. The new role is granted before the old one
// is revoked, so the group never holds zero roles. Every request after the
// rename uses the new, escaped name.
func TestSsoGroupUpdateRenamesThenGrantsThenRevokes(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"PATCH /v2/sso-groups/old%20name":              {http.StatusOK, groupJSON("eng/data", "e-2")},
		"POST /v2/sso-groups/eng%2Fdata/roles":         {http.StatusCreated, `{"id":"g-new","role":"monitor_editor","entity":{"type":"select_organization","id":"org","display_name":"Org"},"create_time":"x"}`},
		"GET /v2/sso-groups/eng%2Fdata/roles#1":        {http.StatusOK, `{"items":[` + orgGrant + `,` + groupGrant + `,` + monitorGrant + `]}`},
		"GET /v2/sso-groups/eng%2Fdata/roles#2":        {http.StatusOK, `{"items":[` + groupGrant + `,` + monitorGrant + `]}`},
		"DELETE /v2/sso-groups/eng%2Fdata/roles/g-org": {http.StatusNoContent, ""},
		"GET /v2/sso-groups/eng%2Fdata":                {http.StatusOK, groupJSON("eng/data", "e-3")},
	})
	usageGroup := scopeObject(t, "usage_group", types.StringValue("ug-1"))
	state := ssoGroupState(t, "old name", "e-1", ssoRole("admin", nullScope()), ssoRole("viewer", usageGroup))
	plan := ssoGroupPlan(t, "eng/data", ssoRole("viewer", usageGroup), ssoRole("monitor_editor", nullScope()))

	resp, got := updateSsoGroup(t, server.URL, plan, state)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	want := []string{
		"PATCH /v2/sso-groups/old%20name",
		"POST /v2/sso-groups/eng%2Fdata/roles",
		"GET /v2/sso-groups/eng%2Fdata/roles",
		"DELETE /v2/sso-groups/eng%2Fdata/roles/g-org",
		"GET /v2/sso-groups/eng%2Fdata",
		"GET /v2/sso-groups/eng%2Fdata/roles",
	}
	if strings.Join(server.calls(), "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests out of order\n got: %v\nwant: %v", server.calls(), want)
	}
	if rename := server.requests[0]; rename.IfMatch != "e-1" || rename.Body != `{"name":"eng/data"}` {
		t.Errorf("the rename should send the new name with If-Match from state, got If-Match %q body %s", rename.IfMatch, rename.Body)
	}
	if grant := server.requests[1]; grant.HasIfMatch || grant.Body != `{"role":"monitor_editor"}` {
		t.Errorf("a grant has no ETag and should send only the new role, got If-Match %q body %s", grant.IfMatch, grant.Body)
	}
	if revoke := server.requests[3]; revoke.HasIfMatch {
		t.Errorf("a grant has no ETag, so the revoke should send no If-Match, got %q", revoke.IfMatch)
	}

	if got.Id.ValueString() != "eng/data" || got.Name.ValueString() != "eng/data" {
		t.Errorf("state should hold the new id and name, got id=%v name=%v", got.Id, got.Name)
	}
	if got.Etag.ValueString() != "e-3" {
		t.Errorf("state should hold the ETag read after the role changes, got %v", got.Etag)
	}
	if !got.Roles.Equal(plan.Roles) {
		t.Errorf("state should hold the planned roles, got %v", got.Roles)
	}
	if len(resp.Diagnostics) != 0 {
		t.Errorf("the final list matches the plan, so there should be no warning, got %v", resp.Diagnostics)
	}
}

// The final list finds a role that changed outside Terraform during the
// apply. State keeps the plan, which Terraform requires, and a warning says
// that the next plan shows the difference.
func TestSsoGroupUpdateWarnsWhenRolesChangedDuringTheApply(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"POST /v2/sso-groups/g/roles": {http.StatusCreated, monitorGrant},
		"GET /v2/sso-groups/g":        {http.StatusOK, groupJSON("g", "e-2")},
		// The organization admin grant was revoked outside Terraform.
		"GET /v2/sso-groups/g/roles": {http.StatusOK, `{"items":[` + monitorGrant + `]}`},
	})
	state := ssoGroupState(t, "g", "e-1", ssoRole("admin", nullScope()))
	plan := ssoGroupPlan(t, "g", ssoRole("admin", nullScope()), ssoRole("monitor_editor", nullScope()))

	resp, got := updateSsoGroup(t, server.URL, plan, state)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error: %v", resp.Diagnostics)
	}
	if resp.Diagnostics.WarningsCount() != 1 || resp.Diagnostics[0].Summary() != "SSO Group Roles Changed During Apply" {
		t.Errorf("expected a warning about the changed roles, got %v", resp.Diagnostics)
	}
	if !got.Roles.Equal(plan.Roles) || got.Etag.ValueString() != "e-2" {
		t.Errorf("state should hold the plan and the new ETag, got %+v", got)
	}
}

// A rename that fails with a server error, or whose response cannot be read,
// can still have happened. State moves to the new URL only when the old URL
// is gone and the new URL holds a group.
func TestSsoGroupUpdateFindsARenameWhoseResponseFailed(t *testing.T) {
	state := ssoGroupState(t, "old", "e-1", ssoRole("admin", nullScope()))
	plan := ssoGroupPlan(t, "new", ssoRole("admin", nullScope()))
	notFound := cannedResponse{http.StatusNotFound, `{"detail":"not found"}`}

	cases := []struct {
		name   string
		rename cannedResponse
		oldGet cannedResponse
		newGet cannedResponse
		wantId string
	}{
		{"server error, renamed", cannedResponse{http.StatusBadGateway, `{"detail":"upstream"}`}, notFound, cannedResponse{http.StatusOK, groupJSON("new", "e-2")}, "new"},
		{"unreadable response, renamed", cannedResponse{http.StatusOK, `not json`}, notFound, cannedResponse{http.StatusOK, groupJSON("new", "e-2")}, "new"},
		{"server error, not renamed", cannedResponse{http.StatusBadGateway, `{"detail":"upstream"}`}, cannedResponse{http.StatusOK, groupJSON("old", "e-1")}, notFound, "old"},
		// Another group already has the new name, and the old group is still
		// there. State must not move to the other group.
		{"server error, both names exist", cannedResponse{http.StatusBadGateway, `{"detail":"upstream"}`}, cannedResponse{http.StatusOK, groupJSON("old", "e-1")}, cannedResponse{http.StatusOK, groupJSON("new", "e-9")}, "old"},
	}
	for _, c := range cases {
		server := newRoutedServer(t, map[string]cannedResponse{
			"PATCH /v2/sso-groups/old": c.rename,
			"GET /v2/sso-groups/old":   c.oldGet,
			"GET /v2/sso-groups/new":   c.newGet,
		})
		resp, got := updateSsoGroup(t, server.URL, plan, state)
		if !resp.Diagnostics.HasError() {
			t.Errorf("%s: the failed rename should still be an error", c.name)
		}
		if got.Id.ValueString() != c.wantId || !got.Roles.Equal(state.Roles) {
			t.Errorf("%s: state id = %v, want %s", c.name, got.Id, c.wantId)
		}
		if c.wantId == "new" && got.Etag.ValueString() != "e-2" {
			t.Errorf("%s: state should take the renamed group's ETag, got %v", c.name, got.Etag)
		}
	}

	// A 4xx is a definite refusal, so nothing is looked up.
	server := newRoutedServer(t, map[string]cannedResponse{
		"PATCH /v2/sso-groups/old": {http.StatusConflict, `{"detail":"taken"}`},
	})
	if _, got := updateSsoGroup(t, server.URL, plan, state); len(server.requests) != 1 || got.Id.ValueString() != "old" {
		t.Errorf("a 409 should send nothing more and keep the old URL, got %v", server.calls())
	}
}

// A scope id that changes only in case is the same grant. The update sends no
// grant and no revoke, and state takes the planned case.
func TestSsoGroupUpdateCaseOnlyChangeSendsNoGrant(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"GET /v2/sso-groups/g":       {http.StatusOK, groupJSON("g", "e-2")},
		"GET /v2/sso-groups/g/roles": {http.StatusOK, `{"items":[{"id":"g-1","role":"viewer","snowflake_account_uuid":"abc-123","entity":{"type":"snowflake_account","id":"abc-123","display_name":"Account"},"create_time":"x"}]}`},
	})
	state := ssoGroupState(t, "g", "e-1", ssoRole("viewer", scopeObject(t, "snowflake_account", types.StringValue("abc-123"))))
	plan := ssoGroupPlan(t, "g", ssoRole("viewer", scopeObject(t, "snowflake_account", types.StringValue("ABC-123"))))

	resp, got := updateSsoGroup(t, server.URL, plan, state)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	for _, call := range server.calls() {
		if !strings.HasPrefix(call, "GET ") {
			t.Errorf("a case-only change should send no grant and no revoke, got %v", server.calls())
		}
	}
	if len(resp.Diagnostics) != 0 {
		t.Errorf("the listed grant is the planned one in another case, so there should be no warning, got %v", resp.Diagnostics)
	}
	if !got.Roles.Equal(plan.Roles) {
		t.Errorf("state should take the planned case, got %v", got.Roles)
	}
}

// After the rename, state points at the new URL even when a later step
// fails. Otherwise the next Read would get a 404 on the old URL, drop the
// group from state and leave it in SELECT.
func TestSsoGroupUpdateKeepsTheRenameWhenAGrantFails(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"PATCH /v2/sso-groups/old":      {http.StatusOK, groupJSON("new", "e-2")},
		"POST /v2/sso-groups/new/roles": {http.StatusUnprocessableEntity, `{"detail":"bad scope"}`},
	})
	state := ssoGroupState(t, "old", "e-1", ssoRole("admin", nullScope()))
	plan := ssoGroupPlan(t, "new", ssoRole("viewer", scopeObject(t, "usage_group", types.StringValue("ug-9"))))

	resp, got := updateSsoGroup(t, server.URL, plan, state)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), "grant a role to the SSO group") {
		t.Fatalf("the failed grant should be an error, got %v", resp.Diagnostics)
	}
	if calls := server.calls(); len(calls) != 2 {
		t.Errorf("no revoke should follow a failed grant, got %v", calls)
	}
	if got.Id.ValueString() != "new" || got.Name.ValueString() != "new" || got.Etag.ValueString() != "e-2" {
		t.Errorf("state should hold the renamed group, got id=%v name=%v etag=%v", got.Id, got.Name, got.Etag)
	}
	if !got.Roles.Equal(state.Roles) {
		t.Errorf("state should hold the roles SELECT still has, got %v", got.Roles)
	}
}

// A revoke that SELECT refuses after a grant succeeded leaves state with both
// roles, which is what SELECT holds. A 4xx is a definite refusal, so the
// tracked progress is saved without a new list.
func TestSsoGroupUpdateRecordsTheGrantWhenARevokeFails(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"POST /v2/sso-groups/g/roles":         {http.StatusCreated, editorGrant},
		"GET /v2/sso-groups/g/roles":          {http.StatusOK, `{"items":[` + orgGrant + `,` + editorGrant + `]}`},
		"DELETE /v2/sso-groups/g/roles/g-org": {http.StatusConflict, `{"detail":"last role"}`},
	})
	state := ssoGroupState(t, "g", "e-1", ssoRole("admin", nullScope()))
	plan := ssoGroupPlan(t, "g", ssoRole("editor", nullScope()))

	resp, got := updateSsoGroup(t, server.URL, plan, state)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), "revoke a role from the SSO group") {
		t.Fatalf("the failed revoke should be an error, got %v", resp.Diagnostics)
	}
	if calls := server.calls(); calls[0] != "POST /v2/sso-groups/g/roles" {
		t.Errorf("the grant should come before the revoke, got %v", calls)
	}
	want := ssoRolesSet(t, ssoRole("admin", nullScope()), ssoRole("editor", nullScope()))
	if !got.Roles.Equal(want) {
		t.Errorf("state should hold both roles, got %v", got.Roles)
	}
	if got.Etag.ValueString() != "e-1" {
		t.Errorf("no step read a new ETag, so state keeps the old one for Read to refresh, got %v", got.Etag)
	}
	if n := len(server.requests); n != 3 {
		t.Errorf("a definite refusal should not list the roles again, got %v", server.calls())
	}
}

// A grant that SELECT made but answered with a 500. The provider lists the
// grants again, and state holds what SELECT lists, not what it assumed.
func TestSsoGroupUpdateRelistsAfterAnAmbiguousGrant(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"POST /v2/sso-groups/g/roles": {http.StatusInternalServerError, `{"detail":"boom"}`},
		"GET /v2/sso-groups/g/roles":  {http.StatusOK, `{"items":[` + orgGrant + `,` + editorGrant + `]}`},
	})
	state := ssoGroupState(t, "g", "e-1", ssoRole("admin", nullScope()))
	plan := ssoGroupPlan(t, "g", ssoRole("editor", scopeObject(t, "organization", types.StringNull())))

	resp, got := updateSsoGroup(t, server.URL, plan, state)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), "grant a role to the SSO group") {
		t.Fatalf("the failed grant should be an error, got %v", resp.Diagnostics)
	}
	// The listed editor grant pairs with the planned element, so it keeps
	// the planned explicit organization scope.
	want := ssoRolesSet(t, ssoRole("admin", nullScope()), ssoRole("editor", scopeObject(t, "organization", types.StringNull())))
	if !got.Roles.Equal(want) {
		t.Errorf("state should hold the grants SELECT lists\n got: %v\nwant: %v", got.Roles, want)
	}

	// The grant did not happen after all.
	server = newRoutedServer(t, map[string]cannedResponse{
		"POST /v2/sso-groups/g/roles": {http.StatusInternalServerError, `{"detail":"boom"}`},
		"GET /v2/sso-groups/g/roles":  {http.StatusOK, `{"items":[` + orgGrant + `]}`},
	})
	_, got = updateSsoGroup(t, server.URL, plan, state)
	if !got.Roles.Equal(state.Roles) {
		t.Errorf("state should hold only the grant SELECT lists, got %v", got.Roles)
	}

	// The list fails too: the tracked progress stays, which is the state
	// before the grant.
	server = newRoutedServer(t, map[string]cannedResponse{
		"POST /v2/sso-groups/g/roles": {http.StatusInternalServerError, `{"detail":"boom"}`},
		"GET /v2/sso-groups/g/roles":  {http.StatusServiceUnavailable, `{"detail":"down"}`},
	})
	_, got = updateSsoGroup(t, server.URL, plan, state)
	if !got.Roles.Equal(state.Roles) {
		t.Errorf("state should keep the tracked roles when the list fails, got %v", got.Roles)
	}
}

// A revoke that SELECT made but answered with a 500. State holds what SELECT
// lists after it: the revoked role is gone.
func TestSsoGroupUpdateRelistsAfterAnAmbiguousRevoke(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"POST /v2/sso-groups/g/roles":         {http.StatusCreated, editorGrant},
		"GET /v2/sso-groups/g/roles#1":        {http.StatusOK, `{"items":[` + orgGrant + `,` + editorGrant + `]}`},
		"DELETE /v2/sso-groups/g/roles/g-org": {http.StatusInternalServerError, `{"detail":"boom"}`},
		"GET /v2/sso-groups/g/roles#2":        {http.StatusOK, `{"items":[` + editorGrant + `]}`},
	})
	state := ssoGroupState(t, "g", "e-1", ssoRole("admin", nullScope()))
	plan := ssoGroupPlan(t, "g", ssoRole("editor", nullScope()))

	resp, got := updateSsoGroup(t, server.URL, plan, state)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), "revoke a role from the SSO group") {
		t.Fatalf("the failed revoke should be an error, got %v", resp.Diagnostics)
	}
	want := []string{
		"POST /v2/sso-groups/g/roles",
		"GET /v2/sso-groups/g/roles",
		"DELETE /v2/sso-groups/g/roles/g-org",
		"GET /v2/sso-groups/g/roles",
	}
	if strings.Join(server.calls(), "\n") != strings.Join(want, "\n") {
		t.Errorf("requests\n got: %v\nwant: %v", server.calls(), want)
	}
	if !got.Roles.Equal(plan.Roles) {
		t.Errorf("state should hold only the grant SELECT lists, got %v", got.Roles)
	}
}

// A 412 on the rename or the delete. When SELECT still holds the state's name
// and roles, the ETag changed only because of something this resource does
// not manage, such as a team membership that the same apply replaced. The
// write is then sent once more with the fresh ETag. When the name or the roles
// changed, the 412 reaches the user and nothing is retried.
func TestSsoGroupRetriesA412OnlyWhenNothingManagedChanged(t *testing.T) {
	stale := cannedResponse{http.StatusPreconditionFailed, `{"detail":"The If-Match header does not match the resource's current ETag."}`}
	sameRoles := cannedResponse{http.StatusOK, `{"items":[` + teamGrant + `,` + orgGrant + `,` + groupGrant + `]}`}
	otherRoles := cannedResponse{http.StatusOK, `{"items":[` + orgGrant + `,` + groupGrant + `,` + editorGrant + `]}`}
	// The state's scope id is in upper case. SELECT lists it in lower case,
	// which is the same grant.
	state := ssoGroupState(t, "old", "e-1",
		ssoRole("admin", nullScope()),
		ssoRole("viewer", scopeObject(t, "usage_group", types.StringValue("UG-1"))),
	)

	type outcome struct {
		wantErr   bool
		wantCalls []string
	}
	retried := func(write string) outcome {
		return outcome{false, []string{write, "GET /v2/sso-groups/old", "GET /v2/sso-groups/old/roles", write}}
	}
	refused := func(write string) outcome {
		return outcome{true, []string{write, "GET /v2/sso-groups/old", "GET /v2/sso-groups/old/roles"}}
	}

	cases := []struct {
		name   string
		write  string
		routes map[string]cannedResponse
		want   outcome
	}{
		{
			name:  "rename, nothing managed changed",
			write: "PATCH /v2/sso-groups/old",
			routes: map[string]cannedResponse{
				"PATCH /v2/sso-groups/old#1":   stale,
				"PATCH /v2/sso-groups/old#2":   {http.StatusOK, groupJSON("new", "e-3")},
				"GET /v2/sso-groups/old":       {http.StatusOK, groupJSON("old", "e-2")},
				"GET /v2/sso-groups/old/roles": sameRoles,
				"GET /v2/sso-groups/new":       {http.StatusOK, groupJSON("new", "e-4")},
				"GET /v2/sso-groups/new/roles": sameRoles,
			},
			want: outcome{false, []string{
				"PATCH /v2/sso-groups/old", "GET /v2/sso-groups/old", "GET /v2/sso-groups/old/roles",
				"PATCH /v2/sso-groups/old", "GET /v2/sso-groups/new", "GET /v2/sso-groups/new/roles",
			}},
		},
		{
			name:  "rename, roles changed",
			write: "PATCH /v2/sso-groups/old",
			routes: map[string]cannedResponse{
				"PATCH /v2/sso-groups/old":     stale,
				"GET /v2/sso-groups/old":       {http.StatusOK, groupJSON("old", "e-2")},
				"GET /v2/sso-groups/old/roles": otherRoles,
			},
			want: refused("PATCH /v2/sso-groups/old"),
		},
		{
			name:  "rename, the retry gets a second 412",
			write: "PATCH /v2/sso-groups/old",
			routes: map[string]cannedResponse{
				"PATCH /v2/sso-groups/old":     stale,
				"GET /v2/sso-groups/old":       {http.StatusOK, groupJSON("old", "e-2")},
				"GET /v2/sso-groups/old/roles": sameRoles,
			},
			want: outcome{true, retried("PATCH /v2/sso-groups/old").wantCalls},
		},
		{
			name:  "delete, nothing managed changed",
			write: "DELETE /v2/sso-groups/old",
			routes: map[string]cannedResponse{
				"DELETE /v2/sso-groups/old#1":  stale,
				"DELETE /v2/sso-groups/old#2":  {http.StatusNoContent, ""},
				"GET /v2/sso-groups/old":       {http.StatusOK, groupJSON("old", "e-2")},
				"GET /v2/sso-groups/old/roles": sameRoles,
			},
			want: retried("DELETE /v2/sso-groups/old"),
		},
		{
			name:  "delete, roles changed",
			write: "DELETE /v2/sso-groups/old",
			routes: map[string]cannedResponse{
				"DELETE /v2/sso-groups/old":    stale,
				"GET /v2/sso-groups/old":       {http.StatusOK, groupJSON("old", "e-2")},
				"GET /v2/sso-groups/old/roles": otherRoles,
			},
			want: refused("DELETE /v2/sso-groups/old"),
		},
		{
			name:  "delete, the retry gets a second 412",
			write: "DELETE /v2/sso-groups/old",
			routes: map[string]cannedResponse{
				"DELETE /v2/sso-groups/old":    stale,
				"GET /v2/sso-groups/old":       {http.StatusOK, groupJSON("old", "e-2")},
				"GET /v2/sso-groups/old/roles": sameRoles,
			},
			want: outcome{true, retried("DELETE /v2/sso-groups/old").wantCalls},
		},
	}

	for _, c := range cases {
		server := newRoutedServer(t, c.routes)
		var diags diag.Diagnostics
		var got ssoGroupModel
		if strings.HasPrefix(c.write, "PATCH") {
			plan := ssoGroupPlan(t, "new",
				ssoRole("admin", nullScope()),
				ssoRole("viewer", scopeObject(t, "usage_group", types.StringValue("UG-1"))),
			)
			var resp resource.UpdateResponse
			resp, got = updateSsoGroup(t, server.URL, plan, state)
			diags = resp.Diagnostics
		} else {
			r := newSsoGroupForTest(server.URL)
			s := ssoGroupResourceSchema(context.Background())
			resp := resource.DeleteResponse{State: v2State(t, s, &state)}
			r.Delete(context.Background(), resource.DeleteRequest{State: v2State(t, s, &state)}, &resp)
			diags = resp.Diagnostics
		}

		if diags.HasError() != c.want.wantErr {
			t.Errorf("%s: error = %v, want %v (%v)", c.name, diags.HasError(), c.want.wantErr, diags)
		}
		if c.want.wantErr && diags[0].Summary() != "SSO Group Changed Outside Terraform" {
			t.Errorf("%s: the 412 should say the group changed outside Terraform, got %v", c.name, diags)
		}
		if strings.Join(server.calls(), "\n") != strings.Join(c.want.wantCalls, "\n") {
			t.Errorf("%s: requests\n got: %v\nwant: %v", c.name, server.calls(), c.want.wantCalls)
		}
		for i, r := range server.requests {
			if r.Method == http.MethodPatch || r.Method == http.MethodDelete {
				want := "e-1"
				if i > 0 {
					want = "e-2"
				}
				if r.IfMatch != want {
					t.Errorf("%s: request %d sent If-Match %q, want %q", c.name, i, r.IfMatch, want)
				}
			}
		}
		if strings.HasPrefix(c.write, "PATCH") {
			wantId := "old"
			if !c.want.wantErr {
				wantId = "new"
			}
			if got.Id.ValueString() != wantId {
				t.Errorf("%s: state id = %v, want %s", c.name, got.Id, wantId)
			}
		}
	}
}

// The check behind the retry, on its own: the name and the managed roles must
// both be what state holds.
func TestSsoGroupUnchangedSince(t *testing.T) {
	ctx := context.Background()
	state := ssoGroupState(t, "g", "e-1", ssoRole("admin", nullScope()), ssoRole("viewer", scopeObject(t, "usage_group", types.StringValue("UG-1"))))
	stateRoles, _ := ssoGroupRolesFromSet(ctx, state.Roles)
	group := &ssoGroupResponse{Id: "g", Name: "g", Etag: "e-2"}
	same := ssoGroupManagedGrants(ssoGrants(t, `[`+teamGrant+`,`+orgGrant+`,`+groupGrant+`]`))

	if !ssoGroupUnchangedSince(ctx, &state, stateRoles, group, same) {
		t.Error("the same name and roles, with a team membership added, should count as unchanged")
	}
	if ssoGroupUnchangedSince(ctx, &state, stateRoles, &ssoGroupResponse{Id: "h", Name: "h"}, same) {
		t.Error("another name is a change")
	}
	if ssoGroupUnchangedSince(ctx, &state, stateRoles, group, ssoGrants(t, `[`+orgGrant+`]`)) {
		t.Error("a revoked role is a change")
	}
	if ssoGroupUnchangedSince(ctx, &state, stateRoles, group, ssoGrants(t, `[`+orgGrant+`,`+groupGrant+`,`+editorGrant+`]`)) {
		t.Error("an added role is a change")
	}
	if ssoGroupUnchangedSince(ctx, &state, stateRoles, group, ssoGrants(t, `[`+orgGrant+`,`+groupGrant+`,`+groupGrant+`]`)) {
		t.Error("a duplicate grant is a change")
	}
}

// Read gets the group, then lists its roles. The set is built from the list,
// paired with the state's elements.
func TestSsoGroupReadListsTheRoles(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"GET /v2/sso-groups/data%20team":       {http.StatusOK, groupJSON("data team", "e-5")},
		"GET /v2/sso-groups/data%20team/roles": {http.StatusOK, `{"items":[` + orgGrant + `,` + groupGrant + `]}`},
	})
	state := ssoGroupState(t, "data team", "e-1",
		ssoRole("admin", nullScope()),
		ssoRole("viewer", scopeObject(t, "usage_group", types.StringValue("UG-1"))),
	)

	resp, got := readSsoGroup(t, server.URL, state)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if got.Etag.ValueString() != "e-5" || got.UpdateTime.ValueString() != "2026-10-02T00:00:00Z" {
		t.Errorf("Read should refresh the group's fields, got %+v", got.SsoGroupModel)
	}
	if !got.Roles.Equal(state.Roles) {
		t.Errorf("Read should keep the configured roles when SELECT holds the same grants\n got: %v\nwant: %v", got.Roles, state.Roles)
	}

	// A role granted outside Terraform shows as drift.
	server = newRoutedServer(t, map[string]cannedResponse{
		"GET /v2/sso-groups/data%20team":       {http.StatusOK, groupJSON("data team", "e-6")},
		"GET /v2/sso-groups/data%20team/roles": {http.StatusOK, `{"items":[` + orgGrant + `,` + groupGrant + `,{"id":"g-x","role":"editor","entity":{"type":"select_organization","id":"org","display_name":"Org"},"create_time":"x"}]}`},
	})
	_, got = readSsoGroup(t, server.URL, state)
	if n := len(got.Roles.Elements()); n != 3 {
		t.Errorf("a grant made outside Terraform should be in the set, got %d roles", n)
	}
}

func TestSsoGroupReadRemovesAGroupThatIsGone(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"GET /v2/sso-groups/g": {http.StatusNotFound, `{"detail":"not found"}`},
	})
	resp, _ := readSsoGroup(t, server.URL, ssoGroupState(t, "g", "e-1", ssoRole("admin", nullScope())))
	if resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
		t.Errorf("a 404 should remove the group from state, got %v", resp.Diagnostics)
	}

	// The group can be deleted between the two requests.
	server = newRoutedServer(t, map[string]cannedResponse{
		"GET /v2/sso-groups/g":       {http.StatusOK, groupJSON("g", "e-1")},
		"GET /v2/sso-groups/g/roles": {http.StatusNotFound, `{"detail":"not found"}`},
	})
	resp, _ = readSsoGroup(t, server.URL, ssoGroupState(t, "g", "e-1", ssoRole("admin", nullScope())))
	if resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
		t.Errorf("a 404 on the roles list should remove the group from state, got %v", resp.Diagnostics)
	}
}

// Import takes the group name, which is the id. Read then fills in the name
// and the roles.
func TestImportSsoGroupByName(t *testing.T) {
	r := newSsoGroupForTest("")
	s := ssoGroupResourceSchema(context.Background())
	imported := resource.ImportStateResponse{State: v2State[ssoGroupModel](t, s, nil)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "eng/data team"}, &imported)
	if imported.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", imported.Diagnostics)
	}
	var state ssoGroupModel
	imported.State.Get(context.Background(), &state)
	if state.Id.ValueString() != "eng/data team" {
		t.Fatalf("import should write the name to id, got %v", state.Id)
	}

	server := newRoutedServer(t, map[string]cannedResponse{
		"GET /v2/sso-groups/eng%2Fdata%20team":       {http.StatusOK, groupJSON("eng/data team", "e-1")},
		"GET /v2/sso-groups/eng%2Fdata%20team/roles": {http.StatusOK, `{"items":[` + orgGrant + `,` + groupGrant + `]}`},
	})
	resp, got := readSsoGroup(t, server.URL, state)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	want := ssoRolesSet(t, ssoRole("admin", nullScope()), ssoRole("viewer", scopeObject(t, "usage_group", types.StringValue("ug-1"))))
	if got.Name.ValueString() != "eng/data team" || !got.Roles.Equal(want) {
		t.Errorf("Read after import should fill in the name and the roles, got name=%v roles=%v", got.Name, got.Roles)
	}
}

// Delete sends If-Match with the state's ETag, on the escaped name. A group
// that is already gone is not an error.
func TestSsoGroupDelete(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotFound} {
		server := newRoutedServer(t, map[string]cannedResponse{
			"DELETE /v2/sso-groups/a%2Fb": {status, ""},
		})
		state := ssoGroupState(t, "a/b", "e-1", ssoRole("admin", nullScope()))
		r := newSsoGroupForTest(server.URL)
		s := ssoGroupResourceSchema(context.Background())
		resp := resource.DeleteResponse{State: v2State(t, s, &state)}
		r.Delete(context.Background(), resource.DeleteRequest{State: v2State(t, s, &state)}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("status %d: unexpected diagnostics: %v", status, resp.Diagnostics)
		}
		if len(server.requests) != 1 || server.requests[0].IfMatch != "e-1" {
			t.Errorf("status %d: delete should be one request with If-Match, got %+v", status, server.requests)
		}
	}
}

// id is planned as the planned name, so a rename shows the new id in the
// plan.
func TestSsoGroupIdFromName(t *testing.T) {
	s := ssoGroupResourceSchema(context.Background())
	plan := ssoGroupPlan(t, "new", ssoRole("admin", nullScope()))
	plan.Id = types.StringUnknown()

	req := planmodifier.StringRequest{Path: path.Root("id"), Plan: tfPlan(t, s, &plan), PlanValue: types.StringUnknown()}
	resp := planmodifier.StringResponse{PlanValue: req.PlanValue}
	ssoGroupIdFromName{}.PlanModifyString(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() || resp.PlanValue.ValueString() != "new" {
		t.Errorf("id should be planned as the name, got %v %v", resp.PlanValue, resp.Diagnostics)
	}

	plan.Name = types.StringUnknown()
	req.Plan = tfPlan(t, s, &plan)
	resp = planmodifier.StringResponse{PlanValue: types.StringUnknown()}
	ssoGroupIdFromName{}.PlanModifyString(context.Background(), req, &resp)
	if !resp.PlanValue.IsUnknown() {
		t.Errorf("an unknown name should leave id unknown, got %v", resp.PlanValue)
	}
}

// teamGrant is how the roles list shows the group's membership of a team.
const teamGrant = `{"id":"g-team","role":"viewer","entity":{"type":"team","id":"t-1","display_name":"Analysts"},"create_time":"x"}`

// The roles list also holds the group's team memberships, as grants on a
// team. select_team_member manages those, so Read does not show them and an
// update never revokes them.
func TestSsoGroupIgnoresTeamMembershipGrants(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"GET /v2/sso-groups/g":       {http.StatusOK, groupJSON("g", "e-1")},
		"GET /v2/sso-groups/g/roles": {http.StatusOK, `{"items":[` + teamGrant + `,` + orgGrant + `,` + groupGrant + `,` + teamGrant + `]}`},
	})
	state := ssoGroupState(t, "g", "e-1",
		ssoRole("admin", nullScope()),
		ssoRole("viewer", scopeObject(t, "usage_group", types.StringValue("ug-1"))),
	)
	resp, got := readSsoGroup(t, server.URL, state)
	if len(resp.Diagnostics) != 0 {
		t.Fatalf("a team membership should be neither an error nor a duplicate warning, got %v", resp.Diagnostics)
	}
	if !got.Roles.Equal(state.Roles) {
		t.Errorf("Read should give only the managed roles\n got: %v\nwant: %v", got.Roles, state.Roles)
	}

	// The update removes the viewer role, which is on the same role as the
	// team membership. Only the usage group grant is revoked, and the team
	// membership is not drift after the update.
	server = newRoutedServer(t, map[string]cannedResponse{
		"GET /v2/sso-groups/g/roles#1":       {http.StatusOK, `{"items":[` + teamGrant + `,` + orgGrant + `,` + groupGrant + `]}`},
		"DELETE /v2/sso-groups/g/roles/g-ug": {http.StatusNoContent, ""},
		"GET /v2/sso-groups/g":               {http.StatusOK, groupJSON("g", "e-2")},
		"GET /v2/sso-groups/g/roles#2":       {http.StatusOK, `{"items":[` + teamGrant + `,` + orgGrant + `]}`},
	})
	plan := ssoGroupPlan(t, "g", ssoRole("admin", nullScope()))
	updated, got := updateSsoGroup(t, server.URL, plan, state)
	if len(updated.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", updated.Diagnostics)
	}
	for _, call := range server.calls() {
		if call == "DELETE /v2/sso-groups/g/roles/g-team" {
			t.Error("an update must never revoke a team membership")
		}
	}
	if !got.Roles.Equal(plan.Roles) {
		t.Errorf("state should hold the planned roles, got %v", got.Roles)
	}
}
