// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// Monitors live on the v2 API. See v2_api.go for the conventions every
// resource on that surface shares.
const monitorsEndpoint = "/v2/monitors"

func monitorEndpoint(id string) string {
	return fmt.Sprintf("%s/%s", monitorsEndpoint, id)
}

var monitorErrors = v2ErrorFormat{
	Noun:       "Monitor",
	Subject:    "the monitor",
	Object:     "the monitor",
	ReadScope:  "monitors:read",
	WriteScope: "monitors:write",
}

// monitorTrigger holds the fields of every trigger_config variant. The API's
// trigger_config is a oneOf with the discriminator type. A field that the
// variant does not have is nil, and omitempty leaves it out of the request.
// Query is the MonitorQuery document exactly as the configuration gives it.
type monitorTrigger struct {
	Type               string                    `json:"type"`
	SkipIfEmpty        *bool                     `json:"skip_if_empty,omitempty"`
	Query              json.RawMessage           `json:"query,omitempty"`
	Operator           *string                   `json:"operator,omitempty"`
	Threshold          *float64                  `json:"threshold,omitempty"`
	Trigger            *string                   `json:"trigger,omitempty"`
	Sensitivity        *float64                  `json:"sensitivity,omitempty"`
	TriggerOnUpperBand *bool                     `json:"trigger_on_upper_band,omitempty"`
	TriggerOnLowerBand *bool                     `json:"trigger_on_lower_band,omitempty"`
	MinimumThreshold   *float64                  `json:"minimum_threshold,omitempty"`
	UpperPercentile    *float64                  `json:"upper_percentile,omitempty"`
	LowerPercentile    *float64                  `json:"lower_percentile,omitempty"`
	Deviations         *float64                  `json:"deviations,omitempty"`
	Thresholds         *[]monitorBudgetThreshold `json:"thresholds,omitempty"`
}

// monitorBudgetThreshold mirrors BudgetThreshold.
type monitorBudgetThreshold struct {
	Metric string  `json:"metric"`
	Unit   string  `json:"unit"`
	Value  float64 `json:"value"`
}

// monitorFrequency holds the fields of every notification_frequency_config
// variant. Only the limit_x_* variants have frequency_interval.
type monitorFrequency struct {
	FrequencyType     string `json:"frequency_type"`
	FrequencyInterval *int64 `json:"frequency_interval,omitempty"`
}

// monitorPartitionConfig mirrors PartitionConfig.
type monitorPartitionConfig struct {
	PartitionColumns *[]monitorPartitionColumn `json:"partition_columns"`
}

type monitorPartitionColumn struct {
	Title string `json:"title"`
}

// monitorCreatePayload mirrors MonitorCreate. It is also the body of an
// update: PUT replaces the full monitor, so the clearable fields are sent as
// an explicit null rather than omitted. A nil PartitionConfig sends
// "partition_config": null, which clears it. DestinationIds is never nil, so
// an empty set sends [].
type monitorCreatePayload struct {
	Name                        string                  `json:"name"`
	DataSource                  string                  `json:"data_source"`
	State                       string                  `json:"state"`
	Schedule                    string                  `json:"schedule"`
	Cron                        *string                 `json:"cron"`
	DisplayInFeed               *bool                   `json:"display_in_feed,omitempty"`
	TeamId                      *string                 `json:"team_id"`
	BudgetId                    *string                 `json:"budget_id"`
	SnowflakeAccountId          *string                 `json:"snowflake_account_id"`
	TriggerConfig               monitorTrigger          `json:"trigger_config"`
	MessageConfig               json.RawMessage         `json:"message_config"`
	PartitionConfig             *monitorPartitionConfig `json:"partition_config"`
	NotificationFrequencyConfig monitorFrequency        `json:"notification_frequency_config"`
	DestinationIds              []string                `json:"destination_ids"`
}

// monitorResponse mirrors the MonitorV2 fields this provider keeps. The run
// and feed fields are not decoded. See generator_config.v2.yml.
type monitorResponse struct {
	Id                          string                  `json:"id"`
	Etag                        string                  `json:"etag"`
	Name                        string                  `json:"name"`
	DataSource                  string                  `json:"data_source"`
	State                       string                  `json:"state"`
	Schedule                    string                  `json:"schedule"`
	Cron                        *string                 `json:"cron"`
	DisplayInFeed               bool                    `json:"display_in_feed"`
	TeamId                      *string                 `json:"team_id"`
	BudgetId                    *string                 `json:"budget_id"`
	SnowflakeAccountId          *string                 `json:"snowflake_account_id"`
	Type                        string                  `json:"type"`
	TriggerConfig               monitorTrigger          `json:"trigger_config"`
	MessageConfig               json.RawMessage         `json:"message_config"`
	PartitionConfig             *monitorPartitionConfig `json:"partition_config"`
	NotificationFrequencyConfig monitorFrequency        `json:"notification_frequency_config"`
	DestinationIds              []string                `json:"destination_ids"`
	Status                      string                  `json:"status"`
	CreatedBy                   *string                 `json:"created_by"`
	UpdatedBy                   *string                 `json:"updated_by"`
	CreateTime                  string                  `json:"create_time"`
	UpdateTime                  string                  `json:"update_time"`
}

// rawJSONString returns a JSON document as a string. It returns nil for an
// absent document or a JSON null.
func rawJSONString(raw json.RawMessage) *string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	result := string(trimmed)
	return &result
}

// rawJSON returns a configured JSON string as a json.RawMessage. It returns
// nil for a null or unknown value, so an omitempty field is left out.
func rawJSON(value types.String) json.RawMessage {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	return json.RawMessage(value.ValueString())
}

func buildMonitorCreate(ctx context.Context, plan *monitorModel) (*monitorCreatePayload, diag.Diagnostics) {
	var diags diag.Diagnostics

	trigger, triggerDiags := monitorTriggerPayload(ctx, plan.TriggerConfig)
	diags.Append(triggerDiags...)

	var frequency monitorFrequencyModel
	diags.Append(plan.NotificationFrequencyConfig.As(ctx, &frequency, basetypes.ObjectAsOptions{})...)

	destinationIds := []string{}
	diags.Append(plan.DestinationIds.ElementsAs(ctx, &destinationIds, false)...)

	partitions, partitionDiags := stringListPointer(ctx, plan.PartitionColumns)
	diags.Append(partitionDiags...)

	if diags.HasError() {
		return nil, diags
	}

	return &monitorCreatePayload{
		Name:               plan.Name.ValueString(),
		DataSource:         plan.DataSource.ValueString(),
		State:              plan.State.ValueString(),
		Schedule:           plan.Schedule.ValueString(),
		Cron:               stringPointer(plan.Cron),
		DisplayInFeed:      boolPointer(plan.DisplayInFeed),
		TeamId:             stringPointer(plan.TeamId),
		BudgetId:           stringPointer(plan.BudgetId),
		SnowflakeAccountId: stringPointer(plan.SnowflakeAccountId),
		TriggerConfig:      trigger,
		MessageConfig:      rawJSON(plan.MessageConfigJson),
		PartitionConfig:    monitorPartitionPayload(partitions),
		NotificationFrequencyConfig: monitorFrequency{
			FrequencyType:     frequency.FrequencyType.ValueString(),
			FrequencyInterval: int64Pointer(frequency.FrequencyInterval),
		},
		DestinationIds: destinationIds,
	}, diags
}

// monitorPartitionPayload builds partition_config from the configured column
// titles. A nil list gives a nil config, which the request sends as null.
func monitorPartitionPayload(titles *[]string) *monitorPartitionConfig {
	if titles == nil {
		return nil
	}
	columns := make([]monitorPartitionColumn, len(*titles))
	for i, title := range *titles {
		columns[i] = monitorPartitionColumn{Title: title}
	}
	return &monitorPartitionConfig{PartitionColumns: &columns}
}

// monitorPartitionTitles is monitorPartitionPayload's inverse. It returns nil
// when the response has no partition columns.
func monitorPartitionTitles(config *monitorPartitionConfig) *[]string {
	if config == nil || config.PartitionColumns == nil {
		return nil
	}
	titles := make([]string, len(*config.PartitionColumns))
	for i, column := range *config.PartitionColumns {
		titles[i] = column.Title
	}
	return &titles
}

// monitorTriggerPayload converts the configured trigger_config into the
// request's shape.
func monitorTriggerPayload(ctx context.Context, value types.Object) (monitorTrigger, diag.Diagnostics) {
	var model monitorTriggerModel
	diags := value.As(ctx, &model, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return monitorTrigger{}, diags
	}

	trigger := monitorTrigger{
		Type:               model.Type.ValueString(),
		SkipIfEmpty:        boolPointer(model.SkipIfEmpty),
		Query:              rawJSON(model.QueryJson),
		Operator:           stringPointer(model.Operator),
		Threshold:          float64Pointer(model.Threshold),
		Trigger:            stringPointer(model.Trigger),
		Sensitivity:        float64Pointer(model.Sensitivity),
		TriggerOnUpperBand: boolPointer(model.TriggerOnUpperBand),
		TriggerOnLowerBand: boolPointer(model.TriggerOnLowerBand),
		MinimumThreshold:   float64Pointer(model.MinimumThreshold),
		UpperPercentile:    float64Pointer(model.UpperPercentile),
		LowerPercentile:    float64Pointer(model.LowerPercentile),
		Deviations:         float64Pointer(model.Deviations),
	}

	if !model.Thresholds.IsNull() && !model.Thresholds.IsUnknown() {
		var thresholds []monitorBudgetThresholdModel
		diags.Append(model.Thresholds.ElementsAs(ctx, &thresholds, false)...)
		if diags.HasError() {
			return monitorTrigger{}, diags
		}
		wire := make([]monitorBudgetThreshold, len(thresholds))
		for i, threshold := range thresholds {
			wire[i] = monitorBudgetThreshold{
				Metric: threshold.Metric.ValueString(),
				Unit:   threshold.Unit.ValueString(),
				Value:  threshold.Value.ValueFloat64(),
			}
		}
		trigger.Thresholds = &wire
	}

	return trigger, diags
}

// monitorTriggerObject builds the trigger_config value from a response.
//
// source is the configured trigger_config, or null after an import. Two
// fields keep the configured null when the response holds what the
// configuration did not set:
//
//   - trigger_on_upper_band and trigger_on_lower_band, when the API returns
//     their default, true. A false still reads as drift.
//   - query_json, which is optional for budget_threshold. The configuration
//     sets no query, so a query that the API holds is not drift, the same
//     rule that preserveConfiguredJSON follows inside a query.
//
// Every other field comes from the response.
func monitorTriggerObject(ctx context.Context, source types.Object, response monitorTrigger) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics

	var configured monitorTriggerModel
	hasSource := !source.IsNull() && !source.IsUnknown()
	if hasSource {
		diags.Append(source.As(ctx, &configured, basetypes.ObjectAsOptions{})...)
		if diags.HasError() {
			return types.ObjectNull(monitorTriggerType().AttrTypes), diags
		}
	}

	defaultTrueBand := func(configured types.Bool, returned *bool) types.Bool {
		if hasSource && configured.IsNull() && (returned == nil || *returned) {
			return types.BoolNull()
		}
		return boolValue(returned)
	}

	// With no source, the zero monitorTriggerModel holds a null QueryJson, so
	// preserveConfiguredJSON uses the response.
	queryJSON := types.StringNull()
	if !hasSource || !configured.QueryJson.IsNull() {
		queryJSON = preserveConfiguredJSON(configured.QueryJson, rawJSONString(response.Query))
	}

	model := monitorTriggerModel{
		Type:               types.StringValue(response.Type),
		SkipIfEmpty:        boolValue(response.SkipIfEmpty),
		QueryJson:          queryJSON,
		Operator:           stringValue(response.Operator),
		Threshold:          float64Value(response.Threshold),
		Trigger:            stringValue(response.Trigger),
		Sensitivity:        float64Value(response.Sensitivity),
		TriggerOnUpperBand: defaultTrueBand(configured.TriggerOnUpperBand, response.TriggerOnUpperBand),
		TriggerOnLowerBand: defaultTrueBand(configured.TriggerOnLowerBand, response.TriggerOnLowerBand),
		MinimumThreshold:   float64Value(response.MinimumThreshold),
		UpperPercentile:    float64Value(response.UpperPercentile),
		LowerPercentile:    float64Value(response.LowerPercentile),
		Deviations:         float64Value(response.Deviations),
		Thresholds:         types.ListNull(monitorBudgetThresholdType()),
	}

	if response.Thresholds != nil {
		thresholds := make([]monitorBudgetThresholdModel, len(*response.Thresholds))
		for i, threshold := range *response.Thresholds {
			thresholds[i] = monitorBudgetThresholdModel{
				Metric: types.StringValue(threshold.Metric),
				Unit:   types.StringValue(threshold.Unit),
				Value:  types.Float64Value(threshold.Value),
			}
		}
		list, listDiags := types.ListValueFrom(ctx, monitorBudgetThresholdType(), thresholds)
		diags.Append(listDiags...)
		model.Thresholds = list
	}

	object, objectDiags := types.ObjectValueFrom(ctx, monitorTriggerType().AttrTypes, model)
	diags.Append(objectDiags...)
	return object, diags
}

// monitorFrequencyObject builds the notification_frequency_config value from
// a response.
func monitorFrequencyObject(ctx context.Context, response monitorFrequency) (types.Object, diag.Diagnostics) {
	return types.ObjectValueFrom(ctx, monitorFrequencyType().AttrTypes, monitorFrequencyModel{
		FrequencyType:     types.StringValue(response.FrequencyType),
		FrequencyInterval: int64Value(response.FrequencyInterval),
	})
}

// applyMonitorResponse writes an API response onto the model. The API adds
// defaults and generated text to message_config and to the trigger's query,
// so those two JSON documents use preserveConfiguredJSON. See
// monitorTriggerObject for the trigger's optional fields.
func applyMonitorResponse(ctx context.Context, model, source *monitorModel, response *monitorResponse) diag.Diagnostics {
	var diags diag.Diagnostics

	model.Id = types.StringValue(response.Id)
	model.Etag = types.StringValue(response.Etag)
	model.Name = types.StringValue(response.Name)
	model.DataSource = types.StringValue(response.DataSource)
	model.State = types.StringValue(response.State)
	model.Schedule = types.StringValue(response.Schedule)
	model.Cron = stringValue(response.Cron)
	model.DisplayInFeed = types.BoolValue(response.DisplayInFeed)
	model.TeamId = stringValue(response.TeamId)
	model.BudgetId = stringValue(response.BudgetId)
	model.SnowflakeAccountId = stringValue(response.SnowflakeAccountId)
	model.Type = types.StringValue(response.Type)
	model.Status = types.StringValue(response.Status)
	model.CreatedBy = stringValue(response.CreatedBy)
	model.UpdatedBy = stringValue(response.UpdatedBy)
	model.CreateTime = types.StringValue(response.CreateTime)
	model.UpdateTime = types.StringValue(response.UpdateTime)
	model.MessageConfigJson = preserveConfiguredJSON(source.MessageConfigJson, rawJSONString(response.MessageConfig))
	model.PartitionColumns = preserveEquivalentList(ctx, source.PartitionColumns, monitorPartitionTitles(response.PartitionConfig))

	destinationIds := response.DestinationIds
	if destinationIds == nil {
		destinationIds = []string{}
	}
	destinations, destinationDiags := types.SetValueFrom(ctx, types.StringType, destinationIds)
	diags.Append(destinationDiags...)
	model.DestinationIds = destinations

	trigger, triggerDiags := monitorTriggerObject(ctx, source.TriggerConfig, response.TriggerConfig)
	diags.Append(triggerDiags...)
	model.TriggerConfig = trigger

	frequency, frequencyDiags := monitorFrequencyObject(ctx, response.NotificationFrequencyConfig)
	diags.Append(frequencyDiags...)
	model.NotificationFrequencyConfig = frequency

	return diags
}
