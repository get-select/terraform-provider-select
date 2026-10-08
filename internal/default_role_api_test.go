// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"
	"testing"

	"terraform-provider-select/internal/provider/resource_default_role"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func newDefaultRole() *v2Resource[defaultRoleModel, defaultRoleResponse] {
	return NewDefaultRoleResource().(*v2Resource[defaultRoleModel, defaultRoleResponse])
}

func defaultRole(t *testing.T) defaultRoleModel {
	return defaultRoleModel{
		DefaultRoleModel: resource_default_role.DefaultRoleModel{
			Id:         types.StringValue("d-1"),
			Etag:       types.StringValue("etag-1"),
			Role:       types.StringValue("viewer"),
			CreateTime: types.StringValue("2026-10-01T00:00:00Z"),
			UpdateTime: types.StringValue("2026-10-01T00:00:00Z"),
		},
		Scope: scopeObject(t, "snowflake_account", types.StringValue("Acct-UUID")),
	}
}

// The API has no update for a default role grant, so role forces a new
// grant. A default grant has no parent.
func TestDefaultRoleSchemaReplacesOnRole(t *testing.T) {
	attributes := defaultRoleResourceSchema(context.Background()).Attributes

	role := attributes["role"].(schema.StringAttribute)
	if !role.Required || !requiresReplace(role) {
		t.Error("role should be required and force a new grant")
	}
	for _, name := range []string{"team_id", "email"} {
		if _, present := attributes[name]; present {
			t.Errorf("a default role grant has no %s", name)
		}
	}
}

func TestDefaultRoleCreatePayload(t *testing.T) {
	plan := defaultRole(t)

	payload, diags := buildDefaultRoleCreate(context.Background(), &plan)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	body := marshal(t, payload)
	scope, _ := body["scope"].(map[string]any)
	if body["role"] != "viewer" || scope["type"] != "snowflake_account" || scope["id"] != "Acct-UUID" {
		t.Errorf("role and scope should be sent as planned, got %v", body)
	}
}

// Read lists the default grants and finds this one. The Snowflake account
// UUID comes back in lower case, and keeps its configured spelling.
func TestDefaultRoleReadFindsTheGrantInTheList(t *testing.T) {
	server := newRecordingServer(t, http.StatusOK, `{"items":[
		{"id":"d-0","role":"admin","entity":{"type":"select_organization","id":"org","display_name":"Org"},"create_time":"x","update_time":"x","etag":"e"},
		{"id":"d-1","role":"viewer","snowflake_account_uuid":"acct-uuid","entity":{"type":"snowflake_account","id":"acct-uuid","display_name":"Acct"},"create_time":"2026-10-01T00:00:00Z","update_time":"2026-10-03T00:00:00Z","etag":"etag-3"}
	]}`)

	state := defaultRole(t)
	resp := readV2(t, newDefaultRole(), server.URL, state)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	var got defaultRoleModel
	resp.State.Get(context.Background(), &got)
	if got.Etag.ValueString() != "etag-3" || got.UpdateTime.ValueString() != "2026-10-03T00:00:00Z" {
		t.Errorf("Read should record the listed grant's etag, got %+v", got.DefaultRoleModel)
	}
	if !got.Scope.Equal(state.Scope) {
		t.Errorf("the scope id should keep its configured case, got %v", got.Scope)
	}
	if server.requests[0].Path != "/v2/default-roles" {
		t.Errorf("Read should list the default grants, got %s", server.requests[0].Path)
	}

	missing := newRecordingServer(t, http.StatusOK, `{"items":[]}`)
	resp = readV2(t, newDefaultRole(), missing.URL, state)
	if resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
		t.Errorf("a grant missing from the list should be removed from state, got %v", resp.Diagnostics)
	}
}

// The default role delete route takes If-Match.
func TestDefaultRoleDeleteSendsIfMatch(t *testing.T) {
	server := newRecordingServer(t, http.StatusNoContent, "")

	resp := deleteV2(t, newDefaultRole(), server.URL, defaultRole(t))
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	got := server.requests[0]
	if got.Method != http.MethodDelete || got.Path != "/v2/default-roles/d-1" || got.IfMatch != "etag-1" {
		t.Errorf("delete should carry the ETag, got %+v", got)
	}
}

// A default role grant is top-level, so `terraform import` takes its own id.
func TestImportDefaultRoleByItsOwnId(t *testing.T) {
	resp := importV2(t, newDefaultRole(), "d-1")
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	var got defaultRoleModel
	resp.State.Get(context.Background(), &got)
	if got.Id.ValueString() != "d-1" {
		t.Errorf("import should write the id, got %v", got.Id)
	}
}
