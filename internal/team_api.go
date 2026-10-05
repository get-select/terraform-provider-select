// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"terraform-provider-select/internal/provider/resource_team"
)

// Teams live on the v2 API. See v2_api.go for the conventions every resource
// on that surface shares.
const teamsEndpoint = "/v2/teams"

// teamEndpoint escapes the id as one path segment. A user-supplied id with a
// "/" or a space then stays one segment, and makeRequest keeps the escaped
// form intact on the wire.
func teamEndpoint(id string) string {
	return fmt.Sprintf("%s/%s", teamsEndpoint, url.PathEscape(id))
}

// teamErrors words the failures every v2 resource can hit. A team has no
// specific conflict handling: the shared conflict diagnostic names the field
// the API reports, which for a team is its name.
var teamErrors = v2ErrorFormat{
	Noun:       "Team",
	Subject:    "the team",
	Object:     "the team",
	ReadScope:  "teams:read",
	WriteScope: "teams:write",
}

// teamCreatePayload mirrors TeamCreateV2. default_member_role and is_all_users
// have schema defaults, so the plan always holds a value for them and they are
// always sent.
type teamCreatePayload struct {
	Name              string `json:"name"`
	DefaultMemberRole string `json:"default_member_role"`
	IsAllUsers        bool   `json:"is_all_users"`
}

// teamUpdatePayload mirrors TeamUpdateV2, a JSON Merge Patch body. Each field
// is sent only when it changed. No field on a team can be cleared, so a plain
// pointer is enough.
type teamUpdatePayload struct {
	Name              *string `json:"name,omitempty"`
	DefaultMemberRole *string `json:"default_member_role,omitempty"`
	IsAllUsers        *bool   `json:"is_all_users,omitempty"`
}

// teamResponse mirrors TeamV2.
type teamResponse struct {
	Id                string `json:"id"`
	Etag              string `json:"etag"`
	Name              string `json:"name"`
	IsAllUsers        bool   `json:"is_all_users"`
	DefaultMemberRole string `json:"default_member_role"`
	CreateTime        string `json:"create_time"`
	UpdateTime        string `json:"update_time"`
}

func buildTeamCreate(plan *resource_team.TeamModel) *teamCreatePayload {
	return &teamCreatePayload{
		Name:              plan.Name.ValueString(),
		DefaultMemberRole: plan.DefaultMemberRole.ValueString(),
		IsAllUsers:        plan.IsAllUsers.ValueBool(),
	}
}

// buildTeamUpdate carries only the fields whose configured value differs from
// what state records.
func buildTeamUpdate(plan, state *resource_team.TeamModel) *teamUpdatePayload {
	return &teamUpdatePayload{
		Name:              changedString(plan.Name, state.Name),
		DefaultMemberRole: changedString(plan.DefaultMemberRole, state.DefaultMemberRole),
		IsAllUsers:        changedBool(plan.IsAllUsers, state.IsAllUsers),
	}
}

// applyTeamResponse writes an API response onto the model. Every field comes
// straight from the response: the API stores each one as it was sent.
func applyTeamResponse(ctx context.Context, model, source *resource_team.TeamModel, response *teamResponse) diag.Diagnostics {
	model.Id = types.StringValue(response.Id)
	model.Etag = types.StringValue(response.Etag)
	model.Name = types.StringValue(response.Name)
	model.IsAllUsers = types.BoolValue(response.IsAllUsers)
	model.DefaultMemberRole = types.StringValue(response.DefaultMemberRole)
	model.CreateTime = types.StringValue(response.CreateTime)
	model.UpdateTime = types.StringValue(response.UpdateTime)

	return nil
}
