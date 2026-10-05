// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"terraform-provider-select/internal/provider/resource_team_member"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func teamMember() resource_team_member.TeamMemberModel {
	return resource_team_member.TeamMemberModel{
		Id:         types.StringValue("m-1"),
		Etag:       types.StringValue("etag-1"),
		TeamId:     types.StringValue("t-1"),
		Type:       types.StringValue("user"),
		Identifier: types.StringValue("alice@example.com"),
		Role:       types.StringValue("editor"),
		CreateTime: types.StringValue("2026-10-01T00:00:00Z"),
	}
}

// requiresReplace reports whether a string attribute carries a plan modifier
// that destroys and recreates the resource.
func requiresReplace(attribute schema.StringAttribute) bool {
	for _, modifier := range attribute.PlanModifiers {
		if strings.Contains(modifier.Description(context.Background()), "destroy and recreate") {
			return true
		}
	}
	return false
}

// TeamMemberUpdateV2 carries only role, so every other input attribute has to
// force a new member. team_id is not in TeamMemberV2 at all, so the generator
// makes it computed; the override makes it required.
func TestTeamMemberSchemaReplacesOnEverythingButRole(t *testing.T) {
	attributes := resource_team_member.TeamMemberResourceSchema(context.Background()).Attributes

	for _, name := range []string{"team_id", "type", "identifier"} {
		attribute, ok := attributes[name].(schema.StringAttribute)
		if !ok {
			t.Fatalf("%s should be a string attribute, got %T", name, attributes[name])
		}
		if !attribute.Required {
			t.Errorf("%s should be required", name)
		}
		if !requiresReplace(attribute) {
			t.Errorf("%s should force a new member: the API cannot change it in place", name)
		}
	}

	role := attributes["role"].(schema.StringAttribute)
	if requiresReplace(role) {
		t.Error("role changes in place with PATCH and should not force a new member")
	}
	if role.Default == nil || defaultString(t, role) != "editor" {
		t.Error("role should default to editor")
	}
}

func TestTeamMemberCreatePayload(t *testing.T) {
	plan := teamMember()

	body := marshal(t, buildTeamMemberCreate(&plan))

	if body["type"] != "user" || body["identifier"] != "alice@example.com" || body["role"] != "editor" {
		t.Errorf("type, identifier and role should be sent as planned, got %v", body)
	}
	if _, present := body["team_id"]; present {
		t.Error("team_id belongs in the path, not the body: TeamMemberCreateV2 forbids extra properties")
	}
}

func TestTeamMemberUpdatePayloadCarriesOnlyRole(t *testing.T) {
	state := teamMember()
	plan := teamMember()

	if body := marshal(t, buildTeamMemberUpdate(&plan, &state)); len(body) != 0 {
		t.Errorf("an unchanged role should send an empty merge patch, got %v", body)
	}

	plan.Role = types.StringValue("viewer")
	body := marshal(t, buildTeamMemberUpdate(&plan, &state))
	if len(body) != 1 || body["role"] != "viewer" {
		t.Errorf("a changed role should be the only field sent, got %v", body)
	}
}

// TeamMemberV2 does not carry the team id, so it has to survive from the
// model. An email that comes back in a different case keeps its configured
// spelling.
func TestApplyTeamMemberResponse(t *testing.T) {
	plan := teamMember()
	plan.Identifier = types.StringValue("Alice@Example.com")
	model := plan
	response := &teamMemberResponse{
		Id: "m-2", Etag: "etag-2", Type: "user", Identifier: "alice@example.com",
		Role: "admin", CreateTime: "2026-10-02T00:00:00Z",
	}

	if diags := applyTeamMemberResponse(context.Background(), &model, &plan, response); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if model.TeamId.ValueString() != "t-1" {
		t.Errorf("team_id should be kept from the model, got %v", model.TeamId)
	}
	if model.Identifier.ValueString() != "Alice@Example.com" {
		t.Errorf("an identifier that differs only in case should keep its configured spelling, got %v", model.Identifier)
	}
	if model.Id.ValueString() != "m-2" || model.Role.ValueString() != "admin" || model.Etag.ValueString() != "etag-2" {
		t.Errorf("id, role and etag should come from the response, got %+v", model)
	}
}

// teamMemberState is a state for the team member schema. With a nil model it
// holds no resource, which is what Terraform gives ImportState.
func teamMemberState(t *testing.T, model *resource_team_member.TeamMemberModel) tfsdk.State {
	t.Helper()
	ctx := context.Background()
	s := resource_team_member.TeamMemberResourceSchema(ctx)
	state := tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if model != nil {
		if diags := state.Set(ctx, model); diags.HasError() {
			t.Fatalf("setting state: %v", diags)
		}
	}
	return state
}

func readTeamMember(t *testing.T, serverURL string, state resource_team_member.TeamMemberModel) resource.ReadResponse {
	t.Helper()
	r := NewTeamMemberResource().(*v2Resource[resource_team_member.TeamMemberModel, teamMemberResponse])
	r.client = NewAPIClient("key", "org", serverURL)

	req := resource.ReadRequest{State: teamMemberState(t, &state)}
	resp := resource.ReadResponse{State: teamMemberState(t, &state)}
	r.Read(context.Background(), req, &resp)
	return resp
}

// Read has no GET for one member, so it pages through the team's members. A
// role changed outside Terraform has to show up as drift.
func TestTeamMemberReadFindsTheMemberOnALaterPage(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page_token") == "" {
			_, _ = w.Write([]byte(`{"items":[{"id":"m-0","etag":"e","type":"user","identifier":"bob@example.com","role":"viewer","create_time":"x"}],"page_token":"p2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"m-1","etag":"etag-9","type":"user","identifier":"alice@example.com","role":"admin","create_time":"2026-10-01T00:00:00Z"}]}`))
	}))
	defer server.Close()

	resp := readTeamMember(t, server.URL, teamMember())
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	var got resource_team_member.TeamMemberModel
	resp.State.Get(context.Background(), &got)
	if got.Role.ValueString() != "admin" || got.Etag.ValueString() != "etag-9" {
		t.Errorf("Read should record the listed member's role and etag, got %+v", got)
	}
	if got.TeamId.ValueString() != "t-1" {
		t.Errorf("Read should keep team_id, got %v", got.TeamId)
	}
	for _, p := range paths {
		if p != "/v2/teams/t-1/members" {
			t.Errorf("Read should only list the team's members, got a request to %s", p)
		}
	}
	if len(paths) != 2 {
		t.Errorf("Read should follow the page token once, got %d requests", len(paths))
	}
}

// A member someone removed outside Terraform is absent from the list. Read
// must drop it from state so the next plan adds it again.
func TestTeamMemberReadRemovesAMemberMissingFromTheList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":"m-0","etag":"e","type":"user","identifier":"bob@example.com","role":"viewer","create_time":"x"}]}`))
	}))
	defer server.Close()

	resp := readTeamMember(t, server.URL, teamMember())
	if resp.Diagnostics.HasError() {
		t.Fatalf("a missing member is not an error: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("a member missing from the list should be removed from state")
	}
}

func TestSplitChildImportID(t *testing.T) {
	cases := map[string]struct {
		parent, child string
		ok            bool
	}{
		"t-1/m-1": {"t-1", "m-1", true},
		// The child is always the last segment; a parent such as an email or an
		// SSO group name can hold a "/".
		"eng/data/m-1": {"eng/data", "m-1", true},
		"just-an-id":   {"", "", false},
		"/m-1":         {"", "m-1", false},
		"t-1/":         {"t-1", "", false},
		"":             {"", "", false},
	}
	for id, want := range cases {
		parent, child, ok := splitChildImportID(id)
		if ok != want.ok || (ok && (parent != want.parent || child != want.child)) {
			t.Errorf("splitChildImportID(%q) = %q, %q, %v; want %q, %q, %v", id, parent, child, ok, want.parent, want.child, want.ok)
		}
	}
}

func TestImportTeamMemberWritesTeamIdAndId(t *testing.T) {
	r := NewTeamMemberResource().(*v2Resource[resource_team_member.TeamMemberModel, teamMemberResponse])

	resp := resource.ImportStateResponse{State: teamMemberState(t, nil)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "t-1/m-1"}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	var got resource_team_member.TeamMemberModel
	resp.State.Get(context.Background(), &got)
	if got.TeamId.ValueString() != "t-1" || got.Id.ValueString() != "m-1" {
		t.Errorf("import should write team_id and id, got team_id=%v id=%v", got.TeamId, got.Id)
	}
}

func TestImportTeamMemberRejectsAddressesMissingAnId(t *testing.T) {
	r := NewTeamMemberResource().(*v2Resource[resource_team_member.TeamMemberModel, teamMemberResponse])

	for _, id := range []string{"just-the-member-id", "/m-1", "t-1/", ""} {
		resp := resource.ImportStateResponse{State: teamMemberState(t, nil)}
		r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)

		if !resp.Diagnostics.HasError() {
			t.Errorf("%q is not a usable import address and should be rejected", id)
			continue
		}
		if !strings.Contains(resp.Diagnostics[0].Detail(), "team_id/member_id") {
			t.Errorf("the error should show the expected form, got: %s", resp.Diagnostics[0].Detail())
		}
	}
}
