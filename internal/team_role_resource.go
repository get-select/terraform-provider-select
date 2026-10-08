// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"terraform-provider-select/internal/provider/resource_team_role"
)

func teamRoleResourceSchema(ctx context.Context) schema.Schema {
	return withRoleGrantScope(resource_team_role.TeamRoleResourceSchema(ctx))
}

func NewTeamRoleResource() resource.Resource {
	return &v2Resource[teamRoleModel, teamRoleResponse]{
		typeNameSuffix:     "_team_role",
		schema:             teamRoleResourceSchema,
		errors:             teamRoleErrors,
		specificDiagnostic: roleGrantConflict(teamRoleErrors, "select_team_role", "<team_id>/<role_id>"),
		collectionEndpoint: func(m *teamRoleModel) string {
			return teamRolesEndpoint(m.TeamId.ValueString())
		},
		itemEndpoint: func(m *teamRoleModel) string {
			return teamRoleEndpoint(m.TeamId.ValueString(), m.Id.ValueString())
		},
		// There is no GET for one grant, so Read lists the team's grants and
		// finds this one by id. A grant that is not in the list is removed
		// from state.
		fetch: v2ListAndFind(
			func(m *teamRoleModel) string { return teamRolesEndpoint(m.TeamId.ValueString()) },
			func(m *teamRoleModel) string { return m.Id.ValueString() },
			func(r *teamRoleResponse) string { return r.Id },
		),
		// A team role grant has no ETag. A null Etag sends no If-Match header,
		// and the delete route does not take one.
		identity: func(m *teamRoleModel) v2Identity {
			return v2Identity{Id: m.Id, Etag: types.StringNull()}
		},
		importState:    v2ImportChild("Team Role", "team_id", "team_id/role_id"),
		createPayload:  v2FalliblePayload(buildTeamRoleCreate),
		updatePayload:  v2NoUpdate[teamRoleModel]("team role grant"),
		applyResponse:  applyTeamRoleResponse,
		validateConfig: validateTeamRoleConfig,
	}
}
