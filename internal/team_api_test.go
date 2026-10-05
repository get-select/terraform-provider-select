// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"testing"

	"terraform-provider-select/internal/provider/resource_team"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/defaults"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func team() resource_team.TeamModel {
	return resource_team.TeamModel{
		Id:                types.StringValue("t-1"),
		Etag:              types.StringValue("etag-1"),
		Name:              types.StringValue("Data Engineering"),
		DefaultMemberRole: types.StringValue("editor"),
		IsAllUsers:        types.BoolValue(false),
		CreateTime:        types.StringValue("2026-10-01T00:00:00Z"),
		UpdateTime:        types.StringValue("2026-10-01T00:00:00Z"),
	}
}

// TeamCreateV2 gives default_member_role a default of editor, but as a $ref,
// which the generator drops. The override in generator_overrides.v2.yml puts
// it back, so the plan shows the role rather than "known after apply".
func TestTeamSchemaDefaults(t *testing.T) {
	ctx := context.Background()
	attributes := resource_team.TeamResourceSchema(ctx).Attributes

	if !attributes["name"].IsRequired() {
		t.Error("name should be required")
	}

	role, ok := attributes["default_member_role"].(schema.StringAttribute)
	if !ok || role.Default == nil {
		t.Fatalf("default_member_role should carry a default, got %#v", attributes["default_member_role"])
	}
	if roleDefault := defaultString(t, role); roleDefault != "editor" {
		t.Errorf("default_member_role should default to editor, got %q", roleDefault)
	}

	allUsers, ok := attributes["is_all_users"].(schema.BoolAttribute)
	if !ok || allUsers.Default == nil {
		t.Fatalf("is_all_users should carry a default, got %#v", attributes["is_all_users"])
	}
}

func TestTeamCreatePayloadSendsEveryField(t *testing.T) {
	plan := team()

	body := marshal(t, buildTeamCreate(&plan))

	if body["name"] != "Data Engineering" || body["default_member_role"] != "editor" {
		t.Errorf("name and default_member_role should be sent as planned, got %v", body)
	}
	// false is a real value here, not an absent one.
	if value, present := body["is_all_users"]; !present || value != false {
		t.Errorf("is_all_users false should be sent, got %v", body["is_all_users"])
	}
}

func TestTeamUpdatePayloadOmitsUnchangedFields(t *testing.T) {
	state := team()
	plan := team()
	plan.Name = types.StringValue("Platform")

	body := marshal(t, buildTeamUpdate(&plan, &state))

	if body["name"] != "Platform" {
		t.Errorf("a changed name should be sent, got %v", body["name"])
	}
	for _, field := range []string{"default_member_role", "is_all_users"} {
		if _, present := body[field]; present {
			t.Errorf("%s did not change and should be omitted from the merge patch, got %v", field, body[field])
		}
	}
}

// Turning is_all_users off is a change to false. A plain bool with omitempty
// would drop it.
func TestTeamUpdatePayloadSendsAChangeToFalse(t *testing.T) {
	state := team()
	state.IsAllUsers = types.BoolValue(true)
	plan := team()
	plan.DefaultMemberRole = types.StringValue("viewer")

	body := marshal(t, buildTeamUpdate(&plan, &state))

	if value, present := body["is_all_users"]; !present || value != false {
		t.Errorf("a change to false should be sent, got %v", body["is_all_users"])
	}
	if body["default_member_role"] != "viewer" {
		t.Errorf("a changed default_member_role should be sent, got %v", body["default_member_role"])
	}
}

func TestApplyTeamResponse(t *testing.T) {
	plan := team()
	model := plan
	response := &teamResponse{
		Id: "t-2", Etag: "etag-2", Name: "Data Engineering", IsAllUsers: true,
		DefaultMemberRole: "admin", CreateTime: "2026-10-02T00:00:00Z", UpdateTime: "2026-10-03T00:00:00Z",
	}

	if diags := applyTeamResponse(context.Background(), &model, &plan, response); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if model.Id.ValueString() != "t-2" || model.Etag.ValueString() != "etag-2" ||
		!model.IsAllUsers.ValueBool() || model.DefaultMemberRole.ValueString() != "admin" ||
		model.UpdateTime.ValueString() != "2026-10-03T00:00:00Z" {
		t.Errorf("every field should come from the response, got %+v", model)
	}
}

// A team is a top-level resource, so `terraform import` takes its own id.
func TestTeamImportsByItsOwnId(t *testing.T) {
	r := NewTeamResource().(*v2Resource[resource_team.TeamModel, teamResponse])
	if r.importState != nil {
		t.Error("a team needs no import hook: the address is its id")
	}
	var _ resource.ResourceWithImportState = r
}

// defaultString resolves a string attribute's static default.
func defaultString(t *testing.T, attribute schema.StringAttribute) string {
	t.Helper()
	var resp defaults.StringResponse
	attribute.Default.DefaultString(context.Background(), defaults.StringRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("resolving default: %v", resp.Diagnostics)
	}
	return resp.PlanValue.ValueString()
}
