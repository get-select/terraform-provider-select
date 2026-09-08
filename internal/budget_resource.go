// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"terraform-provider-select/internal/provider/resource_budget"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// budgetModel embeds the generated struct rather than restating it, so a field
// the API adds lands in both the generated model and schema together and only
// period — which the generator cannot produce at all — needs to be hand-kept in
// sync. See generator_config.v2.yml for why period is ignored, and getStructTags
// in terraform-plugin-framework's internal/reflect package for why an anonymous,
// untagged embed is enough for plan.Get and state.Set to reach its fields: it
// recurses into embedded structs and flattens their tfsdk tags into the same
// map a flat struct would produce. A duplicate promoted tag is a hard framework
// error, so this is a free canary if upstream ever supports oneOf and the
// ignores entry above is dropped.
type budgetModel struct {
	resource_budget.BudgetModel
	Period types.Object `tfsdk:"period"`
}

// budgetResourceSchema takes the generated schema and injects the period
// attribute the generator could not produce. BudgetResourceSchema builds a
// fresh attribute map on every call, so mutating it here aliases nothing.
func budgetResourceSchema(ctx context.Context) schema.Schema {
	s := resource_budget.BudgetResourceSchema(ctx)
	s.Attributes["period"] = budgetPeriodAttribute()
	return s
}

// budgetPeriodAttribute is one flat object keyed by its own discriminator
// (schedule_type) rather than three variant blocks, which would have to
// restate schedule_type inside each block and add a tri-state exactly-one-of
// check per block instead of the single map validateBudgetPeriod already needs.
func budgetPeriodAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Required: true,
		Description: "The recurrence or date range the budget's threshold applies over. " +
			"schedule_type picks the variant, and which of the other fields are valid " +
			"depends on it: fixed_range needs end_date; weekly, monthly, quarterly and " +
			"yearly accept expires_on and expires_after_occurrences; custom needs " +
			"repeat_every and repeat_unit and accepts the same two expiry fields.",
		Attributes: map[string]schema.Attribute{
			"schedule_type": schema.StringAttribute{
				Required:    true,
				Description: "Which shape this period takes.",
				Validators: []validator.String{
					stringvalidator.OneOf("fixed_range", "weekly", "monthly", "quarterly", "yearly", "custom"),
				},
			},
			"start_date": schema.StringAttribute{
				Required:    true,
				Description: "The date this period's recurrence or range begins, as YYYY-MM-DD.",
			},
			"end_date": schema.StringAttribute{
				Optional:    true,
				Description: "The last date of the range. Required for, and only valid with, schedule_type = fixed_range.",
			},
			"repeat_every": schema.Int64Attribute{
				Optional:    true,
				Description: "How many repeat_units between occurrences. Required for, and only valid with, schedule_type = custom.",
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
			"repeat_unit": schema.StringAttribute{
				Optional:    true,
				Description: "The unit repeat_every counts. Required for, and only valid with, schedule_type = custom.",
				Validators: []validator.String{
					stringvalidator.OneOf("days", "weeks", "months", "quarters", "years"),
				},
			},
			"expires_on": schema.StringAttribute{
				Optional:    true,
				Description: "The date the recurrence stops. Valid only for a recurring or custom schedule_type.",
			},
			"expires_after_occurrences": schema.Int64Attribute{
				Optional:    true,
				Description: "The number of occurrences after which the recurrence stops. Valid only for a recurring or custom schedule_type.",
			},
		},
	}
}

// budgetPeriodType derives the period attribute's object type from the
// attribute itself, so budgetPeriodObject's rebuilt values cannot drift from
// what budgetResourceSchema actually declares.
func budgetPeriodType(ctx context.Context) types.ObjectType {
	return budgetPeriodAttribute().GetType().(types.ObjectType)
}

func NewBudgetResource() resource.Resource {
	return &v2Resource[budgetModel, budgetResponse]{
		typeNameSuffix:     "_budget",
		schema:             budgetResourceSchema,
		errors:             budgetErrors,
		specificDiagnostic: nil,
		collectionEndpoint: budgetsEndpoint,
		itemEndpoint:       budgetEndpoint,
		identity: func(m *budgetModel) v2Identity {
			return v2Identity{Id: m.Id, Etag: m.Etag}
		},
		createPayload:  v2FalliblePayload(buildBudgetCreate),
		updatePayload:  v2FalliblePatch(buildBudgetUpdate),
		applyResponse:  applyBudgetResponse,
		validateConfig: validateBudgetConfig,
	}
}

// scheduleFieldRules records, per schedule_type, which of period's optional
// fields are required and which are allowed at all — the same shape
// credentialFieldsByMethod uses for Snowflake's authentication methods.
type scheduleFieldRules struct {
	required []string
	allowed  []string
}

// recurringScheduleFields is shared by weekly, monthly, quarterly and yearly:
// none of the four requires anything beyond start_date, and all four accept
// the same pair of optional expiry fields.
var recurringScheduleFields = scheduleFieldRules{
	allowed: []string{"expires_on", "expires_after_occurrences"},
}

var budgetScheduleFields = map[string]scheduleFieldRules{
	"fixed_range": {
		required: []string{"end_date"},
		allowed:  []string{"end_date"},
	},
	"weekly":    recurringScheduleFields,
	"monthly":   recurringScheduleFields,
	"quarterly": recurringScheduleFields,
	"yearly":    recurringScheduleFields,
	"custom": {
		required: []string{"repeat_every", "repeat_unit"},
		allowed:  []string{"repeat_every", "repeat_unit", "expires_on", "expires_after_occurrences"},
	},
}

// validateBudgetConfig rejects at plan time the field combinations the API
// rejects on the way in, so a mistake costs a plan rather than a round trip.
func validateBudgetConfig(ctx context.Context, config *budgetModel) diag.Diagnostics {
	var diags diag.Diagnostics

	diags.Append(validateBudgetPeriod(ctx, config.Period)...)

	if expression := config.FilterExpressionJson; !expression.IsNull() && !expression.IsUnknown() {
		if !json.Valid([]byte(expression.ValueString())) {
			diags.AddError(
				"Invalid Budget Filter",
				"filter_expression_json must be a JSON-encoded filter.",
			)
		}
	}

	return diags
}

// validateBudgetPeriod mirrors the schedule classes in the API's own schemas:
// which fields belong to which schedule_type, reported the same way
// validateSnowflakeCredentials reports a credential mismatch — missing and
// not-valid-for-this-variant fields both named, sorted so the message does not
// depend on map iteration order.
func validateBudgetPeriod(ctx context.Context, period types.Object) diag.Diagnostics {
	var diags diag.Diagnostics

	if period.IsNull() || period.IsUnknown() {
		return diags
	}

	var model budgetPeriodModel
	diags.Append(period.As(ctx, &model, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return diags
	}

	if model.ScheduleType.IsUnknown() {
		// A value only known after apply cannot be checked here; the API still
		// enforces the same rules.
		return diags
	}

	scheduleType := model.ScheduleType.ValueString()
	rules, known := budgetScheduleFields[scheduleType]
	if !known {
		// The schema's own enum validator already rejects anything else.
		return diags
	}

	present := map[string]bool{
		"end_date":                  isSet(model.EndDate),
		"repeat_every":              !model.RepeatEvery.IsNull() && !model.RepeatEvery.IsUnknown(),
		"repeat_unit":               isSet(model.RepeatUnit),
		"expires_on":                isSet(model.ExpiresOn),
		"expires_after_occurrences": !model.ExpiresAfterOccurrences.IsNull() && !model.ExpiresAfterOccurrences.IsUnknown(),
	}

	var missing []string
	for _, field := range rules.required {
		if !present[field] {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		diags.AddError(
			"Incomplete Budget Period",
			fmt.Sprintf("period.%s is required for schedule_type %q.",
				strings.Join(missing, ", period."), scheduleType),
		)
	}

	allowed := map[string]bool{}
	for _, field := range rules.allowed {
		allowed[field] = true
	}
	var unexpected []string
	for field, set := range present {
		if set && !allowed[field] {
			unexpected = append(unexpected, field)
		}
	}
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		diags.AddError(
			"Unused Budget Period Fields",
			fmt.Sprintf("period.%s is not valid for schedule_type %q.",
				strings.Join(unexpected, ", period."), scheduleType),
		)
	}

	return diags
}
