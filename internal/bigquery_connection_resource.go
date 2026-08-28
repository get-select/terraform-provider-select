// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"terraform-provider-select/internal/provider/resource_bigquery_connection"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = (*bigQueryConnectionResource)(nil)
var _ resource.ResourceWithConfigure = (*bigQueryConnectionResource)(nil)
var _ resource.ResourceWithImportState = (*bigQueryConnectionResource)(nil)
var _ resource.ResourceWithValidateConfig = (*bigQueryConnectionResource)(nil)

func NewBigQueryConnectionResource() resource.Resource {
	return &bigQueryConnectionResource{}
}

type bigQueryConnectionResource struct {
	client *APIClient
}

func (r *bigQueryConnectionResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *bigQueryConnectionResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bigquery_connection"
}

func (r *bigQueryConnectionResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resource_bigquery_connection.BigqueryConnectionResourceSchema(ctx)
}

func (r *bigQueryConnectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan resource_bigquery_connection.BigqueryConnectionModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	request := buildBigQueryConnectionCreate(&plan)

	var connection bigQueryConnectionResponse
	apiErr, diags := r.client.doRequest(ctx, http.MethodPost, bigQueryConnectionsEndpoint, request, &connection, requestOptions{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		resp.Diagnostics.Append(bigQueryConnectionAPIDiagnostic("add the BigQuery connection", plan.IsDoit.ValueBool(), apiErr))
		return
	}

	state := plan
	resp.Diagnostics.Append(applyBigQueryConnectionResponse(ctx, &state, &plan, &connection)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *bigQueryConnectionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state resource_bigquery_connection.BigqueryConnectionModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var connection bigQueryConnectionResponse
	endpoint := bigQueryConnectionEndpoint(state.Id.ValueString())
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
		resp.Diagnostics.Append(bigQueryConnectionAPIDiagnostic("read the BigQuery connection", false, apiErr))
		return
	}

	refreshed := state
	resp.Diagnostics.Append(applyBigQueryConnectionResponse(ctx, &refreshed, &state, &connection)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &refreshed)...)
}

func (r *bigQueryConnectionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state resource_bigquery_connection.BigqueryConnectionModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	request := buildBigQueryConnectionUpdate(&plan, &state)

	var connection bigQueryConnectionResponse
	endpoint := bigQueryConnectionEndpoint(state.Id.ValueString())
	apiErr, diags := r.client.doRequest(ctx, http.MethodPatch, endpoint, request, &connection, requestOptions{
		headers: ifMatchHeader(state.Etag),
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		resp.Diagnostics.Append(bigQueryConnectionAPIDiagnostic("update the BigQuery connection", false, apiErr))
		return
	}

	updated := plan
	resp.Diagnostics.Append(applyBigQueryConnectionResponse(ctx, &updated, &plan, &connection)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
}

func (r *bigQueryConnectionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state resource_bigquery_connection.BigqueryConnectionModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := bigQueryConnectionEndpoint(state.Id.ValueString())
	apiErr, diags := r.client.doRequest(ctx, http.MethodDelete, endpoint, nil, nil, requestOptions{
		headers: ifMatchHeader(state.Etag),
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Already gone is the outcome delete was asked for.
	if apiErr != nil && apiErr.StatusCode != http.StatusNotFound {
		resp.Diagnostics.Append(bigQueryConnectionAPIDiagnostic("delete the BigQuery connection", false, apiErr))
	}
}

func (r *bigQueryConnectionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ValidateConfig checks the one cross-field rule the generated schema cannot
// express: whether bigquery_dataset_id and billing_account_id are required or
// forbidden depends on is_doit, which a plain attribute-level validator cannot
// see. Mirrors the API's own _check_field_combinations validator on
// BigQueryConnectionCreate.
func (r *bigQueryConnectionResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config resource_bigquery_connection.BigqueryConnectionModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(validateDatasetAndBillingAccount(config.IsDoit, config.BigqueryDatasetId, config.BillingAccountId)...)
}

func validateDatasetAndBillingAccount(isDoit types.Bool, datasetId, billingAccountId types.String) diag.Diagnostics {
	var diags diag.Diagnostics

	if isDoit.IsUnknown() || datasetId.IsUnknown() || billingAccountId.IsUnknown() {
		return diags
	}

	// is_doit has a schema default of false, applied during plan modification
	// rather than config validation, so an omitted is_doit is null here and has
	// to be read as false to match what the API will actually receive.
	doit := !isDoit.IsNull() && isDoit.ValueBool()

	if doit {
		var set []string
		if !datasetId.IsNull() {
			set = append(set, "bigquery_dataset_id")
		}
		if !billingAccountId.IsNull() {
			set = append(set, "billing_account_id")
		}
		if len(set) > 0 {
			diags.AddError(
				"Fields Not Allowed on a DoiT-Managed Connection",
				fmt.Sprintf("%s must be omitted when is_doit is true.", joinAnd(set)),
			)
		}
		return diags
	}

	var missing []string
	if datasetId.IsNull() {
		missing = append(missing, "bigquery_dataset_id")
	}
	if billingAccountId.IsNull() {
		missing = append(missing, "billing_account_id")
	}
	if len(missing) > 0 {
		diags.AddError(
			"Missing Required Fields",
			fmt.Sprintf("%s is required when is_doit is false.", joinAnd(missing)),
		)
	}
	return diags
}

func joinAnd(fields []string) string {
	switch len(fields) {
	case 1:
		return fields[0]
	default:
		return fields[0] + " and " + fields[1]
	}
}
