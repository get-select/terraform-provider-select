// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"terraform-provider-select/internal/provider/resource_team_role"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func newTeamRole() *v2Resource[teamRoleModel, teamRoleResponse] {
	return NewTeamRoleResource().(*v2Resource[teamRoleModel, teamRoleResponse])
}

func teamRole(t *testing.T) teamRoleModel {
	return teamRoleModel{
		TeamRoleModel: resource_team_role.TeamRoleModel{
			Id:         types.StringValue("r-1"),
			TeamId:     types.StringValue("t-1"),
			Role:       types.StringValue("viewer"),
			CreateTime: types.StringValue("2026-10-01T00:00:00Z"),
		},
		Scope: scopeObject(t, "usage_group", types.StringValue("ug-1")),
	}
}

// The API has no update for a team role grant, so every input attribute has
// to force a new grant.
func TestTeamRoleSchemaReplacesOnEveryAttribute(t *testing.T) {
	attributes := teamRoleResourceSchema(context.Background()).Attributes

	for _, name := range []string{"team_id", "role"} {
		attribute, ok := attributes[name].(schema.StringAttribute)
		if !ok {
			t.Fatalf("%s should be a string attribute, got %T", name, attributes[name])
		}
		if !attribute.Required || !requiresReplace(attribute) {
			t.Errorf("%s should be required and force a new grant", name)
		}
	}
	if _, present := attributes["etag"]; present {
		t.Error("a team role grant has no ETag")
	}
}

func TestTeamRoleCreatePayload(t *testing.T) {
	plan := teamRole(t)

	payload, diags := buildTeamRoleCreate(context.Background(), &plan)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	body := marshal(t, payload)
	scope, _ := body["scope"].(map[string]any)
	if body["role"] != "viewer" || scope["type"] != "usage_group" || scope["id"] != "ug-1" {
		t.Errorf("role and scope should be sent as planned, got %v", body)
	}
	if _, present := body["team_id"]; present {
		t.Error("team_id belongs in the path, not the body: TeamRoleGrantCreateV2 forbids extra properties")
	}
}

// TeamRoleGrantV2 does not carry the team id, so it has to survive from the
// model. The scope comes back as an entity and a flat column, and has to
// round-trip to the configured value.
func TestApplyTeamRoleResponse(t *testing.T) {
	ctx := context.Background()
	plan := teamRole(t)
	plan.Id = types.StringUnknown()
	model := plan
	response := &teamRoleResponse{roleGrantResponse{
		Id: "r-2", Role: "viewer", CreateTime: "2026-10-02T00:00:00Z",
		roleGrantScopeFields: roleGrantScopeFields{
			Entity:       roleGrantEntity{Type: "usage_group", Id: "ug-1", DisplayName: "Set: Group"},
			UsageGroupId: strPtr("ug-1"),
		},
	}}

	if diags := applyTeamRoleResponse(ctx, &model, &plan, response); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if model.TeamId.ValueString() != "t-1" || model.Id.ValueString() != "r-2" || model.CreateTime.ValueString() != "2026-10-02T00:00:00Z" {
		t.Errorf("team_id should be kept and id and create_time taken from the response, got %+v", model.TeamRoleModel)
	}
	if !model.Scope.Equal(plan.Scope) {
		t.Errorf("the scope should round-trip, got %v want %v", model.Scope, plan.Scope)
	}

	// An omitted scope means the organization. The API reports it as the
	// organization entity, and the attribute has to stay null.
	plan.Scope = nullScope()
	model = plan
	response.roleGrantScopeFields = roleGrantScopeFields{Entity: roleGrantEntity{Type: "select_organization", Id: "org"}}
	if diags := applyTeamRoleResponse(ctx, &model, &plan, response); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !model.Scope.IsNull() {
		t.Errorf("an omitted scope should stay null, got %v", model.Scope)
	}
}

func TestValidateTeamRoleConfig(t *testing.T) {
	config := teamRole(t)
	if diags := validateTeamRoleConfig(context.Background(), &config); diags.HasError() {
		t.Errorf("viewer on a usage group is valid: %v", diags)
	}

	config.Role = types.StringValue("editor")
	diags := validateTeamRoleConfig(context.Background(), &config)
	if !diags.HasError() {
		t.Fatal("editor on a usage group should be rejected at plan time")
	}
	if d, ok := diags[0].(interface{ Path() path.Path }); !ok || !d.Path().Equal(path.Root("role")) {
		t.Errorf("the error should point at role, got %v", diags[0])
	}
}

// Read has no GET for one grant, so it pages through the team's grants. A
// grant missing from the list is removed from state.
func TestTeamRoleReadFindsTheGrantInTheList(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page_token") == "" {
			_, _ = w.Write([]byte(`{"items":[{"id":"r-0","role":"admin","entity":{"type":"select_organization","id":"org","display_name":"Org"},"create_time":"x"}],"page_token":"p2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"r-1","role":"viewer","usage_group_id":"ug-1","entity":{"type":"usage_group","id":"ug-1","display_name":"Set: Group"},"create_time":"2026-10-01T00:00:00Z"}]}`))
	}))
	defer server.Close()

	state := teamRole(t)
	resp := readV2(t, newTeamRole(), server.URL, state)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	var got teamRoleModel
	resp.State.Get(context.Background(), &got)
	if got.Id.ValueString() != "r-1" || got.TeamId.ValueString() != "t-1" || !got.Scope.Equal(state.Scope) {
		t.Errorf("Read should keep the grant as it was, got %+v", got)
	}
	for _, p := range paths {
		if p != "/v2/teams/t-1/roles" {
			t.Errorf("Read should only list the team's grants, got a request to %s", p)
		}
	}

	missing := newRecordingServer(t, http.StatusOK, `{"items":[]}`)
	resp = readV2(t, newTeamRole(), missing.URL, state)
	if resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
		t.Errorf("a grant missing from the list should be removed from state, got %v", resp.Diagnostics)
	}
}

// A team role grant has no ETag, so the delete must not send If-Match.
func TestTeamRoleDeleteSendsNoIfMatch(t *testing.T) {
	server := newRecordingServer(t, http.StatusNoContent, "")

	resp := deleteV2(t, newTeamRole(), server.URL, teamRole(t))
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if len(server.requests) != 1 {
		t.Fatalf("expected one request, got %v", server.requests)
	}
	got := server.requests[0]
	if got.Method != http.MethodDelete || got.Path != "/v2/teams/t-1/roles/r-1" {
		t.Errorf("delete went to %s %s", got.Method, got.Path)
	}
	if got.HasIfMatch {
		t.Errorf("a team role grant has no ETag, so no If-Match should be sent, got %q", got.IfMatch)
	}
}

func TestImportTeamRole(t *testing.T) {
	resp := importV2(t, newTeamRole(), "t-1/r-1")
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	var got teamRoleModel
	resp.State.Get(context.Background(), &got)
	if got.TeamId.ValueString() != "t-1" || got.Id.ValueString() != "r-1" {
		t.Errorf("import should write team_id and id, got team_id=%v id=%v", got.TeamId, got.Id)
	}

	resp = importV2(t, newTeamRole(), "r-1")
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), "team_id/role_id") {
		t.Errorf("an address without the team id should be rejected with the expected form, got %v", resp.Diagnostics)
	}
}

func TestTeamRoleEndpointsEscapeEachPathSegment(t *testing.T) {
	cases := map[string]string{
		teamRolesEndpoint("a/b c"):           "/v2/teams/a%2Fb%20c/roles",
		teamRoleEndpoint("a/b c", "r/1?x#y"): "/v2/teams/a%2Fb%20c/roles/r%2F1%3Fx%23y",
		defaultRoleEndpoint("r/1"):           "/v2/default-roles/r%2F1",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("endpoint = %q, want %q", got, want)
		}
	}
}

// A create POSTs role and scope to the team's grant list. A 409 tells the
// user how to import the grant that may already exist.
func TestTeamRoleCreateConflictSuggestsImport(t *testing.T) {
	server := newRecordingServer(t, http.StatusConflict,
		`{"type":"about:blank","title":"Conflict","status":409,"detail":"Role grant already exists","code":"conflict"}`)
	r := newTeamRole()
	r.client = NewAPIClient("key", "org", server.URL)
	ctx := context.Background()
	s := r.schema(ctx)

	plan := teamRole(t)
	plan.Id = types.StringUnknown()
	plan.CreateTime = types.StringUnknown()
	resp := resource.CreateResponse{State: v2State[teamRoleModel](t, s, nil)}
	r.Create(ctx, resource.CreateRequest{Plan: tfPlan(t, s, &plan)}, &resp)

	got := server.requests[0]
	if got.Method != http.MethodPost || got.Path != "/v2/teams/t-1/roles" ||
		got.Body != `{"role":"viewer","scope":{"type":"usage_group","id":"ug-1"}}` {
		t.Errorf("create should POST role and scope to the team's grants, got %+v", got)
	}
	if !resp.Diagnostics.HasError() {
		t.Fatal("a 409 should fail the create")
	}
	if detail := resp.Diagnostics[0].Detail(); !strings.Contains(detail, "terraform import select_team_role.<name> <team_id>/<role_id>") {
		t.Errorf("the diagnostic should tell how to import the grant, got:\n%s", detail)
	}
}
