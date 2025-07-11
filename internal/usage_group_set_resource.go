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
	// Prevent panic if the provider has not been configured.
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
	// Use the code-generated schema
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

	// Get the planned changes
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get the current state (which contains the ID)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Create the update model by merging plan with state ID
	updateModel := resource_usage_group_set.UsageGroupSetModel{
		Id:                        state.Id,                       // ID from current state
		Name:                      plan.Name,                      // Updated name from plan
		Order:                     plan.Order,                     // Updated order from plan
		OrganizationId:            state.OrganizationId,           // Keep from state
		SnowflakeAccountUuid:      plan.SnowflakeAccountUuid,      // Updated value from plan
		SnowflakeOrganizationName: plan.SnowflakeOrganizationName, // Updated value from plan
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
	// Retrieve import ID and save to id attribute
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// createUsageGroupSet handles the creation of a new usage group set
// Uses the correct API route with organization_id path parameter
func createUsageGroupSet(ctx context.Context, model *resource_usage_group_set.UsageGroupSetModel, client *APIClient) diag.Diagnostics {
	// Get organization_id from the client (configured at provider level)
	orgId := client.GetOrganizationId()

	// Create a copy of the model without organization_id for the request body
	// (organization_id goes in the URL path only, not the JSON payload)
	requestModel := resource_usage_group_set.UsageGroupSetModel{
		Name:                      model.Name,
		Order:                     model.Order,
		SnowflakeAccountUuid:      model.SnowflakeAccountUuid,
		SnowflakeOrganizationName: model.SnowflakeOrganizationName,
		// Explicitly exclude: Id (computed) and OrganizationId (URL path only)
	}

	// Use the correct API route: POST /api/{organization_id}/usage-group-sets
	endpoint := fmt.Sprintf("/api/%s/usage-group-sets", orgId)
	diags := client.Post(ctx, endpoint, &requestModel, model)

	// After successful creation, set the organization_id in the response model
	if !diags.HasError() {
		model.OrganizationId = types.StringValue(orgId)
	}

	return diags
}

// readUsageGroupSet handles reading an existing usage group set from the API
// Uses the correct API route with organization_id and usage_group_set_id path parameters
func readUsageGroupSet(ctx context.Context, model *resource_usage_group_set.UsageGroupSetModel, client *APIClient) diag.Diagnostics {
	// Get organization_id from the client (configured at provider level)
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

	// Use the correct API route: GET /api/{organization_id}/usage-group-sets/{usage_group_set_id}
	endpoint := fmt.Sprintf("/api/%s/usage-group-sets/%s", orgId, usageGroupSetId)
	diags := client.Get(ctx, endpoint, model)

	// After successful read, ensure organization_id is set in the response model
	if !diags.HasError() {
		model.OrganizationId = types.StringValue(orgId)
	}

	return diags
}

// updateUsageGroupSet handles the update of an existing usage group set
// Uses the correct API route with organization_id and usage_group_set_id path parameters
func updateUsageGroupSet(ctx context.Context, model *resource_usage_group_set.UsageGroupSetModel, client *APIClient) diag.Diagnostics {
	// Get organization_id from the client (configured at provider level)
	orgId := client.GetOrganizationId()
	usageGroupSetId := model.Id.ValueString()

	if usageGroupSetId == "" {
		return diag.Diagnostics{
			diag.NewErrorDiagnostic(
				"Missing Usage Group Set ID",
				"usage_group_set_id is required but was not found in the plan.",
			),
		}
	}

	// Create a copy of the model with only fields allowed in UsageGroupSetUpdate schema
	// According to OpenAPI spec: only id, name, and order are allowed for updates
	requestModel := resource_usage_group_set.UsageGroupSetModel{
		Id:    model.Id,
		Name:  model.Name,
		Order: model.Order,
		// Exclude: OrganizationId (URL path only), SnowflakeAccountUuid, SnowflakeOrganizationName (not allowed in update)
	}

	// Use the correct API route: PUT /api/{organization_id}/usage-group-sets/{usage_group_set_id}
	endpoint := fmt.Sprintf("/api/%s/usage-group-sets/%s", orgId, usageGroupSetId)
	diags := client.Put(ctx, endpoint, &requestModel, model)

	// After successful update, ensure organization_id is set in the response model
	if !diags.HasError() {
		model.OrganizationId = types.StringValue(orgId)
	}

	return diags
}

// deleteUsageGroupSet handles the deletion of a usage group set
// Uses the correct API route with organization_id and usage_group_set_id path parameters
func deleteUsageGroupSet(ctx context.Context, model *resource_usage_group_set.UsageGroupSetModel, client *APIClient) diag.Diagnostics {
	// Get organization_id from the client (configured at provider level)
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

	// Use the correct API route: DELETE /api/{organization_id}/usage-group-sets/{usage_group_set_id}
	endpoint := fmt.Sprintf("/api/%s/usage-group-sets/%s", orgId, usageGroupSetId)
	return client.Delete(ctx, endpoint)
}
