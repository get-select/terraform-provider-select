// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
func importUsageGroup(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	setId, id, found := strings.Cut(req.ID, "/")
	if !found || setId == "" || id == "" {
		resp.Diagnostics.AddError(
			"Invalid Usage Group Import ID",
			fmt.Sprintf("Expected an import ID of the form `usage_group_set_id/usage_group_id`, got %q.", req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("usage_group_set_id"), setId)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

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
