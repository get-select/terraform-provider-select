// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"terraform-provider-select/internal/provider/resource_usage_group"
)

// Usage groups live on the v2 API, nested under the set they belong to. See
// v2_api.go for the conventions every resource on that surface shares.
func usageGroupsEndpoint(setId string) string {
	return fmt.Sprintf("%s/usage-groups", usageGroupSetEndpoint(setId))
}

func usageGroupEndpoint(setId, id string) string {
	return fmt.Sprintf("%s/%s", usageGroupsEndpoint(setId), id)
}

// usageGroupErrors words the failures every v2 resource can hit. Both usage
// group routes answer a conflict with the shared Conflict response rather than
// anything group-specific, so there is no specificDiagnostic for this resource:
// the problem document's own detail says more than a guess about the cause
// would.
var usageGroupErrors = v2ErrorFormat{
	Noun:       "Usage Group",
	Subject:    "the usage group",
	Object:     "the usage group",
	ReadScope:  "usage_groups:read",
	WriteScope: "usage_groups:write",
}

// usageGroupCreatePayload mirrors UsageGroupCreateV2.
//
// There is deliberately no filter_expression field. The API 422s ("Use exactly
// one of filter_expression or filter_expression_json") when both keys are
// present, and counts an explicit null as present, so this resource must never
// emit the key at all — filter_expression_json is its whole filter surface. See
// generator_config.v2.yml.
type usageGroupCreatePayload struct {
	Name                 string   `json:"name"`
	Order                int64    `json:"order"`
	FilterExpressionJson string   `json:"filter_expression_json"`
	Budget               *float64 `json:"budget,omitempty"`
}

// usageGroupUpdatePayload mirrors UsageGroupUpdateV2, a JSON Merge Patch body
// where an omitted field is left unchanged. Each field is sent only when
// changed.
//
// budget is the one field here the API lets a caller clear, so it uses
// *nullableNumber for the three-state omit/null/value a plain pointer cannot
// express. filter_expression_json needs no such treatment: the attribute is
// required, so a configuration cannot remove it. And as on create, there is no
// filter_expression field.
type usageGroupUpdatePayload struct {
	Name                 *string         `json:"name,omitempty"`
	Order                *int64          `json:"order,omitempty"`
	FilterExpressionJson *string         `json:"filter_expression_json,omitempty"`
	Budget               *nullableNumber `json:"budget,omitempty"`
}

// usageGroupResponse mirrors UsageGroupV2. filter_expression is deliberately
// not decoded: the structured form carries the same filter this resource
// already holds as JSON, and nothing here reads it.
type usageGroupResponse struct {
	Id                   string   `json:"id"`
	Etag                 string   `json:"etag"`
	UsageGroupSetId      string   `json:"usage_group_set_id"`
	Name                 string   `json:"name"`
	Order                int64    `json:"order"`
	Budget               *float64 `json:"budget"`
	FilterExpressionJson *string  `json:"filter_expression_json"`
	CreateTime           string   `json:"create_time"`
	UpdateTime           string   `json:"update_time"`
}

func buildUsageGroupCreate(plan *resource_usage_group.UsageGroupModel) *usageGroupCreatePayload {
	return &usageGroupCreatePayload{
		Name:                 plan.Name.ValueString(),
		Order:                plan.Order.ValueInt64(),
		FilterExpressionJson: plan.FilterExpressionJson.ValueString(),
		Budget:               numberPointer(plan.Budget),
	}
}

// buildUsageGroupUpdate carries only the fields whose configured value differs
// from what state records. See usageGroupUpdatePayload.
func buildUsageGroupUpdate(plan, state *resource_usage_group.UsageGroupModel) *usageGroupUpdatePayload {
	return &usageGroupUpdatePayload{
		Name:                 changedString(plan.Name, state.Name),
		Order:                changedInt64(plan.Order, state.Order),
		FilterExpressionJson: changedString(plan.FilterExpressionJson, state.FilterExpressionJson),
		Budget:               clearedNumber(plan.Budget, state.Budget),
	}
}

// applyUsageGroupResponse writes an API response onto the model. Two fields
// need drift suppression, or an apply that only touched something else would
// fail with "provider produced inconsistent result": filter_expression_json,
// since the API re-encodes it from the structured filter it stores and reorders
// leaf keys along the way; and budget, since Terraform's configured precision
// differs from a float64 round trip.
func applyUsageGroupResponse(ctx context.Context, model, source *resource_usage_group.UsageGroupModel, response *usageGroupResponse) diag.Diagnostics {
	model.Id = types.StringValue(response.Id)
	model.Etag = types.StringValue(response.Etag)
	model.UsageGroupSetId = types.StringValue(response.UsageGroupSetId)
	model.Name = types.StringValue(response.Name)
	model.Order = types.Int64Value(response.Order)
	model.Budget = preserveEquivalentNumber(source.Budget, response.Budget)
	model.FilterExpressionJson = preserveEquivalentJSON(source.FilterExpressionJson, response.FilterExpressionJson)
	model.CreateTime = types.StringValue(response.CreateTime)
	model.UpdateTime = types.StringValue(response.UpdateTime)

	return nil
}
