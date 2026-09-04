// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/url"
	"terraform-provider-select/internal/provider/resource_databricks_connection"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func NewDatabricksConnectionResource() resource.Resource {
	return &v2Resource[resource_databricks_connection.DatabricksConnectionModel, databricksConnectionResponse]{
		typeNameSuffix:     "_databricks_connection",
		schema:             resource_databricks_connection.DatabricksConnectionResourceSchema,
		errors:             databricksConnectionErrors,
		specificDiagnostic: databricksConnectionSpecificDiagnostic,
		collectionEndpoint: databricksConnectionsEndpoint,
		itemEndpoint:       databricksConnectionEndpoint,
		identity: func(m *resource_databricks_connection.DatabricksConnectionModel) v2Identity {
			return v2Identity{Id: m.Id, Etag: m.Etag}
		},
		createPayload:  v2Payload(buildDatabricksConnectionCreate),
		updatePayload:  v2Patch(buildDatabricksConnectionUpdate),
		applyResponse:  applyDatabricksConnectionResponse,
		validateConfig: validateDatabricksConnectionConfig,
	}
}

// validateDatabricksConnectionConfig checks the one thing the generated schema
// cannot. The API validates databricks_account_id, warehouse_id and both
// credential fields with patterns the generator carried over, but
// primary_workspace_url has no pattern on the API side: a value missing its
// scheme is only caught once SELECT has tried to connect, which costs an apply
// and a round trip to Databricks to learn something the configuration already
// shows.
func validateDatabricksConnectionConfig(ctx context.Context, config *resource_databricks_connection.DatabricksConnectionModel) diag.Diagnostics {
	return validateWorkspaceURL(config.PrimaryWorkspaceUrl)
}

func validateWorkspaceURL(workspaceURL types.String) diag.Diagnostics {
	var diags diag.Diagnostics

	if workspaceURL.IsNull() || workspaceURL.IsUnknown() {
		return diags
	}

	parsed, err := url.Parse(workspaceURL.ValueString())
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		diags.AddAttributeError(
			path.Root("primary_workspace_url"),
			"Invalid Workspace URL",
			"primary_workspace_url must be an absolute URL including the scheme, "+
				"for example https://my-workspace.cloud.databricks.com.",
		)
	}

	return diags
}
