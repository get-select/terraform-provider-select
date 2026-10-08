// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"terraform-provider-select/internal/provider/resource_default_role"
)

// Default role grants live on the v2 API at the top level: every member of
// the organization holds them. The API has a list route, a create route and
// a delete route that takes If-Match, but no GET for one grant and no update.
// See default_role_resource.go.
const defaultRolesEndpoint = "/v2/default-roles"

// defaultRoleEndpoint escapes the id as one path segment.
func defaultRoleEndpoint(id string) string {
	return fmt.Sprintf("%s/%s", defaultRolesEndpoint, url.PathEscape(id))
}

// defaultRoleErrors words the failures every v2 resource can hit. The API key
// scope names follow the route's "default-roles" tag; the spec does not name
// them.
var defaultRoleErrors = v2ErrorFormat{
	Noun:       "Default Role",
	Subject:    "the default role grant",
	Object:     "the default role grant",
	ReadScope:  "default_roles:read",
	WriteScope: "default_roles:write",
}

// defaultRoleModel is the generated model plus the hand-written scope
// attribute.
type defaultRoleModel struct {
	resource_default_role.DefaultRoleModel
	Scope types.Object `tfsdk:"scope"`
}

// defaultRoleResponse mirrors DefaultRoleGrantV2.
type defaultRoleResponse struct {
	roleGrantResponse
	Etag       string `json:"etag"`
	UpdateTime string `json:"update_time"`
}

func buildDefaultRoleCreate(ctx context.Context, plan *defaultRoleModel) (*roleGrantCreatePayload, diag.Diagnostics) {
	return buildRoleGrantCreate(ctx, plan.Role, plan.Scope)
}

// applyDefaultRoleResponse writes an API response onto the model.
func applyDefaultRoleResponse(ctx context.Context, model, source *defaultRoleModel, response *defaultRoleResponse) diag.Diagnostics {
	scope, diags := roleGrantScopeValue(ctx, source.Scope, response.roleGrantScopeFields)
	if diags.HasError() {
		return diags
	}

	model.Id = types.StringValue(response.Id)
	model.Etag = types.StringValue(response.Etag)
	model.Role = types.StringValue(response.Role)
	model.Scope = scope
	model.CreateTime = types.StringValue(response.CreateTime)
	model.UpdateTime = types.StringValue(response.UpdateTime)

	return diags
}
