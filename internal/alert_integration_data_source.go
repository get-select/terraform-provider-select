// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// alertIntegrationDataSourceModel is the select_alert_integration data source
// shape: the resource's attributes and the Slack workspace.
type alertIntegrationDataSourceModel struct {
	alertIntegrationModel
	SlackWorkspaceId   types.String `tfsdk:"slack_workspace_id"`
	SlackWorkspaceName types.String `tfsdk:"slack_workspace_name"`
}

func NewAlertIntegrationDataSource() datasource.DataSource {
	return &v2DataSource[alertIntegrationDataSourceModel]{
		typeNameSuffix: "_alert_integration",
		schema:         alertIntegrationDataSourceSchema,
		read:           readAlertIntegration,
	}
}

func alertIntegrationDataSourceSchema(ctx context.Context) schema.Schema {
	return schema.Schema{
		Description: "Finds an alert integration of any type, Slack included. Use its id as the " +
			"`integration_id` of an alert destination. The filters must match exactly one integration.",
		Attributes: map[string]schema.Attribute{
			"integration_type": schema.StringAttribute{
				Required:    true,
				Description: "`slack`, `email` or `ms_teams_webhook`.",
				Validators: []validator.String{
					stringvalidator.OneOf("slack", "email", "ms_teams_webhook"),
				},
			},
			"display_name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Keep only the integration with this display name. Must match exactly.",
			},
			"slack_workspace_name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Keep only the Slack integration for this workspace. Must match exactly.",
			},
			"slack_workspace_id": schema.StringAttribute{
				Computed:    true,
				Description: "The id of the connected Slack workspace. Null for other types.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The integration's identifier in SELECT.",
			},
			"etag": schema.StringAttribute{
				Computed:    true,
				Description: "Opaque strong ETag for this integration.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "The status of SELECT's connection: `unknown`, `pending`, `failing` or `connected`.",
			},
			"credentials_configured": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether SELECT holds credentials for this integration.",
			},
			"create_time": schema.StringAttribute{
				Computed:    true,
				Description: "When the integration was connected — RFC 3339 UTC.",
			},
			"update_time": schema.StringAttribute{
				Computed:    true,
				Description: "When the integration was last changed — RFC 3339 UTC.",
			},
		},
	}
}

// readAlertIntegration lists the integrations of the configured type and
// keeps the one that matches the optional filters.
func readAlertIntegration(ctx context.Context, client *APIClient, model *alertIntegrationDataSourceModel) diag.Diagnostics {
	integrationType := model.IntegrationType.ValueString()

	candidates, apiErr, diags := v2ListAll[alertIntegrationResponse](ctx, client, alertIntegrationsEndpoint,
		url.Values{"integration_type": {integrationType}})
	if diags.HasError() {
		return diags
	}
	if apiErr != nil {
		return diag.Diagnostics{alertIntegrationErrors.diagnostic("list alert integrations", apiErr, nil)}
	}

	integration, notFound := matchAlertIntegration(integrationType, model.DisplayName, model.SlackWorkspaceName, candidates)
	if notFound != nil {
		return diag.Diagnostics{notFound}
	}

	applyAlertIntegrationResponse(&model.alertIntegrationModel, integration)
	model.SlackWorkspaceId = stringValue(integration.SlackWorkspaceId)
	model.SlackWorkspaceName = stringValue(integration.SlackWorkspaceName)
	return nil
}

// matchAlertIntegration returns the one candidate of integrationType that
// matches the filters that are set. It returns a diagnostic that names the
// candidates when no candidate or more than one candidate matches.
func matchAlertIntegration(integrationType string, displayName, slackWorkspaceName types.String, candidates []alertIntegrationResponse) (*alertIntegrationResponse, diag.Diagnostic) {
	var matches []*alertIntegrationResponse
	var names []string
	for i := range candidates {
		candidate := &candidates[i]
		if candidate.IntegrationType != integrationType {
			continue
		}
		names = append(names, describeAlertIntegration(candidate))
		if known(displayName) && candidate.DisplayName != displayName.ValueString() {
			continue
		}
		if known(slackWorkspaceName) &&
			(candidate.SlackWorkspaceName == nil || *candidate.SlackWorkspaceName != slackWorkspaceName.ValueString()) {
			continue
		}
		matches = append(matches, candidate)
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		detail := fmt.Sprintf("SELECT has no %s alert integration that matches the filters.", integrationType)
		if len(names) > 0 {
			detail += " These integrations of that type exist: " + strings.Join(names, ", ") + "."
		}
		return nil, diag.NewErrorDiagnostic("Alert Integration Not Found", detail)
	default:
		found := make([]string, len(matches))
		for i, match := range matches {
			found[i] = describeAlertIntegration(match)
		}
		return nil, diag.NewErrorDiagnostic(
			"Multiple Alert Integrations Found",
			fmt.Sprintf("%d %s alert integrations match: %s. Set display_name or "+
				"slack_workspace_name so that the filters match one integration.",
				len(matches), integrationType, strings.Join(found, ", ")),
		)
	}
}

// describeAlertIntegration names an integration in a diagnostic.
func describeAlertIntegration(integration *alertIntegrationResponse) string {
	description := fmt.Sprintf("%q (id %s", integration.DisplayName, integration.Id)
	if integration.SlackWorkspaceName != nil {
		description += fmt.Sprintf(", workspace %q", *integration.SlackWorkspaceName)
	}
	return description + ")"
}
