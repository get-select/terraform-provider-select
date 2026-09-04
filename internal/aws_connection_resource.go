// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"terraform-provider-select/internal/provider/resource_aws_connection"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// Unlike the other v2 connection resources this one implements no
// ValidateConfig: the AWS create model has no cross-field rule for one to
// enforce, and every constraint the API does impose is a per-attribute pattern
// the generated schema already carries.
var _ resource.Resource = (*awsConnectionResource)(nil)
var _ resource.ResourceWithConfigure = (*awsConnectionResource)(nil)
var _ resource.ResourceWithImportState = (*awsConnectionResource)(nil)

func NewAwsConnectionResource() resource.Resource {
	return &awsConnectionResource{}
}

type awsConnectionResource struct {
	client *APIClient
}

func (r *awsConnectionResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *awsConnectionResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_aws_connection"
}

func (r *awsConnectionResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resource_aws_connection.AwsConnectionResourceSchema(ctx)
}

func (r *awsConnectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan resource_aws_connection.AwsConnectionModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	request := buildAwsConnectionCreate(&plan)

	var connection awsConnectionResponse
	apiErr, diags := r.client.doRequest(ctx, http.MethodPost, awsAccountsEndpoint, request, &connection, requestOptions{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		resp.Diagnostics.Append(awsConnectionAPIDiagnostic("add the AWS connection", apiErr))
		return
	}

	state := plan
	resp.Diagnostics.Append(applyAwsConnectionResponse(ctx, &state, &plan, &connection)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *awsConnectionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state resource_aws_connection.AwsConnectionModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var connection awsConnectionResponse
	endpoint := awsConnectionEndpoint(state.Id.ValueString())
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
		resp.Diagnostics.Append(awsConnectionAPIDiagnostic("read the AWS connection", apiErr))
		return
	}

	refreshed := state
	resp.Diagnostics.Append(applyAwsConnectionResponse(ctx, &refreshed, &state, &connection)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &refreshed)...)
}

func (r *awsConnectionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state resource_aws_connection.AwsConnectionModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	request := buildAwsConnectionUpdate(&plan, &state)

	var connection awsConnectionResponse
	endpoint := awsConnectionEndpoint(state.Id.ValueString())
	apiErr, diags := r.client.doRequest(ctx, http.MethodPatch, endpoint, request, &connection, requestOptions{
		headers: ifMatchHeader(state.Etag),
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		resp.Diagnostics.Append(awsConnectionAPIDiagnostic("update the AWS connection", apiErr))
		return
	}

	updated := plan
	resp.Diagnostics.Append(applyAwsConnectionResponse(ctx, &updated, &plan, &connection)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
}

func (r *awsConnectionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state resource_aws_connection.AwsConnectionModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := awsConnectionEndpoint(state.Id.ValueString())
	apiErr, diags := r.client.doRequest(ctx, http.MethodDelete, endpoint, nil, nil, requestOptions{
		headers: ifMatchHeader(state.Etag),
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Already gone is the outcome delete was asked for.
	if apiErr != nil && apiErr.StatusCode != http.StatusNotFound {
		resp.Diagnostics.Append(awsConnectionAPIDiagnostic("delete the AWS connection", apiErr))
	}
}

func (r *awsConnectionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
