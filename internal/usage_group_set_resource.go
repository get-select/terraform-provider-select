// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"terraform-provider-select/internal/provider/resource_usage_group_set"
)

func NewUsageGroupSetResource() resource.Resource {
	return &v2Resource[resource_usage_group_set.UsageGroupSetModel, usageGroupSetResponse]{
		typeNameSuffix:     "_usage_group_set",
		schema:             resource_usage_group_set.UsageGroupSetResourceSchema,
		errors:             usageGroupSetErrors,
		specificDiagnostic: nil,
		collectionEndpoint: func(*resource_usage_group_set.UsageGroupSetModel) string {
			return usageGroupSetsEndpoint
		},
		itemEndpoint: func(m *resource_usage_group_set.UsageGroupSetModel) string {
			return usageGroupSetEndpoint(m.Id.ValueString())
		},
		identity: func(m *resource_usage_group_set.UsageGroupSetModel) v2Identity {
			return v2Identity{Id: m.Id, Etag: m.Etag}
		},
		// A set never records a version of its own accord. A version is a copy of
		// the set's groups, and nothing this resource writes — name, order, team,
		// visibility — changes them, so there is no prepareWrite here. It only has
		// to survive a version one of its groups recorded, which rotates this
		// set's ETag underneath an update that had already read the old one.
		selfInflicted412: func(client *APIClient, m *resource_usage_group_set.UsageGroupSetModel) bool {
			return client.VersionRecorded(m.Id.ValueString())
		},
		createPayload: v2Payload(buildUsageGroupSetCreate),
		updatePayload: v2Patch(buildUsageGroupSetUpdate),
		applyResponse: applyUsageGroupSetResponse,
	}
}
