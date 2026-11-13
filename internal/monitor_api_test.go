// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

const monitorMessageConfig = `{"sections":[{"section_type":"chart","query":{"query_class":"OrgDailySpendQuery","query_object":{"measures":["spend"]},"period":{"period":30,"grain":"day"}}}]}`

// monitorMessageConfigReturned is monitorMessageConfig as the API returns it:
// with a generated chart title and the query defaults added.
const monitorMessageConfigReturned = `{"title":null,"sections":[{"section_type":"chart","enabled":true,"title":"Daily spend","highlight_last_point":false,"threshold":null,"bands":null,"group_by":null,"query":{"query_class":"OrgDailySpendQuery","query_object":{"dimensions":[],"measures":["spend"],"filters":[],"measure_filters":[],"sort":[],"limit":null},"period":{"period":30,"grain":"day","date_column":"day","fill_missing_dates":false},"compare_with":null}}],"templated_values":{}}`

const monitorQuery = `{"query_class":"OrgDailySpendQuery","query_object":{"measures":["spend"]},"period":{"period":7,"grain":"day"}}`

const monitorQueryReturned = `{"query_class":"OrgDailySpendQuery","query_object":{"dimensions":[],"measures":["spend"],"filters":[],"measure_filters":[],"sort":[],"limit":null},"period":{"period":7,"grain":"day","date_column":"day","fill_missing_dates":false},"compare_with":null}`

func monitorTriggerValue(t *testing.T, model monitorTriggerModel) types.Object {
	t.Helper()
	ctx := context.Background()
	if model.Thresholds.IsNull() && model.Thresholds.ElementType(ctx) == nil {
		model.Thresholds = types.ListNull(monitorBudgetThresholdType())
	}
	object, diags := types.ObjectValueFrom(ctx, monitorTriggerType().AttrTypes, model)
	if diags.HasError() {
		t.Fatalf("building trigger_config: %v", diags)
	}
	return object
}

func budgetThresholds(t *testing.T, values ...float64) types.List {
	t.Helper()
	elements := make([]monitorBudgetThresholdModel, len(values))
	for i, value := range values {
		elements[i] = monitorBudgetThresholdModel{
			Metric: types.StringValue("actual_spend"),
			Unit:   types.StringValue("percent"),
			Value:  types.Float64Value(value),
		}
	}
	list, diags := types.ListValueFrom(context.Background(), monitorBudgetThresholdType(), elements)
	if diags.HasError() {
		t.Fatalf("building thresholds: %v", diags)
	}
	return list
}

func monitorFrequencyValue(t *testing.T, frequencyType string, interval types.Int64) types.Object {
	t.Helper()
	object, diags := types.ObjectValueFrom(context.Background(), monitorFrequencyType().AttrTypes, monitorFrequencyModel{
		FrequencyType:     types.StringValue(frequencyType),
		FrequencyInterval: interval,
	})
	if diags.HasError() {
		t.Fatalf("building notification_frequency_config: %v", diags)
	}
	return object
}

func stringSet(t *testing.T, values ...string) types.Set {
	t.Helper()
	set, diags := types.SetValueFrom(context.Background(), types.StringType, values)
	if diags.HasError() {
		t.Fatalf("building set: %v", diags)
	}
	return set
}

// budgetMonitorPlan is a planned budget_threshold monitor, with the computed
// attributes unknown as Terraform gives them to Create.
func budgetMonitorPlan(t *testing.T) monitorModel {
	t.Helper()
	var m monitorModel
	m.Id = types.StringUnknown()
	m.Etag = types.StringUnknown()
	m.Name = types.StringValue("Budget alert")
	m.DataSource = types.StringValue("budget")
	m.State = types.StringValue("enabled")
	m.Schedule = types.StringValue("daily")
	m.Cron = types.StringNull()
	m.DisplayInFeed = types.BoolValue(true)
	m.TeamId = types.StringNull()
	m.BudgetId = types.StringValue("b-1")
	m.SnowflakeAccountId = types.StringNull()
	m.Type = types.StringUnknown()
	m.Status = types.StringUnknown()
	m.CreatedBy = types.StringUnknown()
	m.UpdatedBy = types.StringUnknown()
	m.CreateTime = types.StringUnknown()
	m.UpdateTime = types.StringUnknown()
	m.DestinationIds = stringSet(t, "d-2", "d-1")
	m.PartitionColumns = types.ListNull(types.StringType)
	m.NotificationFrequencyConfig = monitorFrequencyValue(t, "on_trigger", types.Int64Null())
	m.TriggerConfig = monitorTriggerValue(t, monitorTriggerModel{
		Type:       types.StringValue("budget_threshold"),
		Thresholds: budgetThresholds(t, 80),
	})
	m.MessageConfigJson = types.StringValue(monitorMessageConfig)
	return m
}

func budgetMonitorJSON(etag string, threshold float64) string {
	thresholdJSON, _ := json.Marshal(threshold)
	return `{"id":"m-1","etag":"` + etag + `","name":"Budget alert","data_source":"budget","state":"enabled",` +
		`"schedule":"daily","cron":null,"display_in_feed":true,"team_id":null,"budget_id":"b-1",` +
		`"snowflake_account_id":null,"type":"budget_threshold",` +
		`"trigger_config":{"type":"budget_threshold","query":null,"thresholds":[{"metric":"actual_spend","unit":"percent","value":` + string(thresholdJSON) + `}]},` +
		`"message_config":` + monitorMessageConfigReturned + `,"partition_config":null,` +
		`"notification_frequency_config":{"frequency_type":"on_trigger"},"destination_ids":["d-1","d-2"],` +
		`"status":"new","latest_run_status":"new","created_by":"u@example.com","updated_by":null,` +
		`"create_time":"2026-11-13T00:00:00Z","update_time":"2026-11-13T00:00:00Z"}`
}

func newMonitorForTest(serverURL string) *v2Resource[monitorModel, monitorResponse] {
	r := NewMonitorResource().(*v2Resource[monitorModel, monitorResponse])
	r.client = NewAPIClient("key", "org", serverURL)
	return r
}

func TestMonitorCreateSendsTheFullBodyAndKeepsTheConfiguredJSON(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"POST /v2/monitors": {status: http.StatusCreated, body: budgetMonitorJSON("e-1", 80)},
	})
	r := newMonitorForTest(server.URL)
	ctx := context.Background()
	s := r.schema(ctx)

	plan := budgetMonitorPlan(t)
	resp := resource.CreateResponse{State: v2State[monitorModel](t, s, nil)}
	r.Create(ctx, resource.CreateRequest{Plan: tfPlan(t, s, &plan)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", resp.Diagnostics)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(server.requests[0].Body), &body); err != nil {
		t.Fatalf("decoding the request: %v", err)
	}
	for _, key := range []string{"cron", "team_id", "snowflake_account_id", "partition_config"} {
		if value, present := body[key]; !present || value != nil {
			t.Errorf("%s should be sent as null, got %v (present %v)", key, value, present)
		}
	}
	trigger := body["trigger_config"].(map[string]any)
	if len(trigger) != 2 || trigger["type"] != "budget_threshold" || trigger["thresholds"] == nil {
		t.Errorf("trigger_config should hold only type and thresholds, got %v", trigger)
	}
	if ids := body["destination_ids"].([]any); len(ids) != 2 {
		t.Errorf("destination_ids should be sent, got %v", ids)
	}

	var state monitorModel
	if diags := resp.State.Get(ctx, &state); diags.HasError() {
		t.Fatalf("reading state: %v", diags)
	}
	if state.MessageConfigJson.ValueString() != monitorMessageConfig {
		t.Errorf("message_config_json should keep the configured JSON, got %s", state.MessageConfigJson)
	}
	if !state.DestinationIds.Equal(plan.DestinationIds) {
		t.Errorf("destination_ids should equal the plan in any order, got %s", state.DestinationIds)
	}
	if !state.TriggerConfig.Equal(plan.TriggerConfig) {
		t.Errorf("trigger_config should equal the plan, got %s", state.TriggerConfig)
	}
	if !state.PartitionColumns.IsNull() {
		t.Errorf("partition_columns should stay null, got %s", state.PartitionColumns)
	}
	if state.Id.ValueString() != "m-1" || state.Type.ValueString() != "budget_threshold" {
		t.Errorf("computed attributes should come from the response, got %+v", state.MonitorModel)
	}
}

func TestMonitorUpdateSendsPutWithIfMatch(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"PUT /v2/monitors/m-1": {status: http.StatusOK, body: budgetMonitorJSON("e-2", 90)},
	})
	r := newMonitorForTest(server.URL)
	ctx := context.Background()
	s := r.schema(ctx)

	var prior monitorResponse
	if err := json.Unmarshal([]byte(budgetMonitorJSON("e-1", 80)), &prior); err != nil {
		t.Fatal(err)
	}
	plan := budgetMonitorPlan(t)
	state := plan
	if diags := applyMonitorResponse(ctx, &state, &plan, &prior); diags.HasError() {
		t.Fatalf("building state: %v", diags)
	}
	plan.Id = state.Id
	plan.CreateTime = state.CreateTime
	plan.CreatedBy = state.CreatedBy
	plan.TriggerConfig = monitorTriggerValue(t, monitorTriggerModel{
		Type:       types.StringValue("budget_threshold"),
		Thresholds: budgetThresholds(t, 90),
	})

	resp := resource.UpdateResponse{State: v2State(t, s, &state)}
	r.Update(ctx, resource.UpdateRequest{Plan: tfPlan(t, s, &plan), State: v2State(t, s, &state)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", resp.Diagnostics)
	}

	got := server.requests[0]
	if got.Method != http.MethodPut || got.IfMatch != "e-1" {
		t.Errorf("update should PUT with the state's ETag, got %s If-Match %q", got.Method, got.IfMatch)
	}
	if !strings.Contains(got.Body, `"name":"Budget alert"`) || !strings.Contains(got.Body, `"value":90`) {
		t.Errorf("update should send the full create body, got %s", got.Body)
	}
}

func TestMonitorApplyResponseKeepsUnsetTriggerDefaultsNull(t *testing.T) {
	ctx := context.Background()
	configured := monitorTriggerValue(t, monitorTriggerModel{
		Type:        types.StringValue("anomaly_prophet"),
		QueryJson:   types.StringValue(monitorQuery),
		Sensitivity: types.Float64Value(10),
	})
	upper, lower := true, true
	sensitivity := 10.0
	response := monitorTrigger{
		Type:               "anomaly_prophet",
		Query:              json.RawMessage(monitorQueryReturned),
		Sensitivity:        &sensitivity,
		TriggerOnUpperBand: &upper,
		TriggerOnLowerBand: &lower,
	}

	got, diags := monitorTriggerObject(ctx, configured, response)
	if diags.HasError() {
		t.Fatalf("building trigger_config: %v", diags)
	}
	if !got.Equal(configured) {
		t.Errorf("an unset band and the query defaults should not change the value, got %s", got)
	}

	lower = false
	changed, diags := monitorTriggerObject(ctx, configured, response)
	if diags.HasError() {
		t.Fatalf("building trigger_config: %v", diags)
	}
	var changedModel monitorTriggerModel
	changed.As(ctx, &changedModel, basetypes.ObjectAsOptions{})
	if changedModel.TriggerOnLowerBand.IsNull() || changedModel.TriggerOnLowerBand.ValueBool() {
		t.Errorf("a band changed to false outside Terraform should read as drift, got %s", changedModel.TriggerOnLowerBand)
	}
	lower = true

	imported, diags := monitorTriggerObject(ctx, types.ObjectNull(monitorTriggerType().AttrTypes), response)
	if diags.HasError() {
		t.Fatalf("building trigger_config: %v", diags)
	}
	var model monitorTriggerModel
	imported.As(ctx, &model, basetypes.ObjectAsOptions{})
	if !model.TriggerOnUpperBand.ValueBool() || model.QueryJson.IsNull() {
		t.Errorf("with no configuration every field should come from the response, got %+v", model)
	}
}

func TestMonitorPartitionColumns(t *testing.T) {
	titles := []string{"warehouse_name"}
	payload := monitorPartitionPayload(&titles)
	encoded, _ := json.Marshal(payload)
	if string(encoded) != `{"partition_columns":[{"title":"warehouse_name"}]}` {
		t.Errorf("partition columns should nest as titles, got %s", encoded)
	}
	if back := monitorPartitionTitles(payload); back == nil || (*back)[0] != "warehouse_name" {
		t.Errorf("partition titles should round-trip, got %v", back)
	}
	if monitorPartitionPayload(nil) != nil || monitorPartitionTitles(nil) != nil {
		t.Error("no partition columns should give a null partition_config")
	}
}

func TestValidateMonitorConfig(t *testing.T) {
	cases := []struct {
		name   string
		modify func(m *monitorModel)
		errors []string
	}{
		{
			name:   "valid budget monitor",
			modify: func(m *monitorModel) {},
		},
		{
			name: "condition trigger with missing and extra fields",
			modify: func(m *monitorModel) {
				m.TriggerConfig = monitorTriggerValue(t, monitorTriggerModel{
					Type:        types.StringValue("condition"),
					QueryJson:   types.StringValue(monitorQuery),
					Operator:    types.StringValue(">"),
					Sensitivity: types.Float64Value(3),
				})
			},
			errors: []string{
				"trigger_config.threshold, trigger_config.trigger is required for type \"condition\".",
				"trigger_config.sensitivity is not valid for type \"condition\".",
			},
		},
		{
			name: "invalid query JSON",
			modify: func(m *monitorModel) {
				m.TriggerConfig = monitorTriggerValue(t, monitorTriggerModel{
					Type:       types.StringValue("budget_threshold"),
					QueryJson:  types.StringValue("{"),
					Thresholds: budgetThresholds(t, 80),
				})
			},
			errors: []string{"trigger_config.query_json must be a JSON-encoded document"},
		},
		{
			name: "limit frequency without an interval",
			modify: func(m *monitorModel) {
				m.NotificationFrequencyConfig = monitorFrequencyValue(t, "limit_x_days", types.Int64Null())
			},
			errors: []string{"notification_frequency_config.frequency_interval is required"},
		},
		{
			name: "interval on a frequency that has none",
			modify: func(m *monitorModel) {
				m.NotificationFrequencyConfig = monitorFrequencyValue(t, "on_trigger", types.Int64Value(2))
			},
			errors: []string{"notification_frequency_config.frequency_interval is not valid"},
		},
		{
			name: "cron schedule without cron",
			modify: func(m *monitorModel) {
				m.Schedule = types.StringValue("cron")
				m.DataSource = types.StringValue("custom")
				m.SnowflakeAccountId = types.StringValue("acme-main")
			},
			errors: []string{`cron is required when schedule is "cron".`},
		},
		{
			name: "cron schedule on a data source that is not custom",
			modify: func(m *monitorModel) {
				m.Schedule = types.StringValue("cron")
				m.Cron = types.StringValue("0 9 * * *")
			},
			errors: []string{`schedule "cron" is valid only when data_source is "custom".`},
		},
		{
			name: "custom data source without a Snowflake account",
			modify: func(m *monitorModel) {
				m.DataSource = types.StringValue("custom")
			},
			errors: []string{`snowflake_account_id is required when data_source is "custom".`},
		},
		{
			name: "invalid message JSON",
			modify: func(m *monitorModel) {
				m.MessageConfigJson = types.StringValue("not json")
			},
			errors: []string{"message_config_json must be a JSON-encoded document"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := budgetMonitorPlan(t)
			tc.modify(&m)
			diags := validateMonitorConfig(context.Background(), &m)
			if len(diags) != len(tc.errors) {
				t.Fatalf("want %d diagnostics, got %v", len(tc.errors), diags)
			}
			for i, want := range tc.errors {
				if !strings.Contains(diags[i].Detail(), want) {
					t.Errorf("diagnostic %d should contain %q, got %q", i, want, diags[i].Detail())
				}
			}
		})
	}
}
