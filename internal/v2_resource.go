// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
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

	// collectionEndpoint is where a create posts, and itemEndpoint is where a
	// read, update or delete goes. Both take the model rather than an id,
	// because a resource nested under a parent — a usage group under its set —
	// needs the parent's id in its path as well as its own.
	collectionEndpoint func(model *TModel) string
	itemEndpoint       func(model *TModel) string
	// fetch reads the item's current state from the API, for Read and for the
	// ETag refetch in write. Nil means a GET on itemEndpoint. A resource with
	// no GET-by-id route — a team member, a role grant — uses v2ListAndFind
	// instead, which answers a 404 when the list does not hold the item.
	fetch func(ctx context.Context, client *APIClient, model *TModel) (*TResponse, *apiError, diag.Diagnostics)
	// identity reads the ID and ETag a write or delete needs out of a model.
	identity func(model *TModel) v2Identity
	// prepareWrite runs before a create, update or delete issues its request,
	// for a resource that needs something to happen first — a usage group
	// records a version of its set. Nil for a resource with nothing to prepare.
	prepareWrite func(ctx context.Context, client *APIClient, model *TModel) diag.Diagnostics
	// selfInflicted412 reports whether a precondition failure is one this
	// provider caused earlier in the same apply rather than a change someone
	// else made. Recording a version changes the set it belongs to, so an ETag
	// read before that no longer matches — a failure the user did nothing to
	// cause and can do nothing about. Saying true here gets the write retried
	// once against a freshly read ETag; a 412 from real drift still surfaces,
	// which is the whole point of sending If-Match. Nil for a resource nothing
	// else in the apply invalidates.
	selfInflicted412 func(client *APIClient, model *TModel) bool
	// importState parses a `terraform import` address into state. Nil means the
	// address is the resource's own id, which is all a top-level resource needs;
	// a nested one has to carry its parent's id too.
	importState func(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse)

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
	client, diags := apiClientFromProviderData("Resource", req.ProviderData)
	resp.Diagnostics.Append(diags...)
	return client
}

// apiClientFromProviderData is the part of Configure that resources and data
// sources share. kind is "Resource" or "Data Source", for the diagnostic.
func apiClientFromProviderData(kind string, data any) (*APIClient, diag.Diagnostics) {
	if data == nil {
		return nil, nil
	}

	providerData, ok := data.(*ProviderData)
	if !ok {
		return nil, diag.Diagnostics{diag.NewErrorDiagnostic(
			"Unexpected "+kind+" Configure Type",
			fmt.Sprintf("Expected *ProviderData, got: %T. Please report this issue to the provider developers.", data),
		)}
	}

	return providerData.Client, nil
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
	if r.importState != nil {
		r.importState(ctx, req, resp)
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// splitChildImportID splits a `terraform import` address of the form
// `parent/child`. It splits at the last "/": a child id the API assigns has no
// "/", but a parent id can — an email address or an SSO group name can hold
// one. ok is false when either part is empty or there is no "/".
func splitChildImportID(id string) (parent, child string, ok bool) {
	i := strings.LastIndex(id, "/")
	if i < 0 {
		return "", "", false
	}
	parent, child = id[:i], id[i+1:]
	return parent, child, parent != "" && child != ""
}

// v2ImportChild builds an importState hook for a resource nested under a
// parent. The address is `<parent id>/<child id>`. The hook writes the parent
// id to parentAttribute and the child id to "id"; Read then fills in the rest.
// noun names the resource in the diagnostic, and form is the address form the
// diagnostic shows, such as "team_id/member_id".
func v2ImportChild(noun, parentAttribute, form string) func(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	return func(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
		parent, child, ok := splitChildImportID(req.ID)
		if !ok {
			resp.Diagnostics.AddError(
				"Invalid "+noun+" Import ID",
				fmt.Sprintf("Expected an import ID of the form `%s`, got %q.", form, req.ID),
			)
			return
		}

		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(parentAttribute), parent)...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), child)...)
	}
}

// prepare runs the resource's prepareWrite hook, if it has one.
func (r *v2Resource[TModel, TResponse]) prepare(ctx context.Context, model *TModel) diag.Diagnostics {
	if r.prepareWrite == nil {
		return nil
	}
	return r.prepareWrite(ctx, r.client, model)
}

// get reads the item's current state from the API, through the fetch hook
// when the resource has one.
func (r *v2Resource[TModel, TResponse]) get(ctx context.Context, model *TModel) (*TResponse, *apiError, diag.Diagnostics) {
	if r.fetch != nil {
		return r.fetch(ctx, r.client, model)
	}

	var response TResponse
	apiErr, diags := r.client.doRequest(ctx, http.MethodGet, r.itemEndpoint(model), nil, &response, requestOptions{})
	if diags.HasError() || apiErr != nil {
		return nil, apiErr, diags
	}
	return &response, nil, nil
}

// freshEtag re-reads the resource and returns the ETag it currently has, for
// retrying a write this provider itself invalidated. See selfInflicted412.
func (r *v2Resource[TModel, TResponse]) freshEtag(ctx context.Context, state *TModel) (types.String, diag.Diagnostics) {
	response, apiErr, diags := r.get(ctx, state)
	if diags.HasError() {
		return types.StringNull(), diags
	}
	if apiErr != nil {
		return types.StringNull(), diag.Diagnostics{
			r.errors.diagnostic("re-read "+r.errors.Object, apiErr, r.specificDiagnostic),
		}
	}

	refreshed := *state
	diags = r.applyResponse(ctx, &refreshed, state, response)
	if diags.HasError() {
		return types.StringNull(), diags
	}
	return r.identity(&refreshed).Etag, nil
}

// write issues a write that carries an ETag, retrying once against a fresh one
// when the resource says the precondition failure was this provider's own
// doing. The retry re-reads rather than trusting any ETag it already holds, so
// a change made outside Terraform between the two attempts still fails the
// second one.
func (r *v2Resource[TModel, TResponse]) write(ctx context.Context, method string, state *TModel, request any, response *TResponse) (*apiError, diag.Diagnostics) {
	etag := r.identity(state).Etag

	apiErr, diags := r.client.doRequest(ctx, method, r.itemEndpoint(state), request, response, requestOptions{
		headers: ifMatchHeader(etag),
	})
	if diags.HasError() || apiErr == nil {
		return apiErr, diags
	}
	if apiErr.StatusCode != http.StatusPreconditionFailed ||
		r.selfInflicted412 == nil || !r.selfInflicted412(r.client, state) {
		return apiErr, diags
	}

	// This 412 is one EnsureVersion caused earlier in the same apply, not a
	// change the user needs to hear about. Naming that here, next to the
	// refetch and the retry it triggers, is what makes the sequence legible in
	// a trace log — each of those three requests logs itself in makeRequest.
	tflog.Debug(ctx, "self-inflicted 412, refetching ETag and retrying write", map[string]interface{}{
		"method": method,
		"path":   r.itemEndpoint(state),
	})

	fresh, freshDiags := r.freshEtag(ctx, state)
	if freshDiags.HasError() {
		return apiErr, freshDiags
	}

	return r.client.doRequest(ctx, method, r.itemEndpoint(state), request, response, requestOptions{
		headers: ifMatchHeader(fresh),
	})
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

	// A create carries no ETag — there is nothing yet to have changed — but the
	// preparation itself may still be needed.
	resp.Diagnostics.Append(r.prepare(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var response TResponse
	apiErr, diags := r.client.doRequest(ctx, http.MethodPost, r.collectionEndpoint(&plan), request, &response, requestOptions{})
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

	response, apiErr, diags := r.get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		// Gone, or no longer visible to this API key. Either way Terraform
		// should plan to recreate it rather than keep stale values. A fetch
		// hook that lists rather than GETs answers a missing item the same way.
		if apiErr.StatusCode == http.StatusNotFound {
			tflog.Debug(ctx, "SELECT resource not found, removing from state", map[string]interface{}{"path": r.itemEndpoint(&state)})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.Append(r.errors.diagnostic("read "+r.errors.Object, apiErr, r.specificDiagnostic))
		return
	}

	refreshed := state
	resp.Diagnostics.Append(r.applyResponse(ctx, &refreshed, &state, response)...)
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

	resp.Diagnostics.Append(r.prepare(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var response TResponse
	apiErr, diags := r.write(ctx, http.MethodPatch, &state, request, &response)
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

	resp.Diagnostics.Append(r.prepare(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiErr, diags := r.write(ctx, http.MethodDelete, &state, nil, nil)
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

// v2NoUpdate is the updatePayload for a resource that the API cannot update,
// such as a team role grant. Every attribute of such a resource forces a new
// resource, so Terraform does not call Update. If it does, this returns an
// error and sends no request.
func v2NoUpdate[TModel any](noun string) func(ctx context.Context, plan, state *TModel) (any, diag.Diagnostics) {
	return func(ctx context.Context, plan, state *TModel) (any, diag.Diagnostics) {
		return nil, diag.Diagnostics{diag.NewErrorDiagnostic(
			"Unexpected "+noun+" Update",
			"SELECT cannot update a "+noun+", so every change must replace it. "+
				"Please report this issue to the provider developers.",
		)}
	}
}

// v2FalliblePatch adapts an update builder that can itself fail validation
// (Snowflake's mode_token_secret invariant).
func v2FalliblePatch[TModel, TPayload any](build func(ctx context.Context, plan, state *TModel) (TPayload, diag.Diagnostics)) func(ctx context.Context, plan, state *TModel) (any, diag.Diagnostics) {
	return func(ctx context.Context, plan, state *TModel) (any, diag.Diagnostics) {
		return build(ctx, plan, state)
	}
}
