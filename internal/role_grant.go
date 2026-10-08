// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// The parts that every role grant shares, other than the scope (see
// role_grant_scope.go): the create request, the common response fields, the
// role check for a scope, and the conflict diagnostic. A team's roles, a
// user's roles, the default roles and an SSO group's roles all use them.

// roleGrantViewer is the only role the API accepts on a usage_group scope.
const roleGrantViewer = "viewer"

// roleGrantRoles mirrors AccessRole, the roles a grant can give. The
// generated schemas validate their own role attribute. A hand-written role
// attribute, such as the role in an SSO group's roles set, uses this list.
var roleGrantRoles = []string{"admin", "editor", "monitor_editor", "viewer", "team_creator"}

// roleGrantCreatePayload mirrors the create request that every role grant
// route takes: TeamRoleGrantCreateV2, UserRoleGrantCreateV2,
// DefaultRoleGrantCreateV2 and SsoGroupRoleGrantCreateV2 have the same two
// fields. A nil Scope omits the key, which grants the role on the whole
// organization.
type roleGrantCreatePayload struct {
	Role  string                 `json:"role"`
	Scope *roleGrantScopePayload `json:"scope,omitempty"`
}

// buildRoleGrantCreate builds the create request from a configured role and
// scope.
func buildRoleGrantCreate(ctx context.Context, role types.String, scope types.Object) (*roleGrantCreatePayload, diag.Diagnostics) {
	payload, diags := buildRoleGrantScope(ctx, scope)
	if diags.HasError() {
		return nil, diags
	}
	return &roleGrantCreatePayload{Role: role.ValueString(), Scope: payload}, diags
}

// roleGrantResponse holds the fields that every role grant response has:
// TeamRoleGrantV2, UserRoleGrantV2, DefaultRoleGrantV2 and
// SsoGroupRoleGrantV2. Embed it, without a tag, in a grant's response struct,
// and add the fields that only that grant has.
type roleGrantResponse struct {
	Id   string `json:"id"`
	Role string `json:"role"`
	roleGrantScopeFields
	CreateTime string `json:"create_time"`
}

// roleGrantScopeWithReplace is the scope attribute for a resource that holds
// one grant. The API cannot change the scope of a grant, so a change replaces
// the grant.
func roleGrantScopeWithReplace() schema.SingleNestedAttribute {
	attribute := roleGrantScopeAttribute()
	attribute.Description += " Changing it forces a new grant."
	attribute.PlanModifiers = []planmodifier.Object{objectplanmodifier.RequiresReplace()}
	return attribute
}

// withRoleGrantScope adds the scope attribute to a generated role grant
// schema. The generator ignores the API's scope fields (see
// generator_config.v2.yml), because the request and the response do not agree
// on one shape. Each call to a generated schema function builds a new
// attribute map, so this changes no shared value.
func withRoleGrantScope(s schema.Schema) schema.Schema {
	s.Attributes["scope"] = roleGrantScopeWithReplace()
	return s
}

// validateRoleGrantRole rejects at plan time a role that the API rejects on
// the configured scope: a usage_group scope accepts only viewer. at is the
// path of the role attribute. A null scope means the organization, which
// accepts every role. A value that is unknown at plan time is not checked;
// the API still checks it.
//
// The function takes the role and the scope as values, not as a model, so
// that the element of a set of roles can use it too.
func validateRoleGrantRole(ctx context.Context, at path.Path, role types.String, scope types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	if role.IsNull() || role.IsUnknown() || scope.IsNull() || scope.IsUnknown() {
		return diags
	}

	var model roleGrantScopeModel
	diags.Append(scope.As(ctx, &model, basetypes.ObjectAsOptions{})...)
	if diags.HasError() || model.Type.IsUnknown() {
		return diags
	}

	if model.Type.ValueString() == "usage_group" && role.ValueString() != roleGrantViewer {
		diags.AddAttributeError(at, "Invalid Role For Scope",
			fmt.Sprintf("A usage_group scope accepts only the %q role, got %q.", roleGrantViewer, role.ValueString()))
	}
	return diags
}

// roleGrantConflict is the specificDiagnostic for a role grant resource. The
// spec documents a 409 on every grant create route but does not say what
// causes it. A grant that already exists is one possible cause, so the
// diagnostic tells how to import that grant. resourceType is the Terraform
// type, such as "select_team_role", and importForm is the import address
// form, such as "<team_id>/<role_id>".
//
// It only handles a 409 on create. A 409 on any other operation, and every
// other status, falls through to the shared wording in v2ErrorFormat.
func roleGrantConflict(f v2ErrorFormat, resourceType, importForm string) v2Diagnostic {
	return func(operation string, apiErr *apiError) diag.Diagnostic {
		// v2Resource.Create names its operation "add " + Object.
		if apiErr.StatusCode != http.StatusConflict || operation != "add "+f.Object {
			return nil
		}
		return diag.NewErrorDiagnostic(
			f.Noun+" Conflict",
			fmt.Sprintf("SELECT could not %s: %s\n\n"+
				"This can mean that the same role is already granted on the same scope. "+
				"If so, import the existing grant instead of creating it again:\n\n"+
				"  terraform import %s.<name> %s",
				operation, apiErr.Detail, resourceType, importForm),
		)
	}
}
