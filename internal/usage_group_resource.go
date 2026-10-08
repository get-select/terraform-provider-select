// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"terraform-provider-select/internal/provider/resource_usage_group"
)

func NewUsageGroupResource() resource.Resource {
	return &v2Resource[resource_usage_group.UsageGroupModel, usageGroupResponse]{
		typeNameSuffix:     "_usage_group",
		schema:             resource_usage_group.UsageGroupResourceSchema,
		errors:             usageGroupErrors,
		specificDiagnostic: nil,
		collectionEndpoint: func(m *resource_usage_group.UsageGroupModel) string {
			return usageGroupsEndpoint(m.UsageGroupSetId.ValueString())
		},
		itemEndpoint: func(m *resource_usage_group.UsageGroupModel) string {
			return usageGroupEndpoint(m.UsageGroupSetId.ValueString(), m.Id.ValueString())
		},
		identity: func(m *resource_usage_group.UsageGroupModel) v2Identity {
			return v2Identity{Id: m.Id, Etag: m.Etag}
		},
		prepareWrite:     recordUsageGroupSetVersion,
		selfInflicted412: usageGroupVersionRecorded,
		importState:      importUsageGroup,
		createPayload:    v2Payload(buildUsageGroupCreate),
		updatePayload:    v2Patch(buildUsageGroupUpdate),
		applyResponse:    applyUsageGroupResponse,
		validateConfig:   validateUsageGroupConfig,
	}
}

// recordUsageGroupSetVersion checkpoints the set's groups before this apply
// changes any of them. Every write to a group runs it; only the first for a
// given set does any work. See APIClient.EnsureVersion for why this has to
// happen inside a write rather than once at the start of the apply.
//
// This runs before Create's request is sent, so a create the API goes on to
// reject with a 422 still burns a version — confirmed against the local API
// that POST .../versions is not idempotent (two calls with nothing changed in
// between still produce two distinct version records) and that there is no
// DELETE on a version to undo one after the fact. Moving the version after
// the create would not help: the version's content is a server-side snapshot
// taken when the POST lands, not something this provider sends, so recording
// it after a successful create would check-point the set including the group
// that create just added rather than the pre-apply state EnsureVersion exists
// to preserve. Catching the 422 here ahead of time would mean reimplementing
// the API's own filter validation client-side, which validateUsageGroupConfig
// deliberately does not do (see its docstring). Every other cost of a version
// already runs before this call: createPayload and ValidateConfig, both of
// which report a locally-detectable problem before EnsureVersion is reached.
// A configuration with a persistent mistake still burns a version per retry;
// there is no fix available on the client side of this API.
func recordUsageGroupSetVersion(ctx context.Context, client *APIClient, model *resource_usage_group.UsageGroupModel) diag.Diagnostics {
	return client.EnsureVersion(ctx, model.UsageGroupSetId.ValueString())
}

// usageGroupVersionRecorded reports whether this apply has recorded a version
// of the group's set, which is what makes a precondition failure this
// provider's own doing rather than a change someone else made.
func usageGroupVersionRecorded(client *APIClient, model *resource_usage_group.UsageGroupModel) bool {
	return client.VersionRecorded(model.UsageGroupSetId.ValueString())
}

// importUsageGroup reads a `terraform import` address of the form
// `usage_group_set_id/usage_group_id`. A group is addressed through its set on
// every route, so its own id is not enough to find it.
var importUsageGroup = v2ImportChild("Usage Group", "usage_group_set_id", "usage_group_set_id/usage_group_id")

// validateUsageGroupConfig rejects at plan time a filter the API would reject
// on the way in, so a mistake costs a plan rather than a round trip. Only the
// encoding is checked here; whether the filter names real dimensions is the
// API's to judge.
func validateUsageGroupConfig(ctx context.Context, config *resource_usage_group.UsageGroupModel) diag.Diagnostics {
	var diags diag.Diagnostics

	expression := config.FilterExpressionJson
	if expression.IsNull() || expression.IsUnknown() {
		return diags
	}

	if !json.Valid([]byte(expression.ValueString())) {
		diags.AddAttributeError(
			path.Root("filter_expression_json"),
			"Invalid Usage Group Filter",
			"filter_expression_json must be a JSON-encoded filter.",
		)
	}

	return diags
}
