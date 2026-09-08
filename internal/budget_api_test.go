// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"terraform-provider-select/internal/provider/resource_budget"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// budgetPeriod builds the period attribute's value the same way ObjectValueFrom
// does in budgetPeriodObject, so a test failure here would also mean
// applyBudgetResponse is broken.
func budgetPeriod(t *testing.T, model budgetPeriodModel) types.Object {
	t.Helper()
	ctx := context.Background()
	object, diags := types.ObjectValueFrom(ctx, budgetPeriodType(ctx).AttrTypes, model)
	if diags.HasError() {
		t.Fatalf("building period: %v", diags)
	}
	return object
}

func fixedRangePeriod(t *testing.T, startDate, endDate string) types.Object {
	return budgetPeriod(t, budgetPeriodModel{
		ScheduleType:            types.StringValue("fixed_range"),
		StartDate:               types.StringValue(startDate),
		EndDate:                 types.StringValue(endDate),
		RepeatEvery:             types.Int64Null(),
		RepeatUnit:              types.StringNull(),
		ExpiresOn:               types.StringNull(),
		ExpiresAfterOccurrences: types.Int64Null(),
	})
}

func weeklyPeriod(t *testing.T, startDate string, expiresOn types.String) types.Object {
	return budgetPeriod(t, budgetPeriodModel{
		ScheduleType:            types.StringValue("weekly"),
		StartDate:               types.StringValue(startDate),
		EndDate:                 types.StringNull(),
		RepeatEvery:             types.Int64Null(),
		RepeatUnit:              types.StringNull(),
		ExpiresOn:               expiresOn,
		ExpiresAfterOccurrences: types.Int64Null(),
	})
}

func customPeriod(t *testing.T, startDate string, repeatEvery int64, repeatUnit string) types.Object {
	return budgetPeriod(t, budgetPeriodModel{
		ScheduleType:            types.StringValue("custom"),
		StartDate:               types.StringValue(startDate),
		EndDate:                 types.StringNull(),
		RepeatEvery:             types.Int64Value(repeatEvery),
		RepeatUnit:              types.StringValue(repeatUnit),
		ExpiresOn:               types.StringNull(),
		ExpiresAfterOccurrences: types.Int64Null(),
	})
}

func fixedRangeBudget(t *testing.T) budgetModel {
	return budgetModel{
		BudgetModel: resource_budget.BudgetModel{
			Id:                   types.StringValue("2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01"),
			Etag:                 types.StringValue(`"abc123"`),
			Name:                 types.StringValue("Q1 Marketing"),
			Amount:               types.NumberValue(big.NewFloat(1000)),
			Status:               types.StringNull(),
			TeamId:               types.StringNull(),
			StartedAt:            types.StringValue("2026-01-01T00:00:00Z"),
			FilterExpressionJson: types.StringNull(),
			CurrentPeriodStart:   types.StringValue("2026-01-01"),
			CurrentPeriodEnd:     types.StringValue("2026-03-31"),
			BudgetEndDate:        types.StringNull(),
			CreatedBy:            types.StringValue("someone@acme.com"),
			UpdatedBy:            types.StringNull(),
			CreateTime:           types.StringValue("2026-01-01T00:00:00Z"),
			UpdateTime:           types.StringValue("2026-01-01T00:00:00Z"),
		},
		Period: fixedRangePeriod(t, "2026-01-01", "2026-03-31"),
	}
}

// The generator answers "schema composition is currently not supported" for a
// oneOf and drops the whole resource, so period is ignored in
// generator_config.v2.yml and hand-written in budget_resource.go instead. If
// tfplugingen-openapi ever learns to generate it, these fail as a canary: the
// hand-written attribute would then promote a field alongside a generated one
// of the same name, which getStructTags rejects as a duplicate tfsdk tag.
func TestBudgetCodegenOmitsPeriod(t *testing.T) {
	if _, ok := reflect.TypeOf(resource_budget.BudgetModel{}).FieldByName("Period"); ok {
		t.Fatal("the generator now produces a period field; drop the ignores entry in generator_config.v2.yml and the hand-written period attribute in budget_resource.go")
	}
	if _, ok := resource_budget.BudgetResourceSchema(context.Background()).Attributes["period"]; ok {
		t.Fatal("the generated schema now carries its own period attribute; see the comment above")
	}
}

func TestBudgetSchemaCarriesFilterExpressionJsonNotFilterExpression(t *testing.T) {
	attributes := resource_budget.BudgetResourceSchema(context.Background()).Attributes
	if _, ok := attributes["filter_expression_json"]; !ok {
		t.Fatal("filter_expression_json should be generated now that the API carries it on BudgetCreate/BudgetPatch/BudgetV2")
	}
	if _, ok := attributes["filter_expression"]; ok {
		t.Fatal("filter_expression is a recursive anyOf the generator cannot produce and must stay in generator_config.v2.yml's ignores list")
	}
}

func TestBudgetPeriodAttributeIsInjectedAndRequired(t *testing.T) {
	s := budgetResourceSchema(context.Background())

	attribute, ok := s.Attributes["period"]
	if !ok {
		t.Fatal("period should be injected into the generated schema")
	}
	if !attribute.IsRequired() {
		t.Error("period should be required: every budget has one")
	}
}

// Exercises the same reflect.Struct/FromStruct path plan.Get and state.Set use,
// so a duplicate or missing tfsdk tag on the embedded generated model would
// fail here rather than only inside a live plan.
func TestBudgetModelRoundTripsThroughSchema(t *testing.T) {
	ctx := context.Background()
	s := budgetResourceSchema(ctx)
	model := fixedRangeBudget(t)

	var value attr.Value
	if diags := tfsdk.ValueFrom(ctx, model, s.Type(), &value); diags.HasError() {
		t.Fatalf("converting model to the schema's type: %v", diags)
	}

	var roundTripped budgetModel
	if diags := tfsdk.ValueAs(ctx, value, &roundTripped); diags.HasError() {
		t.Fatalf("converting back to a model: %v", diags)
	}

	if !roundTripped.Id.Equal(model.Id) || !roundTripped.Name.Equal(model.Name) {
		t.Errorf("promoted fields should round-trip, got %+v", roundTripped)
	}
	if !roundTripped.Period.Equal(model.Period) {
		t.Errorf("the hand-written period attribute should round-trip, got %v", roundTripped.Period)
	}
}

// The API 422s ("Use exactly one of filter_expression or filter_expression_json")
// if both keys are present, and treats an explicit null as present — so this
// resource must never emit filter_expression under any circumstances.
func TestBudgetCreatePayloadNeverEmitsFilterExpression(t *testing.T) {
	plan := fixedRangeBudget(t)
	plan.FilterExpressionJson = types.StringValue(`{"operator":"and","filters":[]}`)

	payload, diags := buildBudgetCreate(context.Background(), &plan)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	body := marshal(t, payload)

	if _, present := body["filter_expression"]; present {
		t.Errorf("create payload must never carry filter_expression, got %v", body["filter_expression"])
	}
	if body["filter_expression_json"] != `{"operator":"and","filters":[]}` {
		t.Errorf("filter_expression_json should carry the configured filter, got %v", body["filter_expression_json"])
	}
}

// Only schedule_type and start_date belong to every period variant; sending a
// fixed_range period with a recurring or custom field would be rejected as
// belonging to a different schedule_type.
func TestBudgetCreatePayloadOmitsFieldsOutsideVariant(t *testing.T) {
	plan := fixedRangeBudget(t)

	payload, diags := buildBudgetCreate(context.Background(), &plan)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	body := marshal(t, payload)

	period, ok := body["period"].(map[string]any)
	if !ok {
		t.Fatalf("period should be an object, got %v", body["period"])
	}
	if period["schedule_type"] != "fixed_range" || period["end_date"] != "2026-03-31" {
		t.Errorf("fixed_range's own fields should be sent, got %v", period)
	}
	for _, field := range []string{"repeat_every", "repeat_unit", "expires_on", "expires_after_occurrences"} {
		if _, present := period[field]; present {
			t.Errorf("%s does not belong to fixed_range and should be omitted, got %v", field, period[field])
		}
	}
}

func TestBudgetUpdatePayloadOmitsUnchangedFields(t *testing.T) {
	state := fixedRangeBudget(t)
	plan := fixedRangeBudget(t)
	plan.Name = types.StringValue("Q1 Marketing (Renamed)")

	payload, diags := buildBudgetUpdate(context.Background(), &plan, &state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	body := marshal(t, payload)

	if body["name"] != "Q1 Marketing (Renamed)" {
		t.Errorf("the changed name should be sent, got %v", body["name"])
	}
	for _, field := range []string{"amount", "period", "started_at", "team_id", "filter_expression_json"} {
		if _, present := body[field]; present {
			t.Errorf("%s did not change and should have been omitted, got %v", field, body[field])
		}
	}
}

// BudgetPatch 422s on an explicit null for these four, so a nil pointer in the
// payload has to mean "unchanged" and never "clear this."
func TestBudgetUpdatePayloadNeverSendsNullForNonNullableFields(t *testing.T) {
	state := fixedRangeBudget(t)
	plan := fixedRangeBudget(t)
	plan.Name = types.StringValue("Q1 Marketing (Renamed)")

	payload, diags := buildBudgetUpdate(context.Background(), &plan, &state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	body := marshal(t, payload)

	for field, value := range body {
		if value == nil {
			t.Errorf("%s was sent as null, which BudgetPatch rejects for name, amount, period and started_at", field)
		}
	}
}

// team_id and filter_expression_json are the only two fields the API lets a
// caller clear, and the only way to say so is an explicit null.
func TestBudgetUpdatePayloadClearsTeamAndFilterWithExplicitNull(t *testing.T) {
	state := fixedRangeBudget(t)
	state.TeamId = types.StringValue("finance")
	state.FilterExpressionJson = types.StringValue(`{"operator":"and","filters":[]}`)
	plan := fixedRangeBudget(t)
	plan.TeamId = types.StringNull()
	plan.FilterExpressionJson = types.StringNull()

	payload, diags := buildBudgetUpdate(context.Background(), &plan, &state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	body := marshal(t, payload)

	for _, field := range []string{"team_id", "filter_expression_json"} {
		value, present := body[field]
		if !present {
			t.Fatalf("clearing %s has to reach the API as an explicit null, but the field was omitted", field)
		}
		if value != nil {
			t.Errorf("%s should be null, got %v", field, value)
		}
	}
	for _, field := range []string{"name", "amount", "period", "started_at"} {
		if _, present := body[field]; present {
			t.Errorf("%s did not change and should have been omitted, got %v", field, body[field])
		}
	}
}

func TestBudgetUpdatePayloadSendsPeriodOnlyWhenChanged(t *testing.T) {
	state := fixedRangeBudget(t)
	plan := fixedRangeBudget(t)

	payload, diags := buildBudgetUpdate(context.Background(), &plan, &state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if payload.Period != nil {
		t.Errorf("an unchanged period should be omitted, got %v", payload.Period)
	}

	plan.Period = weeklyPeriod(t, "2026-01-01", types.StringNull())
	payload, diags = buildBudgetUpdate(context.Background(), &plan, &state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	body := marshal(t, payload)
	period, ok := body["period"].(map[string]any)
	if !ok {
		t.Fatalf("a changed period should be sent as an object, got %v", body["period"])
	}
	if period["schedule_type"] != "weekly" {
		t.Errorf("the new variant should be sent, got %v", period)
	}
}

// Terraform parses a configured number at far higher precision than the
// response's float64 carries, so preserveEquivalentNumber has to keep the
// configured spelling rather than rebuilding one from the response.
func TestBudgetApplyResponsePreservesAmountPrecision(t *testing.T) {
	source := fixedRangeBudget(t)
	source.Amount = types.NumberValue(big.NewFloat(0.1))
	model := fixedRangeBudget(t)
	model.Amount = types.NumberValue(big.NewFloat(0.1))

	amount := 0.1
	response := &budgetResponse{
		Id:                 "2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01",
		Etag:               `"def456"`,
		Name:               "Q1 Marketing",
		Amount:             amount,
		StartedAt:          "2026-01-01T00:00:00Z",
		CurrentPeriodStart: "2026-01-01",
		CurrentPeriodEnd:   "2026-03-31",
		CreatedBy:          "someone@acme.com",
		CreateTime:         "2026-01-01T00:00:00Z",
		UpdateTime:         "2026-01-01T00:00:00Z",
		Period: budgetSchedule{
			ScheduleType: "fixed_range",
			StartDate:    "2026-01-01",
			EndDate:      strPtr("2026-03-31"),
		},
	}

	if diags := applyBudgetResponse(context.Background(), &model, &source, response); diags.HasError() {
		t.Fatalf("applying the response: %v", diags)
	}
	if !model.Amount.Equal(source.Amount) {
		t.Errorf("the configured spelling should be kept, got %v", model.Amount)
	}
}

// A bare configured date comes back with a time and zone attached, which
// should not read as drift.
func TestBudgetApplyResponsePreservesStartedAtSpelling(t *testing.T) {
	source := fixedRangeBudget(t)
	source.StartedAt = types.StringValue("2026-01-01")
	model := fixedRangeBudget(t)
	model.StartedAt = types.StringValue("2026-01-01")

	response := &budgetResponse{
		Id:                 "2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01",
		Etag:               `"def456"`,
		Name:               "Q1 Marketing",
		Amount:             1000,
		StartedAt:          "2026-01-01T00:00:00Z",
		CurrentPeriodStart: "2026-01-01",
		CurrentPeriodEnd:   "2026-03-31",
		CreatedBy:          "someone@acme.com",
		CreateTime:         "2026-01-01T00:00:00Z",
		UpdateTime:         "2026-01-01T00:00:00Z",
		Period: budgetSchedule{
			ScheduleType: "fixed_range",
			StartDate:    "2026-01-01",
			EndDate:      strPtr("2026-03-31"),
		},
	}

	if diags := applyBudgetResponse(context.Background(), &model, &source, response); diags.HasError() {
		t.Fatalf("applying the response: %v", diags)
	}
	if model.StartedAt.ValueString() != "2026-01-01" {
		t.Errorf("the configured spelling should be kept, got %v", model.StartedAt)
	}
}

// The API serializes started_at from a stored timestamp, so it carries
// fractional seconds whenever the underlying value has them. The same instant
// spelled with and without them should not read as drift.
func TestBudgetApplyResponsePreservesStartedAtAcrossFractionalSeconds(t *testing.T) {
	source := fixedRangeBudget(t)
	source.StartedAt = types.StringValue("2026-01-01T00:00:00Z")
	model := fixedRangeBudget(t)
	model.StartedAt = types.StringValue("2026-01-01T00:00:00Z")

	response := &budgetResponse{
		Id:                 "2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01",
		Etag:               `"def456"`,
		Name:               "Q1 Marketing",
		Amount:             1000,
		StartedAt:          "2026-01-01T00:00:00.000000Z",
		CurrentPeriodStart: "2026-01-01",
		CurrentPeriodEnd:   "2026-03-31",
		CreatedBy:          "someone@acme.com",
		CreateTime:         "2026-01-01T00:00:00Z",
		UpdateTime:         "2026-01-01T00:00:00Z",
		Period: budgetSchedule{
			ScheduleType: "fixed_range",
			StartDate:    "2026-01-01",
			EndDate:      strPtr("2026-03-31"),
		},
	}

	if diags := applyBudgetResponse(context.Background(), &model, &source, response); diags.HasError() {
		t.Fatalf("applying the response: %v", diags)
	}
	if model.StartedAt.ValueString() != "2026-01-01T00:00:00Z" {
		t.Errorf("the configured spelling should be kept, got %v", model.StartedAt)
	}
}

// The API re-encodes filter_expression_json from the structured filter it
// stores, reordering leaf keys along the way (written field, operator, value;
// returned field, value, operator). That reordering should not read as drift.
func TestBudgetApplyResponsePreservesFilterKeyOrder(t *testing.T) {
	configured := `{"operator":"and","filters":[{"field":"warehouse_name","operator":"in","values":["SELECT_BACKEND"]}]}`
	returned := `{"operator":"and","filters":[{"field":"warehouse_name","values":["SELECT_BACKEND"],"operator":"in"}]}`

	source := fixedRangeBudget(t)
	source.FilterExpressionJson = types.StringValue(configured)
	model := fixedRangeBudget(t)
	model.FilterExpressionJson = types.StringValue(configured)

	response := &budgetResponse{
		Id:                   "2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01",
		Etag:                 `"def456"`,
		Name:                 "Q1 Marketing",
		Amount:               1000,
		StartedAt:            "2026-01-01T00:00:00Z",
		FilterExpressionJson: &returned,
		CurrentPeriodStart:   "2026-01-01",
		CurrentPeriodEnd:     "2026-03-31",
		CreatedBy:            "someone@acme.com",
		CreateTime:           "2026-01-01T00:00:00Z",
		UpdateTime:           "2026-01-01T00:00:00Z",
		Period: budgetSchedule{
			ScheduleType: "fixed_range",
			StartDate:    "2026-01-01",
			EndDate:      strPtr("2026-03-31"),
		},
	}

	if diags := applyBudgetResponse(context.Background(), &model, &source, response); diags.HasError() {
		t.Fatalf("applying the response: %v", diags)
	}
	if model.FilterExpressionJson.ValueString() != configured {
		t.Errorf("the configured spelling should be kept despite the reordering, got %v", model.FilterExpressionJson)
	}
}

// A field outside the response's own variant should come back null rather than
// hold on to a value left over from a previous plan.
func TestBudgetApplyResponseNullsFieldsOutsideVariant(t *testing.T) {
	source := fixedRangeBudget(t)
	source.Period = customPeriod(t, "2026-01-01", 2, "weeks")
	model := fixedRangeBudget(t)
	model.Period = customPeriod(t, "2026-01-01", 2, "weeks")

	response := &budgetResponse{
		Id:                 "2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01",
		Etag:               `"def456"`,
		Name:               "Q1 Marketing",
		Amount:             1000,
		StartedAt:          "2026-01-01T00:00:00Z",
		CurrentPeriodStart: "2026-01-01",
		CurrentPeriodEnd:   "2026-01-15",
		CreatedBy:          "someone@acme.com",
		CreateTime:         "2026-01-01T00:00:00Z",
		UpdateTime:         "2026-01-01T00:00:00Z",
		Period: budgetSchedule{
			ScheduleType: "weekly",
			StartDate:    "2026-01-01",
		},
	}

	if diags := applyBudgetResponse(context.Background(), &model, &source, response); diags.HasError() {
		t.Fatalf("applying the response: %v", diags)
	}

	var period budgetPeriodModel
	if diags := model.Period.As(context.Background(), &period, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("reading back period: %v", diags)
	}
	if period.ScheduleType.ValueString() != "weekly" {
		t.Errorf("the new variant should be recorded, got %v", period.ScheduleType)
	}
	if !period.RepeatEvery.IsNull() || !period.RepeatUnit.IsNull() {
		t.Errorf("custom's fields should be cleared once the variant changes, got repeat_every=%v repeat_unit=%v", period.RepeatEvery, period.RepeatUnit)
	}
}

// status is fully computed by the API's own daily refresh; the resource has
// nothing of its own to preserve there.
func TestBudgetApplyResponseTakesStatusFromResponse(t *testing.T) {
	source := fixedRangeBudget(t)
	source.Status = types.StringNull()
	model := fixedRangeBudget(t)
	model.Status = types.StringNull()

	status := "at_risk"
	response := &budgetResponse{
		Id:                 "2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01",
		Etag:               `"def456"`,
		Name:               "Q1 Marketing",
		Amount:             1000,
		Status:             &status,
		StartedAt:          "2026-01-01T00:00:00Z",
		CurrentPeriodStart: "2026-01-01",
		CurrentPeriodEnd:   "2026-03-31",
		CreatedBy:          "someone@acme.com",
		CreateTime:         "2026-01-01T00:00:00Z",
		UpdateTime:         "2026-01-01T00:00:00Z",
		Period: budgetSchedule{
			ScheduleType: "fixed_range",
			StartDate:    "2026-01-01",
			EndDate:      strPtr("2026-03-31"),
		},
	}

	if diags := applyBudgetResponse(context.Background(), &model, &source, response); diags.HasError() {
		t.Fatalf("applying the response: %v", diags)
	}
	if model.Status.ValueString() != "at_risk" {
		t.Errorf("status should come from the response, got %v", model.Status)
	}
}

func TestValidateBudgetPeriod(t *testing.T) {
	tests := []struct {
		name    string
		period  types.Object
		wantErr string
	}{
		{
			name:   "fixed_range with end_date is valid",
			period: fixedRangePeriod(t, "2026-01-01", "2026-03-31"),
		},
		{
			name:    "fixed_range without end_date is missing a required field",
			period:  weeklyPeriodWithScheduleType(t, "fixed_range", "2026-01-01"),
			wantErr: "period.end_date is required",
		},
		{
			name:   "weekly with no expiry is valid",
			period: weeklyPeriod(t, "2026-01-01", types.StringNull()),
		},
		{
			name:   "weekly with expires_on is valid",
			period: weeklyPeriod(t, "2026-01-01", types.StringValue("2026-06-01")),
		},
		{
			name:   "custom with repeat_every and repeat_unit is valid",
			period: customPeriod(t, "2026-01-01", 2, "weeks"),
		},
		{
			name:    "custom without repeat_every or repeat_unit is missing required fields",
			period:  weeklyPeriodWithScheduleType(t, "custom", "2026-01-01"),
			wantErr: "period.repeat_every, period.repeat_unit is required",
		},
		{
			name:    "fixed_range with repeat_every is not valid for its variant",
			period:  budgetPeriod(t, budgetPeriodModel{ScheduleType: types.StringValue("fixed_range"), StartDate: types.StringValue("2026-01-01"), EndDate: types.StringValue("2026-03-31"), RepeatEvery: types.Int64Value(2), RepeatUnit: types.StringNull(), ExpiresOn: types.StringNull(), ExpiresAfterOccurrences: types.Int64Null()}),
			wantErr: "period.repeat_every is not valid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := validateBudgetPeriod(context.Background(), tt.period)
			if tt.wantErr == "" {
				if diags.HasError() {
					t.Errorf("unexpected diagnostics: %v", diags)
				}
				return
			}
			if !diags.HasError() {
				t.Fatalf("expected an error containing %q, got none", tt.wantErr)
			}
			found := false
			for _, d := range diags.Errors() {
				if strings.Contains(d.Detail(), tt.wantErr) {
					found = true
				}
			}
			if !found {
				t.Errorf("expected an error containing %q, got %v", tt.wantErr, diags)
			}
		})
	}
}

// weeklyPeriodWithScheduleType builds a period object with only schedule_type
// and start_date set, for exercising a schedule_type's required-field checks
// against a period that satisfies none of them.
func weeklyPeriodWithScheduleType(t *testing.T, scheduleType, startDate string) types.Object {
	return budgetPeriod(t, budgetPeriodModel{
		ScheduleType:            types.StringValue(scheduleType),
		StartDate:               types.StringValue(startDate),
		EndDate:                 types.StringNull(),
		RepeatEvery:             types.Int64Null(),
		RepeatUnit:              types.StringNull(),
		ExpiresOn:               types.StringNull(),
		ExpiresAfterOccurrences: types.Int64Null(),
	})
}

// The ETag contract and the API key's scopes are the two failures a user is
// least likely to diagnose unaided.
func TestBudgetPreconditionAndScopeDiagnostics(t *testing.T) {
	stale := budgetAPIDiagnostic("update the budget",
		newAPIError(412, `{"detail":"The If-Match header does not match the resource's current ETag.","code":"precondition_failed"}`))
	if !strings.Contains(stale.Detail(), "-refresh-only") {
		t.Errorf("a stale ETag should tell the user how to recover, got: %s", stale.Detail())
	}

	missing := budgetAPIDiagnostic("delete the budget",
		newAPIError(428, `{"detail":"This is a configurable resource; If-Match is required for writes.","code":"precondition_required"}`))
	if !strings.Contains(missing.Detail(), "-refresh-only") {
		t.Errorf("a missing ETag should tell the user how to recover, got: %s", missing.Detail())
	}

	forbidden := budgetAPIDiagnostic("add the budget",
		newAPIError(403, `{"detail":"This caller lacks the budgets:write scope.","code":"forbidden"}`))
	if !strings.Contains(forbidden.Detail(), "budgets:write") {
		t.Errorf("a scope failure should name the scopes needed, got: %s", forbidden.Detail())
	}
}
