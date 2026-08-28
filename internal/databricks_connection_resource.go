// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"terraform-provider-select/internal/provider/resource_databricks_connection"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = (*databricksConnectionResource)(nil)
var _ resource.ResourceWithConfigure = (*databricksConnectionResource)(nil)
var _ resource.ResourceWithImportState = (*databricksConnectionResource)(nil)
var _ resource.ResourceWithValidateConfig = (*databricksConnectionResource)(nil)

func NewDatabricksConnectionResource() resource.Resource {
	return &databricksConnectionResource{}
}

type databricksConnectionResource struct {
	client *APIClient
}

func (r *databricksConnectionResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	providerData, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	r.client = providerData.Client
}

func (r *databricksConnectionResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_databricks_connection"
}

func (r *databricksConnectionResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resource_databricks_connection.DatabricksConnectionResourceSchema(ctx)
}

func (r *databricksConnectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan resource_databricks_connection.DatabricksConnectionModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	request := buildDatabricksConnectionCreate(&plan)

	var connection databricksConnectionResponse
	apiErr, diags := r.client.doRequest(ctx, http.MethodPost, databricksConnectionsEndpoint, request, &connection, requestOptions{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		resp.Diagnostics.Append(databricksConnectionAPIDiagnostic("add the Databricks connection", apiErr))
		return
	}

	state := plan
	resp.Diagnostics.Append(applyDatabricksConnectionResponse(ctx, &state, &plan, &connection)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *databricksConnectionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state resource_databricks_connection.DatabricksConnectionModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var connection databricksConnectionResponse
	endpoint := databricksConnectionEndpoint(state.Id.ValueString())
	apiErr, diags := r.client.doRequest(ctx, http.MethodGet, endpoint, nil, &connection, requestOptions{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		// The connection is gone, or is no longer visible to this API key. Either
		// way Terraform should plan to recreate it rather than keep stale values.
		if apiErr.StatusCode == http.StatusNotFound {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.Append(databricksConnectionAPIDiagnostic("read the Databricks connection", apiErr))
		return
	}

	refreshed := state
	resp.Diagnostics.Append(applyDatabricksConnectionResponse(ctx, &refreshed, &state, &connection)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &refreshed)...)
}

func (r *databricksConnectionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state resource_databricks_connection.DatabricksConnectionModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	request := buildDatabricksConnectionUpdate(&plan, &state)

	var connection databricksConnectionResponse
	endpoint := databricksConnectionEndpoint(state.Id.ValueString())
	apiErr, diags := r.client.doRequest(ctx, http.MethodPatch, endpoint, request, &connection, requestOptions{
		headers: ifMatchHeader(state.Etag),
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		resp.Diagnostics.Append(databricksConnectionAPIDiagnostic("update the Databricks connection", apiErr))
		return
	}

	updated := plan
	resp.Diagnostics.Append(applyDatabricksConnectionResponse(ctx, &updated, &plan, &connection)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
}

func (r *databricksConnectionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state resource_databricks_connection.DatabricksConnectionModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := databricksConnectionEndpoint(state.Id.ValueString())
	apiErr, diags := r.client.doRequest(ctx, http.MethodDelete, endpoint, nil, nil, requestOptions{
		headers: ifMatchHeader(state.Etag),
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Already gone is the outcome delete was asked for.
	if apiErr != nil && apiErr.StatusCode != http.StatusNotFound {
		resp.Diagnostics.Append(databricksConnectionAPIDiagnostic("delete the Databricks connection", apiErr))
	}
}

func (r *databricksConnectionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ValidateConfig checks the one thing the generated schema cannot. The API
// validates databricks_account_id, warehouse_id and both credential fields with
// patterns the generator carried over, but primary_workspace_url has no pattern
// on the API side: a value missing its scheme is only caught once SELECT has
// tried to connect, which costs an apply and a round trip to Databricks to learn
// something the configuration already shows.
func (r *databricksConnectionResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config resource_databricks_connection.DatabricksConnectionModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(validateWorkspaceURL(config.PrimaryWorkspaceUrl)...)
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
