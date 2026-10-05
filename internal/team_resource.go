// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"terraform-provider-select/internal/provider/resource_team"
)

func NewTeamResource() resource.Resource {
	return &v2Resource[resource_team.TeamModel, teamResponse]{
		typeNameSuffix:     "_team",
		schema:             resource_team.TeamResourceSchema,
		errors:             teamErrors,
		specificDiagnostic: nil,
		collectionEndpoint: func(*resource_team.TeamModel) string { return teamsEndpoint },
		itemEndpoint: func(m *resource_team.TeamModel) string {
			return teamEndpoint(m.Id.ValueString())
		},
		identity: func(m *resource_team.TeamModel) v2Identity {
			return v2Identity{Id: m.Id, Etag: m.Etag}
		},
		createPayload: v2Payload(buildTeamCreate),
		updatePayload: v2Patch(buildTeamUpdate),
		applyResponse: applyTeamResponse,
	}
}
