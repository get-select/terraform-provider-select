// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"terraform-provider-select/internal/provider/resource_user_role"
)

// User role grants live on the v2 API, nested under the user's email
// address. The API keeps a grant against the email, so a user who has not
// signed in yet can hold one. See v2_api.go for the conventions every
// resource on that surface shares.
const usersEndpoint = "/v2/users"

// userRolesEndpoint escapes the email as one path segment. An email address
// can hold characters such as "+" and, in its local part, "/".
func userRolesEndpoint(email string) string {
	return fmt.Sprintf("%s/%s/roles", usersEndpoint, url.PathEscape(email))
}

// userRoleEndpoint escapes the email and the grant id as one path segment
// each.
func userRoleEndpoint(email, id string) string {
	return fmt.Sprintf("%s/%s", userRolesEndpoint(email), url.PathEscape(id))
}

// userRoleErrors words the failures every v2 resource can hit. The API key
// scope names follow the route's "users" tag; the spec does not name them.
var userRoleErrors = v2ErrorFormat{
	Noun:       "User Role",
	Subject:    "the user role grant",
	Object:     "the user role grant",
	ReadScope:  "users:read",
	WriteScope: "users:write",
}

// userRoleModel is the generated model plus the hand-written scope attribute.
type userRoleModel struct {
	resource_user_role.UserRoleModel
	Scope types.Object `tfsdk:"scope"`
}

// userRoleUpdatePayload mirrors UserRoleGrantUpdateV2, a JSON Merge Patch
// body. role is the only field a grant can change; every other attribute
// forces a new grant.
type userRoleUpdatePayload struct {
	Role *string `json:"role,omitempty"`
}

// userRoleResponse mirrors UserRoleGrantV2. It does not carry the email: the
// route holds it.
type userRoleResponse struct {
	roleGrantResponse
	Etag       string `json:"etag"`
	UpdateTime string `json:"update_time"`
	// IsDefault and GrantedFromTeamName tell where the user's role comes
	// from. A grant this resource manages is a direct grant: IsDefault is
	// false and GrantedFromTeamName is null.
	IsDefault           bool    `json:"is_default"`
	GrantedFromTeamName *string `json:"granted_from_team_name"`
}

// isDirect reports whether the grant was made to the user directly. A default
// grant is managed by select_default_role, and a team-inherited grant by
// select_team_role.
func (r *userRoleResponse) isDirect() bool {
	return !r.IsDefault && r.GrantedFromTeamName == nil
}

func buildUserRoleCreate(ctx context.Context, plan *userRoleModel) (*roleGrantCreatePayload, diag.Diagnostics) {
	return buildRoleGrantCreate(ctx, plan.Role, plan.Scope)
}

func buildUserRoleUpdate(plan, state *userRoleModel) *userRoleUpdatePayload {
	return &userRoleUpdatePayload{
		Role: changedString(plan.Role, state.Role),
	}
}

// fetchDirectUserRole is the fetch hook for select_user_role. GET on a grant
// id returns the grant from any of its three sources: direct, default or
// team-inherited. This resource manages only direct grants, so a grant from
// another source answers a 404, the same as a deleted grant. Read then
// removes it from state, and an import of such a grant fails.
func fetchDirectUserRole(ctx context.Context, client *APIClient, model *userRoleModel) (*userRoleResponse, *apiError, diag.Diagnostics) {
	endpoint := userRoleEndpoint(model.Email.ValueString(), model.Id.ValueString())

	var response userRoleResponse
	apiErr, diags := client.doRequest(ctx, http.MethodGet, endpoint, nil, &response, requestOptions{})
	if diags.HasError() || apiErr != nil {
		return nil, apiErr, diags
	}
	if !response.isDirect() {
		return nil, &apiError{
			StatusCode: http.StatusNotFound,
			Detail:     fmt.Sprintf("the role grant at %s is not a direct grant to the user", endpoint),
		}, nil
	}
	return &response, nil, nil
}

// applyUserRoleResponse writes an API response onto the model. email comes
// from source, because the response does not carry it. The email therefore
// keeps its configured spelling.
func applyUserRoleResponse(ctx context.Context, model, source *userRoleModel, response *userRoleResponse) diag.Diagnostics {
	scope, diags := roleGrantScopeValue(ctx, source.Scope, response.roleGrantScopeFields)
	if diags.HasError() {
		return diags
	}

	model.Id = types.StringValue(response.Id)
	model.Etag = types.StringValue(response.Etag)
	model.Email = source.Email
	model.Role = types.StringValue(response.Role)
	model.Scope = scope
	model.CreateTime = types.StringValue(response.CreateTime)
	model.UpdateTime = types.StringValue(response.UpdateTime)

	return diags
}
