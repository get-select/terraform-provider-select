// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Alert integrations live on the v2 API. An organization has at most one
// integration of each type. Email and MS Teams webhook integrations are
// enabled with an action that has no body. A Slack integration needs an
// OAuth flow in a browser, so Terraform can only look one up.
const alertIntegrationsEndpoint = "/v2/alert-integrations"

func alertIntegrationEndpoint(id string) string {
	return fmt.Sprintf("%s/%s", alertIntegrationsEndpoint, id)
}

// alertIntegrationEnableEndpoints maps each integration type that Terraform
// can enable to its enable action. The action returns the existing
// integration when there is one, so a create adopts it.
var alertIntegrationEnableEndpoints = map[string]string{
	"email":            alertIntegrationsEndpoint + "/actions/enable-email",
	"ms_teams_webhook": alertIntegrationsEndpoint + "/actions/enable-ms-teams-webhook",
}

var alertIntegrationErrors = v2ErrorFormat{
	Noun:       "Alert Integration",
	Subject:    "the alert integration",
	Object:     "the alert integration",
	ReadScope:  "alert_integrations:read",
	WriteScope: "alert_integrations:write",
}

// alertIntegrationConflict explains a delete that the integration's
// destinations block.
func alertIntegrationConflict(operation string, apiErr *apiError) diag.Diagnostic {
	if apiErr.StatusCode != http.StatusConflict || apiErr.Code != deleteBlockedByDependencies {
		return nil
	}
	return diag.NewErrorDiagnostic(
		"Alert Integration In Use",
		fmt.Sprintf("SELECT could not %s because it still has destinations. "+
			"Delete its destinations first.\n\n%s", operation, apiErr.Detail),
	)
}

// alertIntegrationResponse mirrors AlertIntegrationV2. last_verified_time and
// last_used_time are not decoded: they change with use, not with
// configuration.
type alertIntegrationResponse struct {
	Id                    string  `json:"id"`
	Etag                  string  `json:"etag"`
	DisplayName           string  `json:"display_name"`
	IntegrationType       string  `json:"integration_type"`
	Status                string  `json:"status"`
	CredentialsConfigured bool    `json:"credentials_configured"`
	CreateTime            string  `json:"create_time"`
	UpdateTime            string  `json:"update_time"`
	SlackWorkspaceId      *string `json:"slack_workspace_id"`
	SlackWorkspaceName    *string `json:"slack_workspace_name"`
}

// alertIntegrationModel is the select_alert_integration resource shape. The
// data source adds the Slack fields to it.
type alertIntegrationModel struct {
	Id                    types.String `tfsdk:"id"`
	Etag                  types.String `tfsdk:"etag"`
	IntegrationType       types.String `tfsdk:"integration_type"`
	DisplayName           types.String `tfsdk:"display_name"`
	Status                types.String `tfsdk:"status"`
	CredentialsConfigured types.Bool   `tfsdk:"credentials_configured"`
	CreateTime            types.String `tfsdk:"create_time"`
	UpdateTime            types.String `tfsdk:"update_time"`
}

// applyAlertIntegrationResponse writes a response onto the model.
func applyAlertIntegrationResponse(model *alertIntegrationModel, response *alertIntegrationResponse) {
	model.Id = types.StringValue(response.Id)
	model.Etag = types.StringValue(response.Etag)
	model.IntegrationType = types.StringValue(response.IntegrationType)
	model.DisplayName = types.StringValue(response.DisplayName)
	model.Status = types.StringValue(response.Status)
	model.CredentialsConfigured = types.BoolValue(response.CredentialsConfigured)
	model.CreateTime = types.StringValue(response.CreateTime)
	model.UpdateTime = types.StringValue(response.UpdateTime)
}
