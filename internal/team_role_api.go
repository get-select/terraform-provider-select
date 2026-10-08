// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"terraform-provider-select/internal/provider/resource_team_role"
)

// Team role grants live on the v2 API, nested under their team. The API has a
// list route, a create route and a delete route, but no GET for one grant and
// no update. A grant also has no ETag. See team_role_resource.go.
func teamRolesEndpoint(teamId string) string {
	return fmt.Sprintf("%s/roles", teamEndpoint(teamId))
}

// teamRoleEndpoint escapes the grant id as one path segment. teamEndpoint
// escapes the team id.
func teamRoleEndpoint(teamId, id string) string {
	return fmt.Sprintf("%s/%s", teamRolesEndpoint(teamId), url.PathEscape(id))
}

// teamRoleErrors words the failures every v2 resource can hit. The grant
// routes use the team scopes.
var teamRoleErrors = v2ErrorFormat{
	Noun:       "Team Role",
	Subject:    "the team role grant",
	Object:     "the team role grant",
	ReadScope:  "teams:read",
	WriteScope: "teams:write",
}

// teamRoleModel is the generated model plus the hand-written scope
// attribute. The framework flattens an untagged embedded struct, as for
// budgetModel.
type teamRoleModel struct {
	resource_team_role.TeamRoleModel
	Scope types.Object `tfsdk:"scope"`
}

// teamRoleResponse mirrors TeamRoleGrantV2. It does not carry the team's id:
// the route holds it.
type teamRoleResponse struct {
	roleGrantResponse
}

func buildTeamRoleCreate(ctx context.Context, plan *teamRoleModel) (*roleGrantCreatePayload, diag.Diagnostics) {
	return buildRoleGrantCreate(ctx, plan.Role, plan.Scope)
}

// applyTeamRoleResponse writes an API response onto the model. team_id comes
// from source, because the response does not carry it.
func applyTeamRoleResponse(ctx context.Context, model, source *teamRoleModel, response *teamRoleResponse) diag.Diagnostics {
	scope, diags := roleGrantScopeValue(ctx, source.Scope, response.roleGrantScopeFields)
	if diags.HasError() {
		return diags
	}

	model.Id = types.StringValue(response.Id)
	model.TeamId = source.TeamId
	model.Role = types.StringValue(response.Role)
	model.Scope = scope
	model.CreateTime = types.StringValue(response.CreateTime)

	return diags
}

// validateTeamRoleConfig runs the plan-time checks that the schema cannot
// express: the role must be one the scope accepts.
func validateTeamRoleConfig(ctx context.Context, config *teamRoleModel) diag.Diagnostics {
	return validateRoleGrantRole(ctx, path.Root("role"), config.Role, config.Scope)
}
