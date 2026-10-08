// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"terraform-provider-select/internal/provider/resource_team_member"
)

func NewTeamMemberResource() resource.Resource {
	return &v2Resource[resource_team_member.TeamMemberModel, teamMemberResponse]{
		typeNameSuffix:     "_team_member",
		schema:             resource_team_member.TeamMemberResourceSchema,
		errors:             teamMemberErrors,
		specificDiagnostic: nil,
		collectionEndpoint: func(m *resource_team_member.TeamMemberModel) string {
			return teamMembersEndpoint(m.TeamId.ValueString())
		},
		itemEndpoint: func(m *resource_team_member.TeamMemberModel) string {
			return teamMemberEndpoint(m.TeamId.ValueString(), m.Id.ValueString())
		},
		// There is no GET for one member, so Read lists the team's members and
		// finds this one by id. A member that is not in the list is removed
		// from state.
		fetch: v2ListAndFind(
			func(m *resource_team_member.TeamMemberModel) string {
				return teamMembersEndpoint(m.TeamId.ValueString())
			},
			func(m *resource_team_member.TeamMemberModel) string { return m.Id.ValueString() },
			func(r *teamMemberResponse) string { return r.Id },
		),
		identity: func(m *resource_team_member.TeamMemberModel) v2Identity {
			return v2Identity{Id: m.Id, Etag: m.Etag}
		},
		importState:   v2ImportChild("Team Member", "team_id", "team_id/member_id"),
		createPayload: v2Payload(buildTeamMemberCreate),
		updatePayload: v2Patch(buildTeamMemberUpdate),
		applyResponse: applyTeamMemberResponse,
	}
}
