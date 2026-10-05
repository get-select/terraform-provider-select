// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"terraform-provider-select/internal/provider/resource_default_role"
)

func defaultRoleResourceSchema(ctx context.Context) schema.Schema {
	return withRoleGrantScope(resource_default_role.DefaultRoleResourceSchema(ctx))
}

func NewDefaultRoleResource() resource.Resource {
	return &v2Resource[defaultRoleModel, defaultRoleResponse]{
		typeNameSuffix:     "_default_role",
		schema:             defaultRoleResourceSchema,
		errors:             defaultRoleErrors,
		specificDiagnostic: roleGrantConflict(defaultRoleErrors, "select_default_role", "<role_id>"),
		collectionEndpoint: func(*defaultRoleModel) string { return defaultRolesEndpoint },
		itemEndpoint: func(m *defaultRoleModel) string {
			return defaultRoleEndpoint(m.Id.ValueString())
		},
		// There is no GET for one default grant, so Read lists the default
		// grants and finds this one by id. A grant that is not in the list is
		// removed from state.
		fetch: v2ListAndFind(
			func(*defaultRoleModel) string { return defaultRolesEndpoint },
			func(m *defaultRoleModel) string { return m.Id.ValueString() },
			func(r *defaultRoleResponse) string { return r.Id },
		),
		identity: func(m *defaultRoleModel) v2Identity {
			return v2Identity{Id: m.Id, Etag: m.Etag}
		},
		createPayload: v2FalliblePayload(buildDefaultRoleCreate),
		updatePayload: v2NoUpdate[defaultRoleModel]("default role grant"),
		applyResponse: applyDefaultRoleResponse,
	}
}
