// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"terraform-provider-select/internal/provider/resource_team_member"
)

// Team members live on the v2 API, nested under their team. The API has a
// list route and per-member PATCH and DELETE routes, but no GET for one
// member, so the resource reads a member from the list. See
// team_member_resource.go.
func teamMembersEndpoint(teamId string) string {
	return fmt.Sprintf("%s/members", teamEndpoint(teamId))
}

func teamMemberEndpoint(teamId, id string) string {
	return fmt.Sprintf("%s/%s", teamMembersEndpoint(teamId), id)
}

// teamMemberErrors words the failures every v2 resource can hit. The member
// routes use the team scopes.
var teamMemberErrors = v2ErrorFormat{
	Noun:       "Team Member",
	Subject:    "the team member",
	Object:     "the team member",
	ReadScope:  "teams:read",
	WriteScope: "teams:write",
}

// teamMemberCreatePayload mirrors TeamMemberCreateV2. role has a schema
// default, so the plan always holds a value for it and it is always sent.
type teamMemberCreatePayload struct {
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
	Role       string `json:"role"`
}

// teamMemberUpdatePayload mirrors TeamMemberUpdateV2. role is the only field
// a member can change; every other attribute forces a new member.
type teamMemberUpdatePayload struct {
	Role *string `json:"role,omitempty"`
}

// teamMemberResponse mirrors TeamMemberV2. It does not carry the team's id:
// the route holds it.
type teamMemberResponse struct {
	Id         string `json:"id"`
	Etag       string `json:"etag"`
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
	Role       string `json:"role"`
	CreateTime string `json:"create_time"`
}

func buildTeamMemberCreate(plan *resource_team_member.TeamMemberModel) *teamMemberCreatePayload {
	return &teamMemberCreatePayload{
		Type:       plan.Type.ValueString(),
		Identifier: plan.Identifier.ValueString(),
		Role:       plan.Role.ValueString(),
	}
}

func buildTeamMemberUpdate(plan, state *resource_team_member.TeamMemberModel) *teamMemberUpdatePayload {
	return &teamMemberUpdatePayload{
		Role: changedString(plan.Role, state.Role),
	}
}

// applyTeamMemberResponse writes an API response onto the model. team_id
// comes from source, because the response does not carry it.
//
// identifier keeps its configured spelling when the API returns it in a
// different case. An email address is compared without case almost
// everywhere, and if the API stores it in lower case, a configured
// "Alice@example.com" would otherwise fail every apply with an
// inconsistent-result error.
func applyTeamMemberResponse(ctx context.Context, model, source *resource_team_member.TeamMemberModel, response *teamMemberResponse) diag.Diagnostics {
	model.Id = types.StringValue(response.Id)
	model.Etag = types.StringValue(response.Etag)
	model.TeamId = source.TeamId
	model.Type = types.StringValue(response.Type)
	model.Identifier = preserveEquivalentFold(source.Identifier, &response.Identifier)
	model.Role = types.StringValue(response.Role)
	model.CreateTime = types.StringValue(response.CreateTime)

	return nil
}
