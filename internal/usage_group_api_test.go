// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"terraform-provider-select/internal/provider/resource_usage_group"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const probeFilter = `{"operator":"and","filters":[{"field":"warehouse_name","operator":"=","value":"DS_WH"}]}`

func usageGroup() resource_usage_group.UsageGroupModel {
	return resource_usage_group.UsageGroupModel{
		Id:                   types.StringValue("ug-1"),
		Etag:                 types.StringValue("etag-1"),
		UsageGroupSetId:      types.StringValue("ugs-1"),
		Name:                 types.StringValue("Data Science"),
		Order:                types.Int64Value(0),
		Budget:               types.NumberValue(big.NewFloat(1000)),
		FilterExpressionJson: types.StringValue(probeFilter),
		CreateTime:           types.StringValue("2026-09-21T00:00:00Z"),
		UpdateTime:           types.StringValue("2026-09-21T00:00:00Z"),
	}
}

// filter_expression is a recursive anyOf the generator cannot produce, so
// filter_expression_json has to be this resource's whole filter surface. If the
// generator ever grows support for it, the ignores entry can go — but until
// then its absence is what the hand-written payloads depend on.
func TestUsageGroupSchemaCarriesFilterExpressionJsonNotFilterExpression(t *testing.T) {
	attributes := resource_usage_group.UsageGroupResourceSchema(context.Background()).Attributes

	filter, ok := attributes["filter_expression_json"]
	if !ok {
		t.Fatal("filter_expression_json should be generated now that the API carries it on UsageGroupCreateV2/UpdateV2/V2")
	}
	if !filter.IsRequired() {
		t.Error("filter_expression_json should be required: every usage group has a filter, and this is the only way to give one")
	}
	if _, ok := attributes["filter_expression"]; ok {
		t.Fatal("filter_expression is a recursive anyOf the generator cannot produce and must stay in generator_config.v2.yml's ignores list")
	}
}

// usage_group_set_id is readOnly on UsageGroupV2, so the generator makes it
// computed — but it is how a configuration says which set to create the group
// in, and it is a path parameter on every route. The override in
// generator_overrides.v2.yml is what corrects it.
func TestUsageGroupSetIdIsRequiredAndForcesReplacement(t *testing.T) {
	attributes := resource_usage_group.UsageGroupResourceSchema(context.Background()).Attributes

	setId, ok := attributes["usage_group_set_id"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("usage_group_set_id should be a string attribute, got %T", attributes["usage_group_set_id"])
	}
	if !setId.Required {
		t.Error("usage_group_set_id should be required: a group cannot be created without naming its set")
	}

	// UsageGroupUpdateV2 does not carry usage_group_set_id, so the API has no
	// way to move a group between sets — a change has to replace the group
	// rather than silently leave it where it was.
	if len(setId.PlanModifiers) == 0 {
		t.Fatal("usage_group_set_id should carry a RequiresReplace plan modifier")
	}
	description := setId.PlanModifiers[0].Description(context.Background())
	if !strings.Contains(description, "destroy and recreate") {
		t.Errorf("usage_group_set_id's plan modifier should force replacement, got %q", description)
	}
}

func TestUsageGroupCreatePayloadNeverEmitsFilterExpression(t *testing.T) {
	plan := usageGroup()

	body := marshal(t, buildUsageGroupCreate(&plan))

	if _, present := body["filter_expression"]; present {
		t.Errorf("create payload must never carry filter_expression, got %v", body["filter_expression"])
	}
	if body["filter_expression_json"] != probeFilter {
		t.Errorf("filter_expression_json should carry the configured filter, got %v", body["filter_expression_json"])
	}
	if body["name"] != "Data Science" || body["order"] != float64(0) {
		t.Errorf("name and order should be sent as configured, got %v", body)
	}
	if body["budget"] != float64(1000) {
		t.Errorf("budget should be sent when set, got %v", body["budget"])
	}
}

// order is a meaningful zero: it is the highest precedence in a set, so
// omitempty on a plain int64 would silently drop it.
func TestUsageGroupCreatePayloadSendsZeroOrder(t *testing.T) {
	plan := usageGroup()
	plan.Order = types.Int64Value(0)

	body := marshal(t, buildUsageGroupCreate(&plan))

	if _, present := body["order"]; !present {
		t.Fatal("order 0 must reach the API: it is the highest cost-allocation precedence in a set")
	}
	if body["order"] != float64(0) {
		t.Errorf("order should be 0, got %v", body["order"])
	}
}

func TestUsageGroupCreatePayloadOmitsAbsentBudget(t *testing.T) {
	plan := usageGroup()
	plan.Budget = types.NumberNull()

	body := marshal(t, buildUsageGroupCreate(&plan))

	if _, present := body["budget"]; present {
		t.Errorf("a group with no budget should omit the key rather than send null, got %v", body["budget"])
	}
}

func TestUsageGroupUpdatePayloadOmitsUnchangedFields(t *testing.T) {
	state := usageGroup()
	plan := usageGroup()
	plan.Name = types.StringValue("Analytics")

	body := marshal(t, buildUsageGroupUpdate(&plan, &state))

	if body["name"] != "Analytics" {
		t.Errorf("a changed name should be sent, got %v", body["name"])
	}
	for _, field := range []string{"order", "budget", "filter_expression_json"} {
		if _, present := body[field]; present {
			t.Errorf("%s did not change and should be omitted from the merge patch, got %v", field, body[field])
		}
	}
}

func TestUsageGroupUpdatePayloadNeverEmitsFilterExpression(t *testing.T) {
	state := usageGroup()
	plan := usageGroup()
	plan.FilterExpressionJson = types.StringValue(`{"operator":"or","filters":[]}`)

	body := marshal(t, buildUsageGroupUpdate(&plan, &state))

	if _, present := body["filter_expression"]; present {
		t.Errorf("update payload must never carry filter_expression, got %v", body["filter_expression"])
	}
	if body["filter_expression_json"] != `{"operator":"or","filters":[]}` {
		t.Errorf("a changed filter should be sent, got %v", body["filter_expression_json"])
	}
}

// budget is the one field on a usage group the API lets a caller clear, so
// removing it from a configuration has to reach the API as an explicit null
// rather than being omitted, which would leave the old budget in place.
func TestUsageGroupUpdatePayloadClearsBudgetWithExplicitNull(t *testing.T) {
	state := usageGroup()
	plan := usageGroup()
	plan.Budget = types.NumberNull()

	body := marshal(t, buildUsageGroupUpdate(&plan, &state))

	value, present := body["budget"]
	if !present {
		t.Fatal("clearing a budget must send an explicit null, not omit the key")
	}
	if value != nil {
		t.Errorf("budget should be null, got %v", value)
	}
}

// The API re-encodes the filter from the structured form it stores, so the
// string that comes back is rarely byte-equal to what Terraform sent. Without
// this an apply that changed something else would fail with an
// inconsistent-result error.
func TestUsageGroupApplyResponsePreservesFilterKeyOrder(t *testing.T) {
	configured := `{"filters":[{"value":"DS_WH","operator":"=","field":"warehouse_name"}],"operator":"and"}`
	returned := probeFilter

	source := usageGroup()
	source.FilterExpressionJson = types.StringValue(configured)

	model := source
	response := usageGroupResponse{
		Id:                   "ug-1",
		Etag:                 "etag-2",
		UsageGroupSetId:      "ugs-1",
		Name:                 "Data Science",
		Order:                0,
		FilterExpressionJson: &returned,
		CreateTime:           "2026-09-21T00:00:00Z",
		UpdateTime:           "2026-09-21T01:00:00Z",
	}

	if diags := applyUsageGroupResponse(context.Background(), &model, &source, &response); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if model.FilterExpressionJson.ValueString() != configured {
		t.Errorf("an equivalent filter should keep the configured spelling, got %s", model.FilterExpressionJson.ValueString())
	}
	if model.Etag.ValueString() != "etag-2" {
		t.Errorf("etag should come from the response, got %s", model.Etag.ValueString())
	}
}

func TestUsageGroupApplyResponseTakesGenuinelyChangedFilter(t *testing.T) {
	returned := `{"operator":"or","filters":[]}`

	source := usageGroup()
	model := source
	response := usageGroupResponse{
		Id:                   "ug-1",
		Etag:                 "etag-2",
		UsageGroupSetId:      "ugs-1",
		Name:                 "Data Science",
		FilterExpressionJson: &returned,
	}

	if diags := applyUsageGroupResponse(context.Background(), &model, &source, &response); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if model.FilterExpressionJson.ValueString() != returned {
		t.Errorf("a filter that really differs should be taken from the response, got %s", model.FilterExpressionJson.ValueString())
	}
}

func TestUsageGroupApplyResponseNullsClearedBudget(t *testing.T) {
	source := usageGroup()
	model := source
	response := usageGroupResponse{
		Id:                   "ug-1",
		Etag:                 "etag-2",
		UsageGroupSetId:      "ugs-1",
		Name:                 "Data Science",
		Budget:               nil,
		FilterExpressionJson: &[]string{probeFilter}[0],
	}

	if diags := applyUsageGroupResponse(context.Background(), &model, &source, &response); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if !model.Budget.IsNull() {
		t.Errorf("a budget the API reports as absent should be null, got %v", model.Budget)
	}
}

func TestUsageGroupEndpointsAreNestedUnderTheirSet(t *testing.T) {
	if got := usageGroupsEndpoint("ugs-1"); got != "/v2/usage-group-sets/ugs-1/usage-groups" {
		t.Errorf("collection endpoint = %s", got)
	}
	if got := usageGroupEndpoint("ugs-1", "ug-1"); got != "/v2/usage-group-sets/ugs-1/usage-groups/ug-1" {
		t.Errorf("item endpoint = %s", got)
	}
}

func TestValidateUsageGroupConfigRejectsMalformedFilter(t *testing.T) {
	config := usageGroup()
	config.FilterExpressionJson = types.StringValue(`{"operator":"and",`)

	diags := validateUsageGroupConfig(context.Background(), &config)
	if !diags.HasError() {
		t.Fatal("a filter that is not JSON should fail at plan time")
	}
}

func TestValidateUsageGroupConfigAcceptsValidFilterAndUnknown(t *testing.T) {
	config := usageGroup()
	if diags := validateUsageGroupConfig(context.Background(), &config); diags.HasError() {
		t.Errorf("a valid filter should pass: %v", diags)
	}

	config.FilterExpressionJson = types.StringUnknown()
	if diags := validateUsageGroupConfig(context.Background(), &config); diags.HasError() {
		t.Errorf("a filter only known after apply cannot be checked here: %v", diags)
	}
}
