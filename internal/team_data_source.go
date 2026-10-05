// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// teamDataSourceModel is the select_team data source's shape. It is
// hand-written: the generator makes a data source from a GET-by-id route,
// and this one looks a team up by name.
type teamDataSourceModel struct {
	Id                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	DefaultMemberRole types.String `tfsdk:"default_member_role"`
	IsAllUsers        types.Bool   `tfsdk:"is_all_users"`
	CreateTime        types.String `tfsdk:"create_time"`
	UpdateTime        types.String `tfsdk:"update_time"`
}

func NewTeamDataSource() datasource.DataSource {
	return &v2DataSource[teamDataSourceModel]{
		typeNameSuffix: "_team",
		schema:         teamDataSourceSchema,
		read:           readTeamByName,
	}
}

func teamDataSourceSchema(ctx context.Context) schema.Schema {
	return schema.Schema{
		Description: "Looks up an existing team by its name. Use it to refer to a team that " +
			"Terraform does not manage, such as the organization's built-in all-users team " +
			"or a team made in the SELECT UI.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required: true,
				Description: "The team's name. It must match exactly, including case. A team " +
					"whose name differs only in case is named in the error.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The team's identifier in SELECT.",
			},
			"default_member_role": schema.StringAttribute{
				Computed:    true,
				Description: "The default role for members added to the team.",
			},
			"is_all_users": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the team automatically includes every organization member.",
			},
			"create_time": schema.StringAttribute{
				Computed:    true,
				Description: "When the team was created — RFC 3339 UTC.",
			},
			"update_time": schema.StringAttribute{
				Computed:    true,
				Description: "When the team was last updated — RFC 3339 UTC.",
			},
		},
	}
}

// readTeamByName lists the teams that name__ilike matches and keeps the one
// with exactly the configured name. name__ilike compares without case, and
// "%" and "_" in a name act as wildcards, so the list can hold more teams
// than the one wanted. The exact match removes them. See teamNameFilter for
// the one case that sends no filter.
func readTeamByName(ctx context.Context, client *APIClient, model *teamDataSourceModel) diag.Diagnostics {
	name := model.Name.ValueString()

	candidates, apiErr, diags := v2ListAll[teamResponse](ctx, client, teamsEndpoint, teamNameFilter(name))
	if diags.HasError() {
		return diags
	}
	if apiErr != nil {
		return diag.Diagnostics{teamErrors.diagnostic("list teams", apiErr, nil)}
	}

	team, notFound := matchTeamByName(name, candidates)
	if notFound != nil {
		return diag.Diagnostics{notFound}
	}

	model.Id = types.StringValue(team.Id)
	model.Name = types.StringValue(team.Name)
	model.DefaultMemberRole = types.StringValue(team.DefaultMemberRole)
	model.IsAllUsers = types.BoolValue(team.IsAllUsers)
	model.CreateTime = types.StringValue(team.CreateTime)
	model.UpdateTime = types.StringValue(team.UpdateTime)
	return nil
}

// teamNameFilter is the list query for a lookup by name. A "%" or "_" in the
// name only makes name__ilike match more teams, which the exact match then
// removes. A backslash can be an escape character in a LIKE pattern, and the
// API does not document how it treats one, so it could make the filter miss
// the team. For a name that holds a backslash, the lookup sends no filter and
// matches over the full list of teams.
func teamNameFilter(name string) url.Values {
	if strings.Contains(name, `\`) {
		return nil
	}
	return url.Values{"name__ilike": {name}}
}

// matchTeamByName returns the one candidate whose name is exactly name. It
// returns a diagnostic when no candidate or more than one candidate matches.
// When nothing matches exactly, the diagnostic names the candidates that
// match without case, which is the usual cause.
func matchTeamByName(name string, candidates []teamResponse) (*teamResponse, diag.Diagnostic) {
	var exact []*teamResponse
	var folded []string
	for i := range candidates {
		switch {
		case candidates[i].Name == name:
			exact = append(exact, &candidates[i])
		case strings.EqualFold(candidates[i].Name, name):
			folded = append(folded, fmt.Sprintf("%q", candidates[i].Name))
		}
	}

	switch len(exact) {
	case 1:
		return exact[0], nil
	case 0:
		detail := fmt.Sprintf("SELECT has no team named %q.", name)
		if len(folded) > 0 {
			detail += fmt.Sprintf(" These teams have the same name in a different case: %s.", strings.Join(folded, ", "))
		}
		return nil, diag.NewErrorDiagnostic("Team Not Found", detail)
	default:
		ids := make([]string, len(exact))
		for i, team := range exact {
			ids[i] = team.Id
		}
		return nil, diag.NewErrorDiagnostic(
			"Multiple Teams Found",
			fmt.Sprintf("SELECT has %d teams named %q (ids %s). Rename one of them so the name finds one team.",
				len(exact), name, strings.Join(ids, ", ")),
		)
	}
}
