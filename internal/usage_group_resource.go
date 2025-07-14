// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"
	"terraform-provider-select/internal/provider/resource_usage_group"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = (*usageGroupResource)(nil)
var _ resource.ResourceWithConfigure = (*usageGroupResource)(nil)
var _ resource.ResourceWithImportState = (*usageGroupResource)(nil)

func NewUsageGroupResource() resource.Resource {
	return &usageGroupResource{}
}

type usageGroupResource struct {
	client *APIClient
}

func (r *usageGroupResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *usageGroupResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_usage_group"
}

func (r *usageGroupResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	// Use the code-generated schema as base and override usage_group_set_id to be required
	baseSchema := resource_usage_group.UsageGroupResourceSchema(ctx)

	resp.Schema = baseSchema
}

func (r *usageGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data resource_usage_group.UsageGroupModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(createUsageGroup(ctx, &data, r.client)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *usageGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data resource_usage_group.UsageGroupModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(readUsageGroup(ctx, &data, r.client)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *usageGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan resource_usage_group.UsageGroupModel
	var state resource_usage_group.UsageGroupModel

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

	// Create the update model by merging plan with state ID and computed fields
	updateModel := resource_usage_group.UsageGroupModel{
		Id:                   state.Id,                  // ID from current state
		Name:                 plan.Name,                 // Updated name from plan
		Order:                plan.Order,                // Updated order from plan
		Budget:               plan.Budget,               // Updated budget from plan
		FilterExpressionJson: plan.FilterExpressionJson, // Updated filter expression from plan
		OrganizationId:       state.OrganizationId,      // Keep from state
		UsageGroupSetId:      state.UsageGroupSetId,     // Keep from state
		UsageGroupSetName:    state.UsageGroupSetName,   // Keep from state
		CreatedAt:            state.CreatedAt,           // Keep from state
		UpdatedAt:            state.UpdatedAt,           // Keep from state
	}

	resp.Diagnostics.Append(updateUsageGroup(ctx, &updateModel, r.client)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &updateModel)...)
}

func (r *usageGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data resource_usage_group.UsageGroupModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(deleteUsageGroup(ctx, &data, r.client)...)
}

func (r *usageGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Parse the import ID as "usage_group_set_id/usage_group_id"
	importID := req.ID
	parts := strings.Split(importID, "/")

	if len(parts) != 2 {
		resp.Diagnostics.AddError(
			"Invalid Import ID Format",
			fmt.Sprintf("Expected import ID in format 'usage_group_set_id/usage_group_id', got: %s", importID),
		)
		return
	}

	usageGroupSetID := parts[0]
	usageGroupID := parts[1]

	// Set both IDs in the state
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("usage_group_set_id"), usageGroupSetID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), usageGroupID)...)
}

// createUsageGroup handles the creation of a new usage group
// Uses the API route: POST /api/{organization_id}/usage-group-sets/{usage_group_set_id}/usage-groups
func createUsageGroup(ctx context.Context, model *resource_usage_group.UsageGroupModel, client *APIClient) diag.Diagnostics {
	// Get organization_id from the client (configured at provider level)
	orgId := client.GetOrganizationId()
	usageGroupSetId := model.UsageGroupSetId.ValueString()

	if usageGroupSetId == "" {
		return diag.Diagnostics{
			diag.NewErrorDiagnostic(
				"Missing Usage Group Set ID",
				"usage_group_set_id is required but was not provided in the configuration.",
			),
		}
	}

	// Create a copy of the model without computed fields for the request body
	requestModel := resource_usage_group.UsageGroupModel{
		Name:                 model.Name,
		Order:                model.Order,
		Budget:               model.Budget,
		FilterExpressionJson: model.FilterExpressionJson,
		UsageGroupSetId:      model.UsageGroupSetId,
		// Explicitly exclude: Id (computed), OrganizationId (URL path),
		// CreatedAt, UpdatedAt, UsageGroupSetName (all computed)
	}

	// Use the API route: POST /api/{organization_id}/usage-group-sets/{usage_group_set_id}/usage-groups
	endpoint := fmt.Sprintf("/api/%s/usage-group-sets/%s/usage-groups", orgId, usageGroupSetId)
	diags := client.Post(ctx, endpoint, &requestModel, model)

	// After successful creation, ensure organization_id and usage_group_set_id are set in the response model
	if !diags.HasError() {
		model.OrganizationId = types.StringValue(orgId)
		// After successful creation, ensure organization_id is set in the response model
		if !diags.HasError() {
			model.OrganizationId = types.StringValue(orgId)
		}
	}

	return diags
}

// readUsageGroup handles reading an existing usage group from the API
// Uses the API route: GET /api/{organization_id}/usage-group-sets/{usage_group_set_id}/usage-groups/{usage_group_id}
func readUsageGroup(ctx context.Context, model *resource_usage_group.UsageGroupModel, client *APIClient) diag.Diagnostics {
	// Get organization_id from the client (configured at provider level)
	orgId := client.GetOrganizationId()
	usageGroupSetId := model.UsageGroupSetId.ValueString()
	usageGroupId := model.Id.ValueString()

	if usageGroupSetId == "" {
		return diag.Diagnostics{
			diag.NewErrorDiagnostic(
				"Missing Usage Group Set ID",
				"usage_group_set_id is required but was not found in the state.",
			),
		}
	}

	if usageGroupId == "" {
		return diag.Diagnostics{
			diag.NewErrorDiagnostic(
				"Missing Usage Group ID",
				"usage_group_id is required but was not found in the state.",
			),
		}
	}

	// Use the API route: GET /api/{organization_id}/usage-group-sets/{usage_group_set_id}/usage-groups/{usage_group_id}
	endpoint := fmt.Sprintf("/api/%s/usage-group-sets/%s/usage-groups/%s", orgId, usageGroupSetId, usageGroupId)
	diags := client.Get(ctx, endpoint, model)

	// After successful read, ensure organization_id is set in the response model
	if !diags.HasError() {
		model.OrganizationId = types.StringValue(orgId)
	}

	return diags
}

// updateUsageGroup handles the update of an existing usage group
// Uses the API route: PUT /api/{organization_id}/usage-group-sets/{usage_group_set_id}/usage-groups/{usage_group_id}
func updateUsageGroup(ctx context.Context, model *resource_usage_group.UsageGroupModel, client *APIClient) diag.Diagnostics {
	// Get organization_id from the client (configured at provider level)
	orgId := client.GetOrganizationId()
	usageGroupSetId := model.UsageGroupSetId.ValueString()
	usageGroupId := model.Id.ValueString()

	if usageGroupSetId == "" {
		return diag.Diagnostics{
			diag.NewErrorDiagnostic(
				"Missing Usage Group Set ID",
				"usage_group_set_id is required but was not found in the plan.",
			),
		}
	}

	if usageGroupId == "" {
		return diag.Diagnostics{
			diag.NewErrorDiagnostic(
				"Missing Usage Group ID",
				"usage_group_id is required but was not found in the plan.",
			),
		}
	}

	// Create a copy of the model with only fields allowed for updates
	requestModel := resource_usage_group.UsageGroupModel{
		Id:                   model.Id,
		Name:                 model.Name,
		Order:                model.Order,
		Budget:               model.Budget,
		FilterExpressionJson: model.FilterExpressionJson,
		// Exclude: OrganizationId, UsageGroupSetId (URL path), CreatedAt, UpdatedAt, UsageGroupSetName (computed)
	}

	// Use the API route: PUT /api/{organization_id}/usage-group-sets/{usage_group_set_id}/usage-groups/{usage_group_id}
	endpoint := fmt.Sprintf("/api/%s/usage-group-sets/%s/usage-groups/%s", orgId, usageGroupSetId, usageGroupId)
	diags := client.Put(ctx, endpoint, &requestModel, model)

	// After successful update, ensure organization_id and usage_group_set_id are set in the response model
	if !diags.HasError() {
		model.OrganizationId = types.StringValue(orgId)
		model.UsageGroupSetId = model.UsageGroupSetId // Keep the original value
	}

	return diags
}

// deleteUsageGroup handles the deletion of a usage group
// Uses the API route: DELETE /api/{organization_id}/usage-group-sets/{usage_group_set_id}/usage-groups/{usage_group_id}
func deleteUsageGroup(ctx context.Context, model *resource_usage_group.UsageGroupModel, client *APIClient) diag.Diagnostics {
	// Get organization_id from the client (configured at provider level)
	orgId := client.GetOrganizationId()
	usageGroupSetId := model.UsageGroupSetId.ValueString()
	usageGroupId := model.Id.ValueString()

	if usageGroupSetId == "" {
		return diag.Diagnostics{
			diag.NewErrorDiagnostic(
				"Missing Usage Group Set ID",
				"usage_group_set_id is required but was not found in the state.",
			),
		}
	}

	if usageGroupId == "" {
		return diag.Diagnostics{
			diag.NewErrorDiagnostic(
				"Missing Usage Group ID",
				"usage_group_id is required but was not found in the state.",
			),
		}
	}

	// Use the API route: DELETE /api/{organization_id}/usage-group-sets/{usage_group_set_id}/usage-groups/{usage_group_id}
	endpoint := fmt.Sprintf("/api/%s/usage-group-sets/%s/usage-groups/%s", orgId, usageGroupSetId, usageGroupId)
	return client.Delete(ctx, endpoint)
}
