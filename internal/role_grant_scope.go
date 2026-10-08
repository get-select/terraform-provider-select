// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// The `scope` attribute that every role grant shares: a team's roles, a
// user's roles, the organization's default roles, and an SSO group's roles.
// The API takes a scope as RoleGrantScope {type, id} on a create, but it
// returns a grant's scope in a different shape: an `entity` object plus three
// flat columns. The functions here convert between the two, so each role
// grant resource does not do it again.
//
// The scope is hand-written, not generated. RoleGrantScope is only in the
// create request schemas, and the response does not carry it, so the
// generator cannot produce one attribute that both sides agree on.

// roleGrantScopeOrganization is the scope type for a grant on the whole
// organization. It is also what an omitted scope means.
const roleGrantScopeOrganization = "organization"

// roleGrantScopeTypes mirrors RoleGrantScopeType.
var roleGrantScopeTypes = []string{
	roleGrantScopeOrganization,
	"snowflake_organization",
	"snowflake_account",
	"databricks_account",
	"databricks_connection",
	"bigquery_connection",
	"aws_account",
	"tableau_site",
	"usage_group",
}

// roleGrantScopeModel is the scope attribute's shape in a Terraform model.
type roleGrantScopeModel struct {
	Type types.String `tfsdk:"type"`
	Id   types.String `tfsdk:"id"`
}

// roleGrantScopeAttribute is the scope attribute. It is optional: a grant with
// no scope applies to the whole organization.
//
// Use it as a top-level attribute of a resource, or as an attribute inside the
// element of a set or list nested attribute. The function sets no plan
// modifiers. The API cannot change the scope of a grant, so a top-level
// caller adds objectplanmodifier.RequiresReplace() to the returned value.
func roleGrantScopeAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional: true,
		Description: "The resource the role applies to. Omit it to grant the role on the whole " +
			"organization. `id` is required for every `type` except `organization`.",
		Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{
				Required: true,
				Description: "The kind of resource the grant applies to: `organization`, " +
					"`snowflake_organization`, `snowflake_account`, `databricks_account`, " +
					"`databricks_connection`, `bigquery_connection`, `aws_account`, " +
					"`tableau_site` or `usage_group`.",
				Validators: []validator.String{
					stringvalidator.OneOf(roleGrantScopeTypes...),
				},
			},
			"id": schema.StringAttribute{
				Optional: true,
				Description: "The resource's identifier: the Snowflake organization name, the " +
					"Snowflake account UUID, the Databricks account id, or the id SELECT gave the " +
					"Databricks connection, BigQuery connection, AWS account, Tableau site (its " +
					"LUID) or usage group. Leave it unset for `organization`.",
			},
		},
		Validators: []validator.Object{
			roleGrantScopeValidator{},
		},
	}
}

// roleGrantScopeAttrTypes is the attribute types of the scope object, taken
// from roleGrantScopeAttribute so the two cannot differ. A caller that nests
// the scope in a set element needs it to build that element's object type.
func roleGrantScopeAttrTypes() map[string]attr.Type {
	return roleGrantScopeAttribute().GetType().(types.ObjectType).AttrTypes
}

// roleGrantScopeValidator rejects at plan time the scopes the API rejects: a
// scope with no id for a type that needs one, and an id on the organization
// scope. The API would accept an organization id but return none, which
// Terraform reports as an inconsistent result.
type roleGrantScopeValidator struct{}

func (v roleGrantScopeValidator) Description(context.Context) string {
	return "id must be set for every scope type except organization, and unset for organization"
}

func (v roleGrantScopeValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v roleGrantScopeValidator) ValidateObject(ctx context.Context, req validator.ObjectRequest, resp *validator.ObjectResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	var scope roleGrantScopeModel
	resp.Diagnostics.Append(req.ConfigValue.As(ctx, &scope, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateRoleGrantScope(req.Path, scope)...)
}

// validateRoleGrantScope is the check roleGrantScopeValidator runs. A value
// that is unknown at plan time is not checked; the API still checks it.
func validateRoleGrantScope(at path.Path, scope roleGrantScopeModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if scope.Type.IsUnknown() || scope.Id.IsUnknown() {
		return diags
	}

	scopeType := scope.Type.ValueString()
	hasId := !scope.Id.IsNull() && scope.Id.ValueString() != ""
	switch {
	case scopeType == roleGrantScopeOrganization && hasId:
		diags.AddAttributeError(at.AtName("id"), "Invalid Role Grant Scope",
			"scope.id must be unset when scope.type is \"organization\". The grant applies to the "+
				"organization the provider is configured for.")
	case scopeType != roleGrantScopeOrganization && !hasId:
		diags.AddAttributeError(at.AtName("id"), "Invalid Role Grant Scope",
			fmt.Sprintf("scope.id is required when scope.type is %q.", scopeType))
	}
	return diags
}

// roleGrantScopePayload mirrors RoleGrantScope, the scope in a role grant's
// create request.
type roleGrantScopePayload struct {
	Type string  `json:"type"`
	Id   *string `json:"id,omitempty"`
}

// buildRoleGrantScope converts the configured scope into the request shape.
// It returns nil for a null scope. The create payload then omits the key,
// which grants the role on the whole organization.
func buildRoleGrantScope(ctx context.Context, scope types.Object) (*roleGrantScopePayload, diag.Diagnostics) {
	if scope.IsNull() || scope.IsUnknown() {
		return nil, nil
	}

	var model roleGrantScopeModel
	diags := scope.As(ctx, &model, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return nil, diags
	}
	return &roleGrantScopePayload{
		Type: model.Type.ValueString(),
		Id:   stringPointer(model.Id),
	}, diags
}

// roleGrantEntity mirrors PermissionEntityWithDisplayName, the resolved scope
// the API returns on every role grant.
type roleGrantEntity struct {
	Type        string `json:"type"`
	Id          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// roleGrantScopeFields are the response fields that every role grant schema
// (TeamRoleGrantV2, UserRoleGrantV2, DefaultRoleGrantV2, SsoGroupRoleGrantV2)
// uses to tell its scope. Embed this struct, without a tag, in a grant's
// response struct: encoding/json then decodes these fields at the top level.
type roleGrantScopeFields struct {
	Entity                    roleGrantEntity `json:"entity"`
	SnowflakeOrganizationName *string         `json:"snowflake_organization_name"`
	SnowflakeAccountUuid      *string         `json:"snowflake_account_uuid"`
	UsageGroupId              *string         `json:"usage_group_id"`
}

// roleGrantEntityTypes maps a PermissionEntityType to the RoleGrantScopeType
// that a create request uses for it. The two enums are the same except for
// the organization, which the entity calls "select_organization". The entity
// type "team" has no scope type: a role cannot be granted on a team.
var roleGrantEntityTypes = map[string]string{
	"select_organization":    roleGrantScopeOrganization,
	"snowflake_organization": "snowflake_organization",
	"snowflake_account":      "snowflake_account",
	"databricks_account":     "databricks_account",
	"databricks_connection":  "databricks_connection",
	"bigquery_connection":    "bigquery_connection",
	"aws_account":            "aws_account",
	"tableau_site":           "tableau_site",
	"usage_group":            "usage_group",
}

// roleGrantScopeFromResponse converts a grant's response fields back into the
// scope a create request would send for it. This is the inverse of
// buildRoleGrantScope.
//
// The entity is the main source. For the three scope types that also have a
// flat column, the column is used for the id when it is set, because it holds
// the value as it was stored. When the entity has no type, the flat columns
// alone give the scope, from the narrowest to the widest. The organization
// scope has no id.
func roleGrantScopeFromResponse(fields roleGrantScopeFields) (roleGrantScopePayload, error) {
	if fields.Entity.Type == "" {
		switch {
		case fields.UsageGroupId != nil:
			return roleGrantScopePayload{Type: "usage_group", Id: fields.UsageGroupId}, nil
		case fields.SnowflakeAccountUuid != nil:
			return roleGrantScopePayload{Type: "snowflake_account", Id: fields.SnowflakeAccountUuid}, nil
		case fields.SnowflakeOrganizationName != nil:
			return roleGrantScopePayload{Type: "snowflake_organization", Id: fields.SnowflakeOrganizationName}, nil
		default:
			return roleGrantScopePayload{Type: roleGrantScopeOrganization}, nil
		}
	}

	scopeType, ok := roleGrantEntityTypes[fields.Entity.Type]
	if !ok {
		return roleGrantScopePayload{}, fmt.Errorf("the API returned a role grant on a %q, which is not a scope type this provider knows", fields.Entity.Type)
	}
	if scopeType == roleGrantScopeOrganization {
		return roleGrantScopePayload{Type: scopeType}, nil
	}

	id := fields.Entity.Id
	var column *string
	switch scopeType {
	case "snowflake_organization":
		column = fields.SnowflakeOrganizationName
	case "snowflake_account":
		column = fields.SnowflakeAccountUuid
	case "usage_group":
		column = fields.UsageGroupId
	}
	if column != nil && *column != "" {
		id = *column
	}
	return roleGrantScopePayload{Type: scopeType, Id: &id}, nil
}

// roleGrantScopeValue builds the scope attribute's value from a grant's
// response. configured is the scope the plan or the prior state holds.
//
// A null configured scope stays null when the API reports the organization
// scope, because an omitted scope means the organization. That is also the
// case after a `terraform import`. The id keeps its configured spelling when
// it differs from the API's only in case: a Snowflake identifier can come
// back in a different case, and Terraform would report that as an
// inconsistent result.
func roleGrantScopeValue(ctx context.Context, configured types.Object, fields roleGrantScopeFields) (types.Object, diag.Diagnostics) {
	attrTypes := roleGrantScopeAttrTypes()

	scope, err := roleGrantScopeFromResponse(fields)
	if err != nil {
		return types.ObjectNull(attrTypes), diag.Diagnostics{
			diag.NewErrorDiagnostic("Unexpected Role Grant Scope", err.Error()),
		}
	}

	if scope.Type == roleGrantScopeOrganization && (configured.IsNull() || configured.IsUnknown()) {
		return types.ObjectNull(attrTypes), nil
	}

	configuredId := types.StringNull()
	if !configured.IsNull() && !configured.IsUnknown() {
		var model roleGrantScopeModel
		diags := configured.As(ctx, &model, basetypes.ObjectAsOptions{})
		if diags.HasError() {
			return types.ObjectNull(attrTypes), diags
		}
		configuredId = model.Id
	}

	return types.ObjectValueFrom(ctx, attrTypes, roleGrantScopeModel{
		Type: types.StringValue(scope.Type),
		Id:   preserveEquivalentFold(configuredId, scope.Id),
	})
}
