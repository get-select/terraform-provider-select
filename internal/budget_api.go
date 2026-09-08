// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// Budgets live on the v2 API. See v2_api.go for the conventions every resource
// on that surface shares.
const budgetsEndpoint = "/v2/budgets"

func budgetEndpoint(id string) string {
	return fmt.Sprintf("%s/%s", budgetsEndpoint, id)
}

// budgetErrors words the failures every v2 resource can hit. Budgets have no
// resource-specific conflict: unlike a connection, nothing about a budget's
// fields collides with another budget's, so there is no specificDiagnostic for
// this resource.
var budgetErrors = v2ErrorFormat{
	Noun:       "Budget",
	Subject:    "the budget",
	Object:     "the budget",
	Plural:     "budgets",
	ReadScope:  "budgets:read",
	WriteScope: "budgets:write",
}

// budgetSchedule mirrors every period variant's fields at once, since the API's
// period is a oneOf discriminated on schedule_type that the generator cannot
// produce (see generator_config.v2.yml). Only schedule_type and start_date
// belong to every variant; the rest are omitted rather than sent as null for a
// variant that does not carry them, since null is not a member of any of them.
type budgetSchedule struct {
	ScheduleType            string  `json:"schedule_type"`
	StartDate               string  `json:"start_date"`
	EndDate                 *string `json:"end_date,omitempty"`
	RepeatEvery             *int64  `json:"repeat_every,omitempty"`
	RepeatUnit              *string `json:"repeat_unit,omitempty"`
	ExpiresOn               *string `json:"expires_on,omitempty"`
	ExpiresAfterOccurrences *int64  `json:"expires_after_occurrences,omitempty"`
}

// budgetPeriodModel is period's own shape, used to move between the
// hand-written types.Object attribute and budgetSchedule on the wire. ObjectValue's
// As/ObjectValueFrom recognize tfsdk tags the same way plan.Get and state.Set do,
// so this needs no manual attribute lookups.
type budgetPeriodModel struct {
	ScheduleType            types.String `tfsdk:"schedule_type"`
	StartDate               types.String `tfsdk:"start_date"`
	EndDate                 types.String `tfsdk:"end_date"`
	RepeatEvery             types.Int64  `tfsdk:"repeat_every"`
	RepeatUnit              types.String `tfsdk:"repeat_unit"`
	ExpiresOn               types.String `tfsdk:"expires_on"`
	ExpiresAfterOccurrences types.Int64  `tfsdk:"expires_after_occurrences"`
}

// schedulePayload converts the configured period into the shape the API wants.
// Fallible because decoding a types.Object can itself produce diagnostics.
func schedulePayload(ctx context.Context, period types.Object) (budgetSchedule, diag.Diagnostics) {
	var model budgetPeriodModel
	diags := period.As(ctx, &model, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return budgetSchedule{}, diags
	}

	return budgetSchedule{
		ScheduleType:            model.ScheduleType.ValueString(),
		StartDate:               model.StartDate.ValueString(),
		EndDate:                 stringPointer(model.EndDate),
		RepeatEvery:             int64Pointer(model.RepeatEvery),
		RepeatUnit:              stringPointer(model.RepeatUnit),
		ExpiresOn:               stringPointer(model.ExpiresOn),
		ExpiresAfterOccurrences: int64Pointer(model.ExpiresAfterOccurrences),
	}, diags
}

// budgetPeriodObject is schedulePayload's inverse, building the period
// attribute's value from what the API returned. A field outside the response's
// own variant comes back nil from budgetSchedule's pointers and therefore null
// here, rather than holding on to a stale value from a previous variant.
func budgetPeriodObject(ctx context.Context, schedule budgetSchedule) (types.Object, diag.Diagnostics) {
	model := budgetPeriodModel{
		ScheduleType:            types.StringValue(schedule.ScheduleType),
		StartDate:               types.StringValue(schedule.StartDate),
		EndDate:                 stringValue(schedule.EndDate),
		RepeatEvery:             int64Value(schedule.RepeatEvery),
		RepeatUnit:              stringValue(schedule.RepeatUnit),
		ExpiresOn:               stringValue(schedule.ExpiresOn),
		ExpiresAfterOccurrences: int64Value(schedule.ExpiresAfterOccurrences),
	}
	return types.ObjectValueFrom(ctx, budgetPeriodType(ctx).AttrTypes, model)
}

// budgetCreatePayload mirrors BudgetCreate. team_id, started_at and
// filter_expression_json are omitted rather than sent as null, which for a
// create means the same thing. There is deliberately no filter_expression
// field: the API 422s ("Use exactly one of filter_expression or
// filter_expression_json") if both keys are present, and an explicit null
// counts as present, so this resource must never emit the key at all.
type budgetCreatePayload struct {
	Name                 string         `json:"name"`
	Amount               float64        `json:"amount"`
	Period               budgetSchedule `json:"period"`
	TeamId               *string        `json:"team_id,omitempty"`
	StartedAt            *string        `json:"started_at,omitempty"`
	FilterExpressionJson *string        `json:"filter_expression_json,omitempty"`
}

// budgetUpdatePayload mirrors BudgetPatch, a JSON Merge Patch body where an
// omitted field is left unchanged.
//
// Every field is sent only when changed, the AWS/BigQuery convention rather
// than Snowflake's send-every-clearable-field: BudgetPatch 422s on an explicit
// null for name, amount, period and started_at, so for those a nil pointer has
// to mean "unchanged" rather than "clear this." team_id and
// filter_expression_json are the two fields the API does let a caller clear, so
// they use *nullableString for the three-state omit/null/value that a plain
// pointer cannot express.
//
// Amount is a *float64 rather than a plain float64 so a change to exactly 0
// still serializes: omitempty on a pointer field omits only a nil pointer, not
// whatever it points to, unlike a plain numeric zero value.
type budgetUpdatePayload struct {
	Name                 *string         `json:"name,omitempty"`
	Amount               *float64        `json:"amount,omitempty"`
	Period               *budgetSchedule `json:"period,omitempty"`
	StartedAt            *string         `json:"started_at,omitempty"`
	TeamId               *nullableString `json:"team_id,omitempty"`
	FilterExpressionJson *nullableString `json:"filter_expression_json,omitempty"`
}

// budgetResponse mirrors BudgetV2.
type budgetResponse struct {
	Id                   string         `json:"id"`
	Etag                 string         `json:"etag"`
	Name                 string         `json:"name"`
	Amount               float64        `json:"amount"`
	Status               *string        `json:"status"`
	TeamId               *string        `json:"team_id"`
	Period               budgetSchedule `json:"period"`
	StartedAt            string         `json:"started_at"`
	FilterExpressionJson *string        `json:"filter_expression_json"`
	CurrentPeriodStart   string         `json:"current_period_start"`
	CurrentPeriodEnd     string         `json:"current_period_end"`
	BudgetEndDate        *string        `json:"budget_end_date"`
	CreatedBy            string         `json:"created_by"`
	UpdatedBy            *string        `json:"updated_by"`
	CreateTime           string         `json:"create_time"`
	UpdateTime           string         `json:"update_time"`
}

func buildBudgetCreate(ctx context.Context, plan *budgetModel) (*budgetCreatePayload, diag.Diagnostics) {
	period, diags := schedulePayload(ctx, plan.Period)
	if diags.HasError() {
		return nil, diags
	}

	var amount float64
	if value := numberPointer(plan.Amount); value != nil {
		amount = *value
	}

	return &budgetCreatePayload{
		Name:                 plan.Name.ValueString(),
		Amount:               amount,
		Period:               period,
		TeamId:               stringPointer(plan.TeamId),
		StartedAt:            stringPointer(plan.StartedAt),
		FilterExpressionJson: stringPointer(plan.FilterExpressionJson),
	}, diags
}

// buildBudgetUpdate carries only the fields whose configured value differs from
// what state records. See budgetUpdatePayload.
func buildBudgetUpdate(ctx context.Context, plan, state *budgetModel) (*budgetUpdatePayload, diag.Diagnostics) {
	payload := &budgetUpdatePayload{
		Name:                 changedString(plan.Name, state.Name),
		Amount:               changedNumber(plan.Amount, state.Amount),
		StartedAt:            changedString(plan.StartedAt, state.StartedAt),
		TeamId:               clearedString(plan.TeamId, state.TeamId),
		FilterExpressionJson: clearedString(plan.FilterExpressionJson, state.FilterExpressionJson),
	}

	if !plan.Period.Equal(state.Period) {
		period, diags := schedulePayload(ctx, plan.Period)
		if diags.HasError() {
			return nil, diags
		}
		payload.Period = &period
	}

	return payload, nil
}

// applyBudgetResponse writes an API response onto the model. Three fields need
// drift suppression, or an apply that only touched something else would fail
// with "provider produced inconsistent result": amount, since Terraform's
// configured precision differs from a float64 round trip; started_at, since a
// bare date comes back with a time and zone attached; and
// filter_expression_json, since the API re-encodes it from the structured
// filter it stores and reorders leaf keys along the way. Everything else comes
// straight from the response — nothing here is carried from source, since this
// resource holds no write-only secret the API would otherwise blank.
func applyBudgetResponse(ctx context.Context, model, source *budgetModel, response *budgetResponse) diag.Diagnostics {
	var diags diag.Diagnostics

	model.Id = types.StringValue(response.Id)
	model.Etag = types.StringValue(response.Etag)
	model.Name = types.StringValue(response.Name)
	model.Amount = preserveEquivalentNumber(source.Amount, &response.Amount)
	model.Status = stringValue(response.Status)
	model.TeamId = stringValue(response.TeamId)
	model.StartedAt = preserveEquivalentTime(source.StartedAt, &response.StartedAt)
	model.FilterExpressionJson = preserveEquivalentJSON(source.FilterExpressionJson, response.FilterExpressionJson)
	model.CurrentPeriodStart = types.StringValue(response.CurrentPeriodStart)
	model.CurrentPeriodEnd = types.StringValue(response.CurrentPeriodEnd)
	model.BudgetEndDate = stringValue(response.BudgetEndDate)
	model.CreatedBy = types.StringValue(response.CreatedBy)
	model.UpdatedBy = stringValue(response.UpdatedBy)
	model.CreateTime = types.StringValue(response.CreateTime)
	model.UpdateTime = types.StringValue(response.UpdateTime)

	period, periodDiags := budgetPeriodObject(ctx, response.Period)
	diags.Append(periodDiags...)
	model.Period = period

	return diags
}

// budgetAPIDiagnostic turns an API failure into the most useful diagnostic
// available for it: an explanation of the ETag contract for a precondition
// failure, and the problem document's detail otherwise. Budgets have nothing
// beyond what every v2 resource already shares, so there is no specific
// diagnostic function to plug in here.
func budgetAPIDiagnostic(operation string, apiErr *apiError) diag.Diagnostic {
	return budgetErrors.diagnostic(operation, apiErr, nil)
}
