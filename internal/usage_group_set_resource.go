// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"terraform-provider-select/internal/provider/resource_usage_group_set"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = (*usageGroupSetResource)(nil)
var _ resource.ResourceWithConfigure = (*usageGroupSetResource)(nil)
var _ resource.ResourceWithImportState = (*usageGroupSetResource)(nil)

func NewUsageGroupSetResource() resource.Resource {
	return &usageGroupSetResource{}
}

type usageGroupSetResource struct {
	client *APIClient
}

func (r *usageGroupSetResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *usageGroupSetResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_usage_group_set"
}

func (r *usageGroupSetResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resource_usage_group_set.UsageGroupSetResourceSchema(ctx)
}

func (r *usageGroupSetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data resource_usage_group_set.UsageGroupSetModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(createUsageGroupSet(ctx, &data, r.client)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *usageGroupSetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data resource_usage_group_set.UsageGroupSetModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(readUsageGroupSet(ctx, &data, r.client)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *usageGroupSetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan resource_usage_group_set.UsageGroupSetModel
	var state resource_usage_group_set.UsageGroupSetModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateModel := resource_usage_group_set.UsageGroupSetModel{
		Id:             state.Id,
		Name:           plan.Name,
		Order:          plan.Order,
		OrganizationId: state.OrganizationId,
		// Scope fields (SnowflakeAccountUuid, SnowflakeOrganizationName, TeamId) are immutable
		// per the API spec and cannot be changed after creation
	}

	resp.Diagnostics.Append(updateUsageGroupSet(ctx, &updateModel, r.client)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &updateModel)...)
}

func (r *usageGroupSetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data resource_usage_group_set.UsageGroupSetModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(deleteUsageGroupSet(ctx, &data, r.client)...)
}

func (r *usageGroupSetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func createUsageGroupSet(ctx context.Context, model *resource_usage_group_set.UsageGroupSetModel, client *APIClient) diag.Diagnostics {
	orgId := client.GetOrganizationId()
	
	requestModel := resource_usage_group_set.UsageGroupSetModel{
		Name:                      model.Name,
		Order:                     model.Order,
		SnowflakeAccountUuid:      model.SnowflakeAccountUuid,
		SnowflakeOrganizationName: model.SnowflakeOrganizationName,
		TeamId:                    model.TeamId,
	}

	endpoint := fmt.Sprintf("/api/%s/usage-group-sets", orgId)
	diags := client.Post(ctx, endpoint, &requestModel, model)

	if !diags.HasError() {
		model.OrganizationId = types.StringValue(orgId)
	}

	return diags
}

func readUsageGroupSet(ctx context.Context, model *resource_usage_group_set.UsageGroupSetModel, client *APIClient) diag.Diagnostics {
	orgId := client.GetOrganizationId()
	usageGroupSetId := model.Id.ValueString()

	if usageGroupSetId == "" {
		return diag.Diagnostics{
			diag.NewErrorDiagnostic(
				"Missing Usage Group Set ID",
				"usage_group_set_id is required but was not found in the state.",
			),
		}
	}

	endpoint := fmt.Sprintf("/api/%s/usage-group-sets/%s", orgId, usageGroupSetId)
	diags := client.Get(ctx, endpoint, model)

	if !diags.HasError() {
		model.OrganizationId = types.StringValue(orgId)
	}

	return diags
}

func updateUsageGroupSet(ctx context.Context, model *resource_usage_group_set.UsageGroupSetModel, client *APIClient) diag.Diagnostics {
	orgId := client.GetOrganizationId()
	usageGroupSetId := model.Id.ValueString()
	
	_, versionDiags := client.GetOrCreateVersion(ctx, usageGroupSetId)
	if versionDiags.HasError() {
		return versionDiags
	}

	if usageGroupSetId == "" {
		return diag.Diagnostics{
			diag.NewErrorDiagnostic(
				"Missing Usage Group Set ID",
				"usage_group_set_id is required but was not found in the plan.",
			),
		}
	}

	// Create a copy of the model with only fields allowed in UsageGroupSetUpdate schema
	// Only id, name, and order are allowed for updates per OpenAPI spec
	requestModel := resource_usage_group_set.UsageGroupSetModel{
		Id:    model.Id,
		Name:  model.Name,
		Order: model.Order,
	}

	endpoint := fmt.Sprintf("/api/%s/usage-group-sets/%s", orgId, usageGroupSetId)
	diags := client.Put(ctx, endpoint, &requestModel, model)

	if !diags.HasError() {
		model.OrganizationId = types.StringValue(orgId)
	}

	return diags
}

func deleteUsageGroupSet(ctx context.Context, model *resource_usage_group_set.UsageGroupSetModel, client *APIClient) diag.Diagnostics {
	orgId := client.GetOrganizationId()
	usageGroupSetId := model.Id.ValueString()

	if usageGroupSetId == "" {
		return diag.Diagnostics{
			diag.NewErrorDiagnostic(
				"Missing Usage Group Set ID",
				"usage_group_set_id is required but was not found in the state.",
			),
		}
	}

	endpoint := fmt.Sprintf("/api/%s/usage-group-sets/%s", orgId, usageGroupSetId)
	return client.Delete(ctx, endpoint)
}
