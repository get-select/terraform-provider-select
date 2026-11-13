// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"terraform-provider-select/internal/provider/resource_monitor"

	"github.com/hashicorp/terraform-plugin-framework-validators/float64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// monitorModel embeds the generated struct and adds the five attributes that
// the generator cannot produce. See budgetModel for why an untagged embed is
// enough for plan.Get and state.Set.
type monitorModel struct {
	resource_monitor.MonitorModel
	DestinationIds              types.Set    `tfsdk:"destination_ids"`
	PartitionColumns            types.List   `tfsdk:"partition_columns"`
	NotificationFrequencyConfig types.Object `tfsdk:"notification_frequency_config"`
	TriggerConfig               types.Object `tfsdk:"trigger_config"`
	MessageConfigJson           types.String `tfsdk:"message_config_json"`
}

// monitorTriggerModel is the shape of trigger_config.
type monitorTriggerModel struct {
	Type               types.String  `tfsdk:"type"`
	SkipIfEmpty        types.Bool    `tfsdk:"skip_if_empty"`
	QueryJson          types.String  `tfsdk:"query_json"`
	Operator           types.String  `tfsdk:"operator"`
	Threshold          types.Float64 `tfsdk:"threshold"`
	Trigger            types.String  `tfsdk:"trigger"`
	Sensitivity        types.Float64 `tfsdk:"sensitivity"`
	TriggerOnUpperBand types.Bool    `tfsdk:"trigger_on_upper_band"`
	TriggerOnLowerBand types.Bool    `tfsdk:"trigger_on_lower_band"`
	MinimumThreshold   types.Float64 `tfsdk:"minimum_threshold"`
	UpperPercentile    types.Float64 `tfsdk:"upper_percentile"`
	LowerPercentile    types.Float64 `tfsdk:"lower_percentile"`
	Deviations         types.Float64 `tfsdk:"deviations"`
	Thresholds         types.List    `tfsdk:"thresholds"`
}

// monitorBudgetThresholdModel is the shape of one trigger_config.thresholds
// element.
type monitorBudgetThresholdModel struct {
	Metric types.String  `tfsdk:"metric"`
	Unit   types.String  `tfsdk:"unit"`
	Value  types.Float64 `tfsdk:"value"`
}

// monitorFrequencyModel is the shape of notification_frequency_config.
type monitorFrequencyModel struct {
	FrequencyType     types.String `tfsdk:"frequency_type"`
	FrequencyInterval types.Int64  `tfsdk:"frequency_interval"`
}

func NewMonitorResource() resource.Resource {
	return &v2Resource[monitorModel, monitorResponse]{
		typeNameSuffix:     "_monitor",
		schema:             monitorResourceSchema,
		errors:             monitorErrors,
		collectionEndpoint: func(*monitorModel) string { return monitorsEndpoint },
		itemEndpoint: func(m *monitorModel) string {
			return monitorEndpoint(m.Id.ValueString())
		},
		identity: func(m *monitorModel) v2Identity {
			return v2Identity{Id: m.Id, Etag: m.Etag}
		},
		createPayload: v2FalliblePayload(buildMonitorCreate),
		// PUT replaces the full monitor with the create body. Every
		// MonitorCreate field is in the schema, so PUT cannot reset a field
		// that Terraform does not manage, and a removed optional attribute
		// reaches the API as the null that clears it.
		updatePayload: func(ctx context.Context, plan, _ *monitorModel) (any, diag.Diagnostics) {
			return buildMonitorCreate(ctx, plan)
		},
		updateMethod:   http.MethodPut,
		applyResponse:  applyMonitorResponse,
		validateConfig: validateMonitorConfig,
	}
}

// monitorResourceSchema takes the generated schema and adds the attributes
// that the generator cannot produce. MonitorResourceSchema builds a new
// attribute map on each call, so this changes no shared value.
func monitorResourceSchema(ctx context.Context) schema.Schema {
	s := resource_monitor.MonitorResourceSchema(ctx)
	s.Attributes["destination_ids"] = schema.SetAttribute{
		Required:    true,
		ElementType: types.StringType,
		Description: "The ids of the alert destinations the monitor sends to. Use the `id` of a " +
			"`select_slack_alert_destination`, `select_email_alert_destination` or " +
			"`select_ms_teams_alert_destination`.",
	}
	s.Attributes["partition_columns"] = schema.ListAttribute{
		Optional:    true,
		ElementType: types.StringType,
		Description: "The titles of the query columns that split the monitor into one alert per " +
			"value. Null sends no partition config.",
		Validators: []validator.List{
			listvalidator.SizeAtLeast(1),
		},
	}
	s.Attributes["notification_frequency_config"] = monitorFrequencyAttribute()
	s.Attributes["trigger_config"] = monitorTriggerAttribute()
	s.Attributes["message_config_json"] = schema.StringAttribute{
		Required: true,
		Description: "The message the monitor sends, as a JSON-encoded `MonitorMessageConfig`: " +
			"`{\"title\": ..., \"sections\": [...]}`. Use `jsonencode()`. A typical message has three " +
			"sections, in this order: `paragraph`, `chart` and `table`. SELECT adds defaults and " +
			"generated text, such as a chart title. These additions do not show as a change. " +
			"A change to a key that the configuration sets shows as a change.",
	}
	return s
}

func monitorFrequencyAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Required:    true,
		Description: "How often the monitor can send a notification.",
		Attributes: map[string]schema.Attribute{
			"frequency_type": schema.StringAttribute{
				Required: true,
				Description: "`on_trigger` sends each time the trigger fires. `on_status_change` sends " +
					"when the status changes. `limit_x_hours`, `limit_x_days`, `limit_x_weeks` and " +
					"`limit_x_months` send at most once per `frequency_interval` units.",
				Validators: []validator.String{
					stringvalidator.OneOf(monitorFrequencyTypes()...),
				},
			},
			"frequency_interval": schema.Int64Attribute{
				Optional: true,
				Description: "The number of units between notifications. Required for, and only " +
					"valid with, a `limit_x_*` frequency_type.",
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
		},
	}
}

func monitorTriggerAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Required: true,
		Description: "When the monitor sends. `type` selects the variant. The fields that each " +
			"type requires or accepts: `digest` requires skip_if_empty. `condition` requires " +
			"query_json, operator, threshold and trigger. `anomaly_prophet` requires query_json " +
			"and sensitivity, and accepts trigger_on_upper_band, trigger_on_lower_band and " +
			"minimum_threshold. `anomaly_percentile` requires query_json, and accepts " +
			"upper_percentile, lower_percentile, deviations and minimum_threshold. " +
			"`budget_threshold` requires thresholds and accepts query_json.",
		Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{
				Required:    true,
				Description: "The trigger variant.",
				Validators: []validator.String{
					stringvalidator.OneOf("digest", "condition", "anomaly_prophet", "anomaly_percentile", "budget_threshold"),
				},
			},
			"skip_if_empty": schema.BoolAttribute{
				Optional:    true,
				Description: "For `digest`: true skips a digest that has no data.",
			},
			"query_json": schema.StringAttribute{
				Optional: true,
				Description: "The semantic-layer query the trigger evaluates, as a JSON-encoded " +
					"`MonitorQuery`: `{\"query_class\": ..., \"query_object\": {...}, \"period\": {...}}`. " +
					"Use `jsonencode()`. `GET /v2/monitors/query-classes` lists the query classes. " +
					"SELECT adds defaults, such as `period.date_column`. These additions do not show as a change.",
			},
			"operator": schema.StringAttribute{
				Optional:    true,
				Description: "For `condition`: how the query result is compared with threshold.",
				Validators: []validator.String{
					stringvalidator.OneOf(">", ">=", "<", "<=", "==", "!="),
				},
			},
			"threshold": schema.Float64Attribute{
				Optional:    true,
				Description: "For `condition`: the value the query result is compared with.",
			},
			"trigger": schema.StringAttribute{
				Optional:    true,
				Description: "For `condition`: when the condition causes a send.",
				Validators: []validator.String{
					stringvalidator.OneOf("always", "on_condition_met", "on_status_change"),
				},
			},
			"sensitivity": schema.Float64Attribute{
				Optional:    true,
				Description: "For `anomaly_prophet`: the width of the expected band, from 0 to 50.",
				Validators: []validator.Float64{
					float64validator.Between(0, 50),
				},
			},
			"trigger_on_upper_band": schema.BoolAttribute{
				Optional:    true,
				Description: "For `anomaly_prophet`: send when the value is above the band. SELECT's default is true.",
			},
			"trigger_on_lower_band": schema.BoolAttribute{
				Optional:    true,
				Description: "For `anomaly_prophet`: send when the value is below the band. SELECT's default is true.",
			},
			"minimum_threshold": schema.Float64Attribute{
				Optional:    true,
				Description: "For `anomaly_prophet` and `anomaly_percentile`: ignore an anomaly below this value.",
			},
			"upper_percentile": schema.Float64Attribute{
				Optional:    true,
				Description: "For `anomaly_percentile`: the upper percentile of the band, from 0 to 100.",
				Validators: []validator.Float64{
					float64validator.Between(0, 100),
				},
			},
			"lower_percentile": schema.Float64Attribute{
				Optional:    true,
				Description: "For `anomaly_percentile`: the lower percentile of the band, from 0 to 100.",
				Validators: []validator.Float64{
					float64validator.Between(0, 100),
				},
			},
			"deviations": schema.Float64Attribute{
				Optional:    true,
				Description: "For `anomaly_percentile`: the number of deviations past the band, from 0 to 10.",
				Validators: []validator.Float64{
					float64validator.Between(0, 10),
				},
			},
			"thresholds": schema.ListNestedAttribute{
				Optional:    true,
				Description: "For `budget_threshold`: the spend levels that cause a send.",
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"metric": schema.StringAttribute{
							Required:    true,
							Description: "`actual_spend` or `forecast_spend`.",
							Validators: []validator.String{
								stringvalidator.OneOf("actual_spend", "forecast_spend"),
							},
						},
						"unit": schema.StringAttribute{
							Required:    true,
							Description: "`percent` of the budget amount, or an `amount` of spend.",
							Validators: []validator.String{
								stringvalidator.OneOf("percent", "amount"),
							},
						},
						"value": schema.Float64Attribute{
							Required:    true,
							Description: "The percent or the amount.",
						},
					},
				},
			},
		},
	}
}

// monitorTriggerType and the two functions below get each object type from
// its attribute, so the values that applyMonitorResponse builds cannot
// differ from what the schema declares.
func monitorTriggerType() types.ObjectType {
	return monitorTriggerAttribute().GetType().(types.ObjectType)
}

func monitorBudgetThresholdType() types.ObjectType {
	return monitorTriggerType().AttrTypes["thresholds"].(types.ListType).ElemType.(types.ObjectType)
}

func monitorFrequencyType() types.ObjectType {
	return monitorFrequencyAttribute().GetType().(types.ObjectType)
}

var monitorLimitFrequencyTypes = []string{"limit_x_hours", "limit_x_days", "limit_x_weeks", "limit_x_months"}

func monitorFrequencyTypes() []string {
	return append([]string{"on_trigger", "on_status_change"}, monitorLimitFrequencyTypes...)
}

// monitorTriggerFields records the trigger_config fields that each type
// requires and allows.
var monitorTriggerFields = map[string]variantFieldRules{
	"digest": {
		required: []string{"skip_if_empty"},
		allowed:  []string{"skip_if_empty"},
	},
	"condition": {
		required: []string{"query_json", "operator", "threshold", "trigger"},
		allowed:  []string{"query_json", "operator", "threshold", "trigger"},
	},
	"anomaly_prophet": {
		required: []string{"query_json", "sensitivity"},
		allowed:  []string{"query_json", "sensitivity", "trigger_on_upper_band", "trigger_on_lower_band", "minimum_threshold"},
	},
	"anomaly_percentile": {
		required: []string{"query_json"},
		allowed:  []string{"query_json", "upper_percentile", "lower_percentile", "deviations", "minimum_threshold"},
	},
	"budget_threshold": {
		required: []string{"thresholds"},
		allowed:  []string{"thresholds", "query_json"},
	},
}

// validateMonitorConfig rejects at plan time the field combinations that the
// API rejects on apply.
func validateMonitorConfig(ctx context.Context, config *monitorModel) diag.Diagnostics {
	var diags diag.Diagnostics

	diags.Append(validateMonitorTrigger(ctx, config.TriggerConfig)...)
	diags.Append(validateMonitorFrequency(ctx, config.NotificationFrequencyConfig)...)
	diags.Append(validateJSONAttribute(path.Root("message_config_json"), "Invalid Monitor Message", config.MessageConfigJson)...)

	if known(config.Schedule) {
		cronSet := !config.Cron.IsNull()
		switch {
		case config.Schedule.ValueString() == "cron" && !cronSet:
			diags.AddAttributeError(path.Root("cron"), "Missing Monitor Cron",
				`cron is required when schedule is "cron".`)
		case config.Schedule.ValueString() != "cron" && cronSet:
			diags.AddAttributeError(path.Root("cron"), "Unused Monitor Cron",
				`cron is valid only when schedule is "cron".`)
		}
		if config.Schedule.ValueString() == "cron" && known(config.DataSource) && config.DataSource.ValueString() != "custom" {
			diags.AddAttributeError(path.Root("schedule"), "Invalid Monitor Schedule",
				`schedule "cron" is valid only when data_source is "custom".`)
		}
	}

	if known(config.DataSource) {
		accountSet := !config.SnowflakeAccountId.IsNull()
		switch {
		case config.DataSource.ValueString() == "custom" && !accountSet:
			diags.AddAttributeError(path.Root("snowflake_account_id"), "Missing Monitor Snowflake Account",
				`snowflake_account_id is required when data_source is "custom".`)
		case config.DataSource.ValueString() != "custom" && accountSet:
			diags.AddAttributeError(path.Root("snowflake_account_id"), "Unused Monitor Snowflake Account",
				`snowflake_account_id is valid only when data_source is "custom".`)
		}
	}

	return diags
}

// known reports whether a value is neither null nor unknown.
func known(value attr.Value) bool {
	return !value.IsNull() && !value.IsUnknown()
}

// validateJSONAttribute reports a known string value that is not valid JSON.
func validateJSONAttribute(attribute path.Path, summary string, value types.String) diag.Diagnostics {
	var diags diag.Diagnostics
	if known(value) && !json.Valid([]byte(value.ValueString())) {
		diags.AddAttributeError(attribute, summary,
			attribute.String()+" must be a JSON-encoded document. Use jsonencode().")
	}
	return diags
}

func validateMonitorTrigger(ctx context.Context, trigger types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	if !known(trigger) {
		return diags
	}

	var model monitorTriggerModel
	diags.Append(trigger.As(ctx, &model, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return diags
	}

	diags.Append(validateJSONAttribute(path.Root("trigger_config").AtName("query_json"), "Invalid Monitor Query", model.QueryJson)...)

	if !known(model.Type) {
		return diags
	}
	rules, ok := monitorTriggerFields[model.Type.ValueString()]
	if !ok {
		// The schema's enum validator rejects any other type.
		return diags
	}

	// An unknown value counts as set: it will have a value at apply.
	present := map[string]bool{
		"skip_if_empty":         !model.SkipIfEmpty.IsNull(),
		"query_json":            !model.QueryJson.IsNull(),
		"operator":              !model.Operator.IsNull(),
		"threshold":             !model.Threshold.IsNull(),
		"trigger":               !model.Trigger.IsNull(),
		"sensitivity":           !model.Sensitivity.IsNull(),
		"trigger_on_upper_band": !model.TriggerOnUpperBand.IsNull(),
		"trigger_on_lower_band": !model.TriggerOnLowerBand.IsNull(),
		"minimum_threshold":     !model.MinimumThreshold.IsNull(),
		"upper_percentile":      !model.UpperPercentile.IsNull(),
		"lower_percentile":      !model.LowerPercentile.IsNull(),
		"deviations":            !model.Deviations.IsNull(),
		"thresholds":            !model.Thresholds.IsNull(),
	}
	diags.Append(rules.check(present, "Monitor Trigger", "trigger_config", "type", model.Type.ValueString())...)
	return diags
}

func validateMonitorFrequency(ctx context.Context, frequency types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	if !known(frequency) {
		return diags
	}

	var model monitorFrequencyModel
	diags.Append(frequency.As(ctx, &model, basetypes.ObjectAsOptions{})...)
	if diags.HasError() || !known(model.FrequencyType) {
		return diags
	}

	rules := variantFieldRules{}
	frequencyType := model.FrequencyType.ValueString()
	if slices.Contains(monitorLimitFrequencyTypes, frequencyType) {
		rules = variantFieldRules{
			required: []string{"frequency_interval"},
			allowed:  []string{"frequency_interval"},
		}
	}
	present := map[string]bool{"frequency_interval": !model.FrequencyInterval.IsNull()}
	diags.Append(rules.check(present, "Monitor Notification Frequency", "notification_frequency_config", "frequency_type", frequencyType)...)
	return diags
}
