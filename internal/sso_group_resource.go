// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"terraform-provider-select/internal/provider/resource_sso_group"
)

// ssoGroupResource is hand-written, not a v2Resource. v2Resource does one
// request per operation and keeps one URL for the life of a resource. An SSO
// group needs more:
//
//   - Read gets the group, then lists its roles.
//   - Update can rename the group, which changes its URL, and then grants and
//     revokes roles one request at a time.
//   - A failure partway through an update must write what SELECT now holds to
//     state.
//
// It uses the same v2 pieces as v2Resource: configureAPIClient, doRequest,
// ifMatchHeader, v2ListAll and the v2ErrorFormat diagnostics.
type ssoGroupResource struct {
	client *APIClient
}

var _ resource.Resource = (*ssoGroupResource)(nil)
var _ resource.ResourceWithConfigure = (*ssoGroupResource)(nil)
var _ resource.ResourceWithImportState = (*ssoGroupResource)(nil)
var _ resource.ResourceWithValidateConfig = (*ssoGroupResource)(nil)

func NewSsoGroupResource() resource.Resource {
	return &ssoGroupResource{}
}

// ssoGroupResourceSchema takes the generated schema, makes id follow name,
// and injects the roles set that the generator does not produce (see
// generator_config.v2.yml). Each call to the generated schema function builds
// a new attribute map, so this changes no shared value.
func ssoGroupResourceSchema(ctx context.Context) schema.Schema {
	s := resource_sso_group.SsoGroupResourceSchema(ctx)

	id := s.Attributes["id"].(schema.StringAttribute)
	id.PlanModifiers = append(id.PlanModifiers, ssoGroupIdFromName{})
	s.Attributes["id"] = id

	s.Attributes["roles"] = ssoGroupRolesAttribute()
	return s
}

// ssoGroupRolesAttribute is the roles set. An element holds only the role and
// the scope. A computed value in an element, such as the grant id, would be
// unknown in the plan and would make every plan replace the element.
func ssoGroupRolesAttribute() schema.SetNestedAttribute {
	return schema.SetNestedAttribute{
		Required: true,
		Description: "The roles that the group's members receive. A change applies on each member's " +
			"next request. At least one " +
			"is required: SELECT does not keep a group with no roles. This set is authoritative: a " +
			"role granted to the group outside Terraform shows as a change in the next plan. A change " +
			"grants the new roles first and then revokes the removed roles, so the group always " +
			"holds at least one role.",
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"role": schema.StringAttribute{
					Required: true,
					Description: "The role to grant: `admin`, `editor`, `monitor_editor`, `viewer` or " +
						"`team_creator`.",
					Validators: []validator.String{
						stringvalidator.OneOf(roleGrantRoles...),
					},
				},
				"scope": roleGrantScopeAttribute(),
			},
		},
		Validators: []validator.Set{
			setvalidator.SizeAtLeast(1),
		},
	}
}

// ssoGroupIdFromName plans id as the planned name. The API keeps id and name
// equal, so the plan can show the new id of a rename instead of an unknown
// value.
type ssoGroupIdFromName struct{}

func (m ssoGroupIdFromName) Description(context.Context) string {
	return "id is always the same as name"
}

func (m ssoGroupIdFromName) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m ssoGroupIdFromName) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	var name types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("name"), &name)...)
	if resp.Diagnostics.HasError() || name.IsNull() || name.IsUnknown() {
		return
	}
	resp.PlanValue = name
}

func (r *ssoGroupResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	client := configureAPIClient(req, resp)
	if client == nil {
		return
	}
	r.client = client
}

func (r *ssoGroupResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sso_group"
}

func (r *ssoGroupResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = ssoGroupResourceSchema(ctx)
}

// ImportState takes the group name, which is also its id. Read fills in the
// rest, roles included.
func (r *ssoGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *ssoGroupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config ssoGroupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateSsoGroupConfig(ctx, &config)...)
}

// failure is the diagnostic for an API failure during operation.
func (r *ssoGroupResource) failure(operation string, apiErr *apiError) diag.Diagnostic {
	return ssoGroupErrors.diagnostic(operation, apiErr, ssoGroupConflict)
}

// getGroup reads the group. The response does not carry the roles.
func (r *ssoGroupResource) getGroup(ctx context.Context, id string) (*ssoGroupResponse, *apiError, diag.Diagnostics) {
	var response ssoGroupResponse
	apiErr, diags := r.client.doRequest(ctx, http.MethodGet, ssoGroupEndpoint(id), nil, &response, requestOptions{})
	if diags.HasError() || apiErr != nil {
		return nil, apiErr, diags
	}
	return &response, nil, nil
}

// listRoles returns the grants of the group that the roles set manages, from
// all pages. See ssoGroupManagedGrants. Every use of the list goes through
// here: Read, the revoke matching, the relist after an ambiguous write, and
// the list after an update.
func (r *ssoGroupResource) listRoles(ctx context.Context, id string) ([]ssoGroupRoleResponse, *apiError, diag.Diagnostics) {
	grants, apiErr, diags := v2ListAll[ssoGroupRoleResponse](ctx, r.client, ssoGroupRolesEndpoint(id), nil)
	if diags.HasError() || apiErr != nil {
		return nil, apiErr, diags
	}
	return ssoGroupManagedGrants(grants), nil, nil
}

// Create sends the name and every role in one request. The API requires at
// least one role.
func (r *ssoGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ssoGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	roles, diags := ssoGroupRolesFromSet(ctx, plan.Roles)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var response ssoGroupResponse
	apiErr, diags := r.client.doRequest(ctx, http.MethodPost, ssoGroupsEndpoint, buildSsoGroupCreate(plan.Name, roles), &response, requestOptions{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		resp.Diagnostics.Append(r.failure(ssoGroupOpCreate, apiErr))
		return
	}

	// The response does not carry the roles. SELECT granted the planned ones,
	// so state takes them from the plan. Read lists them later.
	state := plan
	applySsoGroupResponse(&state, &response)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Read gets the group and lists its roles. A group that is gone is removed
// from state.
func (r *ssoGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ssoGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, apiErr, diags := r.getGroup(ctx, state.Id.ValueString())
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		if apiErr.StatusCode == http.StatusNotFound {
			tflog.Debug(ctx, "SELECT SSO group not found, removing from state", map[string]interface{}{"id": state.Id.ValueString()})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.Append(r.failure(ssoGroupOpRead, apiErr))
		return
	}

	grants, apiErr, diags := r.listRoles(ctx, group.Id)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		// The group was deleted between the two requests.
		if apiErr.StatusCode == http.StatusNotFound {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.Append(r.failure(ssoGroupOpList, apiErr))
		return
	}

	configured, diags := ssoGroupRolesFromSet(ctx, state.Roles)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	roles, diags := ssoGroupRolesValue(ctx, configured, grants)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	refreshed := state
	applySsoGroupResponse(&refreshed, group)
	refreshed.Roles = roles
	resp.Diagnostics.Append(resp.State.Set(ctx, &refreshed)...)
}

// ssoGroupProgress is what SELECT holds during an update: the group's fields
// and the roles granted so far. Update writes it to state when a step fails,
// so that state does not point at the old URL after a rename, and the next
// plan starts from the roles that were actually granted and revoked.
type ssoGroupProgress struct {
	model ssoGroupModel
	roles []ssoGroupRole
}

// save writes the progress to state. It appends any diagnostic to diags.
func (p *ssoGroupProgress) save(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	roles, d := ssoGroupRolesSet(ctx, p.roles)
	diags.Append(d...)
	if d.HasError() {
		return
	}
	model := p.model
	model.Roles = roles
	diags.Append(state.Set(ctx, &model)...)
}

// Update runs in this order:
//
//  1. Rename the group, with If-Match, when the name changed. The new id goes
//     to state at once, because every later request uses the new URL.
//  2. Grant each role that the plan adds.
//  3. Revoke each role that the plan removes. Steps 2 and 3 are in this order
//     so that the group always holds at least one role: the API refuses to
//     revoke the last one.
//  4. Get the group again for its new ETag. A role change also changes it.
//     Then list the roles, to find a change made outside Terraform during
//     the apply.
//
// When a step fails, state holds what SELECT holds after the steps before it.
func (r *ssoGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state ssoGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	planRoles, diags := ssoGroupRolesFromSet(ctx, plan.Roles)
	resp.Diagnostics.Append(diags...)
	stateRoles, diags := ssoGroupRolesFromSet(ctx, state.Roles)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	add, remove := diffSsoGroupRoles(planRoles, stateRoles)

	progress := ssoGroupProgress{model: state, roles: stateRoles}
	fail := func(operation string, apiErr *apiError, diags diag.Diagnostics) {
		resp.Diagnostics.Append(diags...)
		if apiErr != nil {
			resp.Diagnostics.Append(r.failure(operation, apiErr))
		}
		progress.save(ctx, &resp.State, &resp.Diagnostics)
	}

	// 1. Rename. The If-Match is the ETag from the last read, so a change
	// made outside Terraform since then fails the rename with a 412. See
	// writeGroup for the one retry.
	if !plan.Name.Equal(state.Name) {
		var renamed ssoGroupResponse
		apiErr, diags := r.writeGroup(ctx, http.MethodPatch, &state, stateRoles,
			ssoGroupUpdatePayload{Name: plan.Name.ValueString()}, &renamed)
		if diags.HasError() || apiErr != nil {
			// A lost response or a server error does not prove that the rename
			// did not happen. If it did, state must point at the new URL, or
			// the next Read gets a 404 on the old URL and drops a group that
			// still exists.
			if ambiguousWrite(apiErr) {
				if group := r.renamedGroup(ctx, state.Id.ValueString(), plan.Name.ValueString()); group != nil {
					applySsoGroupResponse(&progress.model, group)
				}
			}
			fail(ssoGroupOpRename, apiErr, diags)
			return
		}
		applySsoGroupResponse(&progress.model, &renamed)
		progress.save(ctx, &resp.State, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	id := progress.model.Id.ValueString()

	// relist replaces the tracked roles with the roles SELECT lists, after a
	// grant or a revoke that failed but can still have happened. The listed
	// grants are paired with the state elements first, then with the planned
	// ones, the same as the tracked progress. If the list fails, the tracked
	// roles stay.
	relist := func() {
		grants, apiErr, diags := r.listRoles(ctx, id)
		if diags.HasError() || apiErr != nil {
			return
		}
		roles, diags := ssoGroupRolesFromGrants(ctx, append(append([]ssoGroupRole{}, stateRoles...), planRoles...), grants)
		if diags.HasError() {
			return
		}
		progress.roles = roles
	}

	// 2. Grant.
	for _, role := range add {
		var granted ssoGroupRoleResponse
		apiErr, diags := r.client.doRequest(ctx, http.MethodPost, ssoGroupRolesEndpoint(id), role.payload, &granted, requestOptions{})
		if diags.HasError() || apiErr != nil {
			if ambiguousWrite(apiErr) {
				relist()
			}
			fail(ssoGroupOpGrant, apiErr, diags)
			return
		}
		progress.roles = append(progress.roles, role)
	}

	// 3. Revoke. State holds no grant ids, so find each removed role's grants
	// in the group's list by role and scope. A role with no grant in the list
	// is already gone.
	if len(remove) > 0 {
		grants, apiErr, diags := r.listRoles(ctx, id)
		if diags.HasError() || apiErr != nil {
			fail(ssoGroupOpList, apiErr, diags)
			return
		}
		for _, role := range remove {
			for _, grant := range ssoGroupGrantsFor(role.key, grants) {
				apiErr, diags := r.client.doRequest(ctx, http.MethodDelete, ssoGroupRoleEndpoint(id, grant.Id), nil, nil, requestOptions{})
				if diags.HasError() || (apiErr != nil && apiErr.StatusCode != http.StatusNotFound) {
					if ambiguousWrite(apiErr) {
						relist()
					}
					fail(ssoGroupOpRevoke, apiErr, diags)
					return
				}
			}
			progress.roles = withoutSsoGroupRole(progress.roles, role.key)
		}
	}

	// 4. Get the group's new ETag and timestamps.
	group, apiErr, diags := r.getGroup(ctx, id)
	if diags.HasError() || apiErr != nil {
		fail(ssoGroupOpRead, apiErr, diags)
		return
	}

	updated := plan
	applySsoGroupResponse(&updated, group)
	progress.model = updated
	progress.roles = planRoles

	grants, apiErr, diags := r.listRoles(ctx, id)
	if diags.HasError() || apiErr != nil {
		fail(ssoGroupOpList, apiErr, diags)
		return
	}
	listed, diags := ssoGroupRolesValue(ctx, planRoles, grants)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		progress.save(ctx, &resp.State, &resp.Diagnostics)
		return
	}

	// State takes the planned roles. A role whose scope id changed only in
	// case keeps the planned case. When SELECT holds other roles, someone
	// changed them during the apply. State that differs from the plan is an
	// error that Terraform reports as a provider bug, so the provider warns
	// instead, and the next plan shows the difference.
	if !listed.Equal(plan.Roles) {
		resp.Diagnostics.AddWarning("SSO Group Roles Changed During Apply",
			fmt.Sprintf("After the update, SELECT holds roles for the SSO group %q that are different from "+
				"the configuration. Something changed them outside Terraform during the apply. The next plan "+
				"shows the difference.", id))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
}

// writeGroup sends a rename or a delete of the group with If-Match from
// state. On a 412 it retries once, with a fresh ETag, when nothing that this
// resource manages has changed since state was read. See
// ssoGroupUnchangedSince.
//
// A change to the group's team memberships also changes the group's ETag.
// In the same apply, Terraform can replace a select_team_member that refers to
// the group, and it destroys the old member before it renames or deletes the
// group. The ETag in state is then stale because of this apply, not because
// of an outside change. This is the same idea as a usage group set, whose
// ETag a recorded version changes (see selfInflicted412). A 412 after a real
// change to the group's name or roles still reaches the user. A second 412
// is returned as it is, so there is no loop.
func (r *ssoGroupResource) writeGroup(ctx context.Context, method string, state *ssoGroupModel, stateRoles []ssoGroupRole, request any, response any) (*apiError, diag.Diagnostics) {
	endpoint := ssoGroupEndpoint(state.Id.ValueString())
	apiErr, diags := r.client.doRequest(ctx, method, endpoint, request, response, requestOptions{
		headers: ifMatchHeader(state.Etag),
	})
	if diags.HasError() || apiErr == nil || apiErr.StatusCode != http.StatusPreconditionFailed {
		return apiErr, diags
	}

	group, getErr, getDiags := r.getGroup(ctx, state.Id.ValueString())
	if getDiags.HasError() || getErr != nil {
		return apiErr, diags
	}
	grants, listErr, listDiags := r.listRoles(ctx, group.Id)
	if listDiags.HasError() || listErr != nil {
		return apiErr, diags
	}
	if !ssoGroupUnchangedSince(ctx, state, stateRoles, group, grants) {
		return apiErr, diags
	}

	tflog.Debug(ctx, "SSO group ETag changed, but its name and roles did not; retrying with the fresh ETag", map[string]interface{}{
		"method": method,
		"path":   endpoint,
	})
	return r.client.doRequest(ctx, method, endpoint, request, response, requestOptions{
		headers: ifMatchHeader(types.StringValue(group.Etag)),
	})
}

// renamedGroup returns the group at newId when a rename from oldId is known
// to have happened: the old URL answers 404 and the new URL holds a group. A
// group at the new URL alone proves nothing, because another group can have
// that name. In every other case it returns nil, and state stays at oldId.
func (r *ssoGroupResource) renamedGroup(ctx context.Context, oldId, newId string) *ssoGroupResponse {
	if _, apiErr, diags := r.getGroup(ctx, oldId); diags.HasError() || apiErr == nil || apiErr.StatusCode != http.StatusNotFound {
		return nil
	}
	group, apiErr, diags := r.getGroup(ctx, newId)
	if diags.HasError() || apiErr != nil {
		return nil
	}
	return group
}

// ambiguousWrite reports whether a failed write can still have happened: no
// response arrived, the response could not be read, or the server failed.
func ambiguousWrite(apiErr *apiError) bool {
	return apiErr == nil || apiErr.StatusCode >= http.StatusInternalServerError
}

// Delete deletes the group, with If-Match. The API deletes every grant of the
// group with it.
func (r *ssoGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ssoGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	stateRoles, diags := ssoGroupRolesFromSet(ctx, state.Roles)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiErr, diags := r.writeGroup(ctx, http.MethodDelete, &state, stateRoles, nil, nil)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Already gone is the outcome delete was asked for.
	if apiErr != nil && apiErr.StatusCode != http.StatusNotFound {
		resp.Diagnostics.Append(r.failure(ssoGroupOpDelete, apiErr))
	}
}
