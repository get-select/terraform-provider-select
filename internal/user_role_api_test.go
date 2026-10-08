// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"terraform-provider-select/internal/provider/resource_user_role"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func newUserRole() *v2Resource[userRoleModel, userRoleResponse] {
	return NewUserRoleResource().(*v2Resource[userRoleModel, userRoleResponse])
}

func userRole() userRoleModel {
	return userRoleModel{
		UserRoleModel: resource_user_role.UserRoleModel{
			Id:         types.StringValue("r-1"),
			Etag:       types.StringValue("etag-1"),
			Email:      types.StringValue("Alice@Example.com"),
			Role:       types.StringValue("viewer"),
			CreateTime: types.StringValue("2026-10-01T00:00:00Z"),
			UpdateTime: types.StringValue("2026-10-01T00:00:00Z"),
		},
		Scope: nullScope(),
	}
}

// userRoleBody is a UserRoleGrantV2 on the organization, with the given
// source fields.
func userRoleBody(role, sourceFields string) string {
	return `{"id":"r-1","role":"` + role + `","entity":{"type":"select_organization","id":"org","display_name":"Org"},` +
		`"create_time":"2026-10-01T00:00:00Z","update_time":"2026-10-02T00:00:00Z","etag":"etag-2",` + sourceFields + `}`
}

// UserRoleGrantUpdateV2 carries only role, so email and scope force a new
// grant. The email is not a secret, although another schema in the spec
// marks a property of the same name sensitive.
func TestUserRoleSchema(t *testing.T) {
	attributes := userRoleResourceSchema(context.Background()).Attributes

	email := attributes["email"].(schema.StringAttribute)
	if !email.Required || !requiresReplace(email) {
		t.Error("email should be required and force a new grant")
	}
	if email.Sensitive {
		t.Error("email should not be sensitive: a plan has to show whose grant changes")
	}

	role := attributes["role"].(schema.StringAttribute)
	if !role.Required || requiresReplace(role) {
		t.Error("role should be required and change in place with PATCH")
	}
}

func TestUserRoleUpdatePayloadCarriesOnlyRole(t *testing.T) {
	state := userRole()
	plan := userRole()

	if body := marshal(t, buildUserRoleUpdate(&plan, &state)); len(body) != 0 {
		t.Errorf("an unchanged role should send an empty merge patch, got %v", body)
	}

	plan.Role = types.StringValue("editor")
	body := marshal(t, buildUserRoleUpdate(&plan, &state))
	if len(body) != 1 || body["role"] != "editor" {
		t.Errorf("a changed role should be the only field sent, got %v", body)
	}
}

func TestUserRoleCreatePayload(t *testing.T) {
	plan := userRole()

	payload, diags := buildUserRoleCreate(context.Background(), &plan)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	body := marshal(t, payload)
	if len(body) != 1 || body["role"] != "viewer" {
		t.Errorf("only role should be sent: email is in the path and a null scope is omitted, got %v", body)
	}
}

// Read records a direct grant, and keeps the configured email: the response
// does not carry it.
func TestUserRoleReadKeepsADirectGrant(t *testing.T) {
	server := newRecordingServer(t, http.StatusOK, userRoleBody("editor", `"is_default":false,"granted_from_team_name":null`))

	resp := readV2(t, newUserRole(), server.URL, userRole())
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	var got userRoleModel
	resp.State.Get(context.Background(), &got)
	if got.Role.ValueString() != "editor" || got.Etag.ValueString() != "etag-2" || got.UpdateTime.ValueString() != "2026-10-02T00:00:00Z" {
		t.Errorf("Read should record the role and etag SELECT reports, got %+v", got.UserRoleModel)
	}
	if got.Email.ValueString() != "Alice@Example.com" {
		t.Errorf("the email should keep its configured spelling, got %v", got.Email)
	}
	if !got.Scope.IsNull() {
		t.Errorf("an omitted scope should stay null, got %v", got.Scope)
	}
	if server.requests[0].Method != http.MethodGet || server.requests[0].Path != "/v2/users/Alice@Example.com/roles/r-1" {
		t.Errorf("Read should GET the grant, got %+v", server.requests[0])
	}
}

// GET returns a grant from any source. This resource manages only direct
// grants, so a default or team-inherited grant is treated as gone.
func TestUserRoleReadTreatsAnIndirectGrantAsGone(t *testing.T) {
	for name, fields := range map[string]string{
		"default":        `"is_default":true,"granted_from_team_name":null`,
		"team-inherited": `"is_default":false,"granted_from_team_name":"Data Engineering"`,
	} {
		server := newRecordingServer(t, http.StatusOK, userRoleBody("viewer", fields))

		resp := readV2(t, newUserRole(), server.URL, userRole())
		if resp.Diagnostics.HasError() {
			t.Errorf("%s: an indirect grant is not an error: %v", name, resp.Diagnostics)
			continue
		}
		if !resp.State.Raw.IsNull() {
			t.Errorf("%s: an indirect grant should be removed from state", name)
		}
	}
}

// An update carries the ETag as If-Match and only the changed role.
func TestUserRoleUpdateSendsIfMatch(t *testing.T) {
	server := newRecordingServer(t, http.StatusOK, userRoleBody("editor", `"is_default":false,"granted_from_team_name":null`))
	r := newUserRole()
	r.client = NewAPIClient("key", "org", server.URL)
	ctx := context.Background()
	s := r.schema(ctx)

	state := userRole()
	plan := userRole()
	plan.Role = types.StringValue("editor")
	resp := resource.UpdateResponse{State: v2State(t, s, &state)}
	r.Update(ctx, resource.UpdateRequest{
		Plan:  tfPlan(t, s, &plan),
		State: v2State(t, s, &state),
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	got := server.requests[0]
	if got.Method != http.MethodPatch || got.IfMatch != "etag-1" || got.Body != `{"role":"editor"}` {
		t.Errorf("update should PATCH the role with If-Match, got %+v", got)
	}
}

func TestImportUserRoleSplitsAtTheLastSlash(t *testing.T) {
	resp := importV2(t, newUserRole(), "odd/name@example.com/r-1")
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	var got userRoleModel
	resp.State.Get(context.Background(), &got)
	if got.Email.ValueString() != "odd/name@example.com" || got.Id.ValueString() != "r-1" {
		t.Errorf("import should write email and id, got email=%v id=%v", got.Email, got.Id)
	}

	resp = importV2(t, newUserRole(), "alice@example.com")
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), "email/role_id") {
		t.Errorf("an address without the grant id should be rejected with the expected form, got %v", resp.Diagnostics)
	}
}

func TestUserRoleEndpointsEscapeTheEmail(t *testing.T) {
	cases := map[string]string{
		userRolesEndpoint("alice@example.com"):          "/v2/users/alice@example.com/roles",
		userRolesEndpoint("odd/name@example.com"):       "/v2/users/odd%2Fname@example.com/roles",
		userRoleEndpoint("a b?c#d@example.com", "r/1"):  "/v2/users/a%20b%3Fc%23d@example.com/roles/r%2F1",
		userRoleEndpoint("alice+ci@example.com", "r-1"): "/v2/users/alice+ci@example.com/roles/r-1",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("endpoint = %q, want %q", got, want)
		}
	}
}

// An email with "+" or "/" must reach the API as one path segment that
// decodes back to the same email. The "+" is literal in a path: only a query
// string decodes it as a space.
func TestEscapedEmailReachesTheServerIntact(t *testing.T) {
	server := newRecordingServer(t, http.StatusOK, userRoleBody("viewer", `"is_default":false,"granted_from_team_name":null`))

	for _, email := range []string{"alice+ci@example.com", "odd/name@example.com"} {
		state := userRole()
		state.Email = types.StringValue(email)
		if resp := readV2(t, newUserRole(), server.URL, state); resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
	}

	want := []struct{ path, escaped string }{
		{"/v2/users/alice+ci@example.com/roles/r-1", "/v2/users/alice+ci@example.com/roles/r-1"},
		{"/v2/users/odd/name@example.com/roles/r-1", "/v2/users/odd%2Fname@example.com/roles/r-1"},
	}
	if len(server.requests) != len(want) {
		t.Fatalf("expected %d requests, got %v", len(want), server.requests)
	}
	for i, w := range want {
		got := server.requests[i]
		if got.Path != w.path || got.EscapedPath != w.escaped {
			t.Errorf("request %d reached the server as %q (escaped %q), want %q (escaped %q)",
				i, got.Path, got.EscapedPath, w.path, w.escaped)
		}
	}
}
