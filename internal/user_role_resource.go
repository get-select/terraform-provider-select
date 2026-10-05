// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"terraform-provider-select/internal/provider/resource_user_role"
)

func userRoleResourceSchema(ctx context.Context) schema.Schema {
	return withRoleGrantScope(resource_user_role.UserRoleResourceSchema(ctx))
}

func NewUserRoleResource() resource.Resource {
	return &v2Resource[userRoleModel, userRoleResponse]{
		typeNameSuffix:     "_user_role",
		schema:             userRoleResourceSchema,
		errors:             userRoleErrors,
		specificDiagnostic: roleGrantConflict(userRoleErrors, "select_user_role", "<email>/<role_id>"),
		collectionEndpoint: func(m *userRoleModel) string {
			return userRolesEndpoint(m.Email.ValueString())
		},
		itemEndpoint: func(m *userRoleModel) string {
			return userRoleEndpoint(m.Email.ValueString(), m.Id.ValueString())
		},
		fetch: fetchDirectUserRole,
		identity: func(m *userRoleModel) v2Identity {
			return v2Identity{Id: m.Id, Etag: m.Etag}
		},
		importState:   v2ImportChild("User Role", "email", "email/role_id"),
		createPayload: v2FalliblePayload(buildUserRoleCreate),
		updatePayload: v2Patch(buildUserRoleUpdate),
		applyResponse: applyUserRoleResponse,
	}
}
