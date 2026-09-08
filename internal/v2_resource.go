// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// v2Identity is what a write needs from a v2 resource's prior state: the
// item's ID for the endpoint, and its ETag for If-Match.
type v2Identity struct {
	Id, Etag types.String
}

// v2Resource implements every v2 connection/account resource's Create, Read,
// Update and Delete once. Each resource's constructor populates one of these
// with its own schema, endpoints, payload builders and response mapper; what
// varies resource to resource — schema, ValidateConfig, payload field lists,
// response field mapping, and 409/503 handling — stays in that resource's own
// files.
type v2Resource[TModel, TResponse any] struct {
	client *APIClient

	// typeNameSuffix names the resource for Terraform, e.g. "_aws_connection".
	typeNameSuffix string
	schema         func(ctx context.Context) schema.Schema
	// errors words this resource's failures the way every v2 resource does. See
	// v2_api.go.
	errors v2ErrorFormat
	// specificDiagnostic handles this resource's own conflict (409) and any
	// other status the shared v2ErrorFormat.diagnostic switch does not own
	// (AWS's 503). May be nil for a resource with nothing extra to say.
	specificDiagnostic v2Diagnostic

	collectionEndpoint string
	itemEndpoint       func(id string) string
	// identity reads the ID and ETag a write or delete needs out of a model.
	identity func(model *TModel) v2Identity

	createPayload func(ctx context.Context, plan *TModel) (any, diag.Diagnostics)
	updatePayload func(ctx context.Context, plan, state *TModel) (any, diag.Diagnostics)
	applyResponse func(ctx context.Context, model, source *TModel, response *TResponse) diag.Diagnostics
	// validateConfig runs a resource's plan-time checks beyond what the
	// generated schema already enforces. Nil for a resource with none.
	validateConfig func(ctx context.Context, config *TModel) diag.Diagnostics
}

var _ resource.Resource = (*v2Resource[struct{}, struct{}])(nil)
var _ resource.ResourceWithConfigure = (*v2Resource[struct{}, struct{}])(nil)
var _ resource.ResourceWithImportState = (*v2Resource[struct{}, struct{}])(nil)
var _ resource.ResourceWithValidateConfig = (*v2Resource[struct{}, struct{}])(nil)

// configureAPIClient reads the provider's client out of a Configure request. It
// returns nil when the provider has not configured yet — Terraform calls
// Configure with no data during validation — and adds a diagnostic when the
// data is not what this provider puts there.
func configureAPIClient(req resource.ConfigureRequest, resp *resource.ConfigureResponse) *APIClient {
	if req.ProviderData == nil {
		return nil
	}

	providerData, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return nil
	}

	return providerData.Client
}

func (r *v2Resource[TModel, TResponse]) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	client := configureAPIClient(req, resp)
	if client == nil {
		return
	}
	r.client = client
}

func (r *v2Resource[TModel, TResponse]) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + r.typeNameSuffix
}

func (r *v2Resource[TModel, TResponse]) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = r.schema(ctx)
}

func (r *v2Resource[TModel, TResponse]) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *v2Resource[TModel, TResponse]) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	if r.validateConfig == nil {
		return
	}

	var config TModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(r.validateConfig(ctx, &config)...)
}

func (r *v2Resource[TModel, TResponse]) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan TModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	request, diags := r.createPayload(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var response TResponse
	apiErr, diags := r.client.doRequest(ctx, http.MethodPost, r.collectionEndpoint, request, &response, requestOptions{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		resp.Diagnostics.Append(r.errors.diagnostic("add "+r.errors.Object, apiErr, r.specificDiagnostic))
		return
	}

	state := plan
	resp.Diagnostics.Append(r.applyResponse(ctx, &state, &plan, &response)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *v2Resource[TModel, TResponse]) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	identity := r.identity(&state)
	var response TResponse
	apiErr, diags := r.client.doRequest(ctx, http.MethodGet, r.itemEndpoint(identity.Id.ValueString()), nil, &response, requestOptions{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		// Gone, or no longer visible to this API key. Either way Terraform
		// should plan to recreate it rather than keep stale values.
		if apiErr.StatusCode == http.StatusNotFound {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.Append(r.errors.diagnostic("read "+r.errors.Object, apiErr, r.specificDiagnostic))
		return
	}

	refreshed := state
	resp.Diagnostics.Append(r.applyResponse(ctx, &refreshed, &state, &response)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &refreshed)...)
}

func (r *v2Resource[TModel, TResponse]) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state TModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	request, diags := r.updatePayload(ctx, &plan, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	identity := r.identity(&state)
	var response TResponse
	apiErr, diags := r.client.doRequest(ctx, http.MethodPatch, r.itemEndpoint(identity.Id.ValueString()), request, &response, requestOptions{
		headers: ifMatchHeader(identity.Etag),
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		resp.Diagnostics.Append(r.errors.diagnostic("update "+r.errors.Object, apiErr, r.specificDiagnostic))
		return
	}

	updated := plan
	resp.Diagnostics.Append(r.applyResponse(ctx, &updated, &plan, &response)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
}

func (r *v2Resource[TModel, TResponse]) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state TModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	identity := r.identity(&state)
	apiErr, diags := r.client.doRequest(ctx, http.MethodDelete, r.itemEndpoint(identity.Id.ValueString()), nil, nil, requestOptions{
		headers: ifMatchHeader(identity.Etag),
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Already gone is the outcome delete was asked for.
	if apiErr != nil && apiErr.StatusCode != http.StatusNotFound {
		resp.Diagnostics.Append(r.errors.diagnostic("delete "+r.errors.Object, apiErr, r.specificDiagnostic))
	}
}

// v2Payload adapts a create builder that cannot fail into the shape
// v2Resource needs.
func v2Payload[TModel, TPayload any](build func(plan *TModel) TPayload) func(ctx context.Context, plan *TModel) (any, diag.Diagnostics) {
	return func(ctx context.Context, plan *TModel) (any, diag.Diagnostics) {
		return build(plan), nil
	}
}

// v2FalliblePayload adapts a create builder that can itself fail validation
// (Snowflake's, which converts a list attribute and can return diagnostics for
// that).
func v2FalliblePayload[TModel, TPayload any](build func(ctx context.Context, plan *TModel) (TPayload, diag.Diagnostics)) func(ctx context.Context, plan *TModel) (any, diag.Diagnostics) {
	return func(ctx context.Context, plan *TModel) (any, diag.Diagnostics) {
		return build(ctx, plan)
	}
}

// v2Patch adapts an update builder that cannot fail.
func v2Patch[TModel, TPayload any](build func(plan, state *TModel) TPayload) func(ctx context.Context, plan, state *TModel) (any, diag.Diagnostics) {
	return func(ctx context.Context, plan, state *TModel) (any, diag.Diagnostics) {
		return build(plan, state), nil
	}
}

// v2FalliblePatch adapts an update builder that can itself fail validation
// (Snowflake's mode_token_secret invariant).
func v2FalliblePatch[TModel, TPayload any](build func(ctx context.Context, plan, state *TModel) (TPayload, diag.Diagnostics)) func(ctx context.Context, plan, state *TModel) (any, diag.Diagnostics) {
	return func(ctx context.Context, plan, state *TModel) (any, diag.Diagnostics) {
		return build(ctx, plan, state)
	}
}
