// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"terraform-provider-select/internal/provider/resource_sso_group"
)

// SSO groups live on the v2 API. The group's id is its name, so a rename
// moves the group to a new URL. The group's role grants are nested under it:
// the API can list, create and delete a grant, but not update one, and a
// grant has no ETag. The group has an ETag. See sso_group_resource.go.
const ssoGroupsEndpoint = "/v2/sso-groups"

// ssoGroupEndpoint escapes the id as one path segment. The id is the group
// name, which can hold a space or a "/".
func ssoGroupEndpoint(id string) string {
	return fmt.Sprintf("%s/%s", ssoGroupsEndpoint, url.PathEscape(id))
}

func ssoGroupRolesEndpoint(id string) string {
	return fmt.Sprintf("%s/roles", ssoGroupEndpoint(id))
}

// ssoGroupRoleEndpoint escapes the grant id as one path segment.
// ssoGroupEndpoint escapes the group id.
func ssoGroupRoleEndpoint(id, roleId string) string {
	return fmt.Sprintf("%s/%s", ssoGroupRolesEndpoint(id), url.PathEscape(roleId))
}

// ssoGroupErrors words the failures every v2 resource can hit. The scope
// names come from the route tag; the spec does not name them.
var ssoGroupErrors = v2ErrorFormat{
	Noun:       "SSO Group",
	Subject:    "the SSO group",
	Object:     "the SSO group",
	ReadScope:  "sso_groups:read",
	WriteScope: "sso_groups:write",
}

// The operations the resource names in its diagnostics.
const (
	ssoGroupOpCreate = "add the SSO group"
	ssoGroupOpRead   = "read the SSO group"
	ssoGroupOpList   = "list the SSO group's roles"
	ssoGroupOpRename = "rename the SSO group"
	ssoGroupOpGrant  = "grant a role to the SSO group"
	ssoGroupOpRevoke = "revoke a role from the SSO group"
	ssoGroupOpDelete = "delete the SSO group"
)

// ssoGroupConflict is the specificDiagnostic for the resource. The spec
// documents a 409 on most routes but does not say what causes it, so each
// message names the likely cause for its operation. A 409 on any other
// operation falls through to the shared wording in v2ErrorFormat.
func ssoGroupConflict(operation string, apiErr *apiError) diag.Diagnostic {
	if apiErr.StatusCode != http.StatusConflict {
		return nil
	}

	var hint string
	switch operation {
	case ssoGroupOpCreate:
		hint = "This can mean that a group with this name already exists. If so, import it " +
			"instead of creating it again:\n\n  terraform import select_sso_group.<name> <group name>"
	case ssoGroupOpRename:
		hint = "This can mean that a group with the new name already exists."
	case ssoGroupOpGrant:
		hint = "This can mean that the group already holds this role on this scope. Run " +
			"`terraform apply -refresh-only` to read the group's current roles, then apply again."
	case ssoGroupOpRevoke:
		hint = "SELECT does not revoke the last role of a group. This can mean that the roles " +
			"in the configuration were revoked outside Terraform. Run " +
			"`terraform apply -refresh-only` to read the group's current roles, then apply again."
	default:
		return nil
	}
	return diag.NewErrorDiagnostic(
		ssoGroupErrors.Noun+" Conflict",
		fmt.Sprintf("SELECT could not %s: %s\n\n%s", operation, apiErr.Detail, hint),
	)
}

// ssoGroupModel is the generated model plus the hand-written roles set. The
// framework flattens an untagged embedded struct, as for budgetModel.
type ssoGroupModel struct {
	resource_sso_group.SsoGroupModel
	Roles types.Set `tfsdk:"roles"`
}

// ssoGroupRoleModel is one element of the roles set.
type ssoGroupRoleModel struct {
	Role  types.String `tfsdk:"role"`
	Scope types.Object `tfsdk:"scope"`
}

// ssoGroupCreatePayload mirrors SsoGroupCreateV2.
type ssoGroupCreatePayload struct {
	Name  string                   `json:"name"`
	Roles []roleGrantCreatePayload `json:"roles"`
}

// ssoGroupUpdatePayload mirrors SsoGroupUpdateV2. name is its only field, so
// the resource sends it only for a rename.
type ssoGroupUpdatePayload struct {
	Name string `json:"name"`
}

// ssoGroupResponse mirrors SsoGroupV2. id and name always hold the same value.
type ssoGroupResponse struct {
	Id         string `json:"id"`
	Name       string `json:"name"`
	CreateTime string `json:"create_time"`
	UpdateTime string `json:"update_time"`
	Etag       string `json:"etag"`
}

// ssoGroupRoleResponse mirrors SsoGroupRoleGrantV2. It does not carry the
// group's id: the route holds it.
type ssoGroupRoleResponse struct {
	roleGrantResponse
}

// ssoGroupRoleKey identifies a grant by what it grants: the role and the
// scope. The configuration and the API both identify a grant this way. The
// scope id is lowercased, because the API can return an id in a different
// case than the one configured. An omitted scope has the organization type.
type ssoGroupRoleKey struct {
	Role, ScopeType, ScopeId string
}

func newSsoGroupRoleKey(role, scopeType string, scopeId *string) ssoGroupRoleKey {
	key := ssoGroupRoleKey{Role: role, ScopeType: scopeType}
	if scopeId != nil {
		key.ScopeId = strings.ToLower(*scopeId)
	}
	return key
}

// ssoGroupRole is one element of a roles set, with its create request and its
// key.
type ssoGroupRole struct {
	model   ssoGroupRoleModel
	payload roleGrantCreatePayload
	key     ssoGroupRoleKey
}

// ssoGroupRolesFromSet reads the elements of a known roles set. A null set
// gives no elements: the state after `terraform import` has no roles yet.
func ssoGroupRolesFromSet(ctx context.Context, set types.Set) ([]ssoGroupRole, diag.Diagnostics) {
	if set.IsNull() || set.IsUnknown() {
		return nil, nil
	}

	var models []ssoGroupRoleModel
	diags := set.ElementsAs(ctx, &models, false)
	if diags.HasError() {
		return nil, diags
	}

	roles := make([]ssoGroupRole, 0, len(models))
	for _, model := range models {
		payload, d := buildRoleGrantCreate(ctx, model.Role, model.Scope)
		diags.Append(d...)
		if diags.HasError() {
			return nil, diags
		}
		scopeType, scopeId := roleGrantScopeOrganization, (*string)(nil)
		if payload.Scope != nil {
			scopeType, scopeId = payload.Scope.Type, payload.Scope.Id
		}
		roles = append(roles, ssoGroupRole{
			model:   model,
			payload: *payload,
			key:     newSsoGroupRoleKey(payload.Role, scopeType, scopeId),
		})
	}
	return roles, diags
}

// ssoGroupGrantKey is the key of a grant the API listed. It fails for a scope
// type that this provider does not know.
func ssoGroupGrantKey(grant *ssoGroupRoleResponse) (ssoGroupRoleKey, error) {
	scope, err := roleGrantScopeFromResponse(grant.roleGrantScopeFields)
	if err != nil {
		return ssoGroupRoleKey{}, err
	}
	return newSsoGroupRoleKey(grant.Role, scope.Type, scope.Id), nil
}

// diffSsoGroupRoles compares the planned roles with the roles in state, by
// key. add holds the planned roles that state does not hold, and remove holds
// the state roles that the plan does not hold. A role whose scope id changes
// only in case, or whose scope changes from omitted to an explicit
// organization, has the same key, so it is in neither list: SELECT holds the
// same grant for both values.
func diffSsoGroupRoles(plan, state []ssoGroupRole) (add, remove []ssoGroupRole) {
	planKeys := make(map[ssoGroupRoleKey]bool, len(plan))
	for _, role := range plan {
		planKeys[role.key] = true
	}
	stateKeys := make(map[ssoGroupRoleKey]bool, len(state))
	for _, role := range state {
		stateKeys[role.key] = true
	}

	for _, role := range plan {
		if !stateKeys[role.key] {
			add = append(add, role)
		}
	}
	for _, role := range state {
		if !planKeys[role.key] {
			remove = append(remove, role)
		}
	}
	return add, remove
}

// ssoGroupGrantsFor returns the listed grants that have the key. A grant on a
// scope type that this provider does not know cannot match a configured role,
// so it is skipped.
func ssoGroupGrantsFor(key ssoGroupRoleKey, grants []ssoGroupRoleResponse) []ssoGroupRoleResponse {
	var matches []ssoGroupRoleResponse
	for i := range grants {
		grantKey, err := ssoGroupGrantKey(&grants[i])
		if err == nil && grantKey == key {
			matches = append(matches, grants[i])
		}
	}
	return matches
}

// withoutSsoGroupRole returns roles without the elements that have the key.
func withoutSsoGroupRole(roles []ssoGroupRole, key ssoGroupRoleKey) []ssoGroupRole {
	kept := make([]ssoGroupRole, 0, len(roles))
	for _, role := range roles {
		if role.key != key {
			kept = append(kept, role)
		}
	}
	return kept
}

// ssoGroupRoleObjectType is the type of one element of the roles set, taken
// from the schema so the two cannot differ.
func ssoGroupRoleObjectType() types.ObjectType {
	return ssoGroupRolesAttribute().NestedObject.Type().(types.ObjectType)
}

// ssoGroupRolesSet builds the roles set from elements.
func ssoGroupRolesSet(ctx context.Context, roles []ssoGroupRole) (types.Set, diag.Diagnostics) {
	models := make([]ssoGroupRoleModel, 0, len(roles))
	for _, role := range roles {
		models = append(models, role.model)
	}
	return types.SetValueFrom(ctx, ssoGroupRoleObjectType(), models)
}

// ssoGroupRolesValue builds the roles set from the grants the API listed. The
// list is authoritative: a grant made outside Terraform is in the set, so the
// next plan shows it as drift.
//
// configured is the roles set that state holds. Each grant is paired with the
// configured element that has the same key, so that roleGrantScopeValue keeps
// the configured scope: a null scope stays null for an organization grant,
// and the scope id keeps its configured case. A grant with no configured
// element, as after `terraform import`, gets a null scope for the
// organization. Two grants with the same key give one element, because a set
// cannot hold the same value twice, and a warning.
func ssoGroupRolesValue(ctx context.Context, configured []ssoGroupRole, grants []ssoGroupRoleResponse) (types.Set, diag.Diagnostics) {
	var diags diag.Diagnostics
	byKey := make(map[ssoGroupRoleKey]ssoGroupRoleModel, len(configured))
	for _, role := range configured {
		byKey[role.key] = role.model
	}

	seen := map[ssoGroupRoleKey]bool{}
	models := make([]ssoGroupRoleModel, 0, len(grants))
	for i := range grants {
		grant := &grants[i]
		key, err := ssoGroupGrantKey(grant)
		if err != nil {
			diags.AddError("Unexpected Role Grant Scope", err.Error())
			return types.SetNull(ssoGroupRoleObjectType()), diags
		}
		if seen[key] {
			diags.AddWarning("Duplicate SSO Group Role",
				fmt.Sprintf("SELECT holds more than one grant of %q on the same scope for this SSO group. "+
					"The roles set shows it once. Removing that role from the configuration revokes every "+
					"one of these grants.", key.Role))
			continue
		}
		seen[key] = true

		configuredScope := types.ObjectNull(roleGrantScopeAttrTypes())
		if model, ok := byKey[key]; ok {
			configuredScope = model.Scope
		}
		scope, d := roleGrantScopeValue(ctx, configuredScope, grant.roleGrantScopeFields)
		diags.Append(d...)
		if diags.HasError() {
			return types.SetNull(ssoGroupRoleObjectType()), diags
		}
		models = append(models, ssoGroupRoleModel{Role: types.StringValue(grant.Role), Scope: scope})
	}

	set, d := types.SetValueFrom(ctx, ssoGroupRoleObjectType(), models)
	diags.Append(d...)
	return set, diags
}

func buildSsoGroupCreate(name types.String, roles []ssoGroupRole) *ssoGroupCreatePayload {
	payload := &ssoGroupCreatePayload{
		Name:  name.ValueString(),
		Roles: make([]roleGrantCreatePayload, 0, len(roles)),
	}
	for _, role := range roles {
		payload.Roles = append(payload.Roles, role.payload)
	}
	return payload
}

// applySsoGroupResponse writes the group's fields from an API response onto
// the model. The response does not carry the roles.
func applySsoGroupResponse(model *ssoGroupModel, response *ssoGroupResponse) {
	model.Id = types.StringValue(response.Id)
	model.Name = types.StringValue(response.Name)
	model.Etag = types.StringValue(response.Etag)
	model.CreateTime = types.StringValue(response.CreateTime)
	model.UpdateTime = types.StringValue(response.UpdateTime)
}

// ssoGroupConfiguredRoleKey is the key of a configured element. known is
// false when a value that the key needs is unknown at plan time.
func ssoGroupConfiguredRoleKey(ctx context.Context, value attr.Value) (key ssoGroupRoleKey, known bool, diags diag.Diagnostics) {
	object, ok := value.(types.Object)
	if !ok || object.IsNull() || object.IsUnknown() {
		return key, false, nil
	}
	var model ssoGroupRoleModel
	diags = object.As(ctx, &model, basetypes.ObjectAsOptions{})
	if diags.HasError() || model.Role.IsUnknown() || model.Role.IsNull() || model.Scope.IsUnknown() {
		return key, false, diags
	}
	if model.Scope.IsNull() {
		return newSsoGroupRoleKey(model.Role.ValueString(), roleGrantScopeOrganization, nil), true, diags
	}

	var scope roleGrantScopeModel
	diags.Append(model.Scope.As(ctx, &scope, basetypes.ObjectAsOptions{})...)
	if diags.HasError() || scope.Type.IsUnknown() || scope.Id.IsUnknown() {
		return key, false, diags
	}
	return newSsoGroupRoleKey(model.Role.ValueString(), scope.Type.ValueString(), stringPointer(scope.Id)), true, diags
}

// validateSsoGroupConfig runs the plan-time checks that the schema cannot
// express. Two elements of the roles set must not grant the same role on the
// same scope: SELECT holds one grant for both, so Terraform could not read
// back two elements. Elements that differ only in the case of the scope id,
// or in an omitted versus an explicit organization scope, are the same grant.
//
// The usage_group → viewer rule of select_team_role is not checked here: the
// spec states it only for team role grants.
func validateSsoGroupConfig(ctx context.Context, config *ssoGroupModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if config.Roles.IsNull() || config.Roles.IsUnknown() {
		return diags
	}

	seen := map[ssoGroupRoleKey]bool{}
	for _, element := range config.Roles.Elements() {
		key, known, d := ssoGroupConfiguredRoleKey(ctx, element)
		diags.Append(d...)
		if diags.HasError() {
			return diags
		}
		if !known {
			continue
		}
		if seen[key] {
			diags.AddAttributeError(path.Root("roles"), "Duplicate SSO Group Role",
				fmt.Sprintf("roles grants %q on the same scope more than once. SELECT holds one grant "+
					"for each role and scope. Scope ids that differ only in case are the same scope, and "+
					"an omitted scope is the same as type = \"organization\".", key.Role))
			continue
		}
		seen[key] = true
	}
	return diags
}
