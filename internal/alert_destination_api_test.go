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
)

func msTeamsDestinationJSON(etag, channel string) string {
	return `{"id":"d-1","etag":"` + etag + `","integration_id":"i-1","integration_type":"ms_teams_webhook",` +
		`"status":"unknown","error_message":null,"last_used_time":null,"create_time":"2026-11-13T00:00:00Z",` +
		`"update_time":"2026-11-13T00:00:00Z","channel":"` + channel + `","webhook_url_configured":true,` +
		`"webhook_url_update_time":"2026-11-13T00:00:00Z"}`
}

func newMsTeamsDestinationForTest(serverURL string) *v2Resource[msTeamsAlertDestinationModel, alertDestinationResponse] {
	r := NewMsTeamsAlertDestinationResource().(*v2Resource[msTeamsAlertDestinationModel, alertDestinationResponse])
	r.client = NewAPIClient("key", "org", serverURL)
	return r
}

func msTeamsDestinationPlan() msTeamsAlertDestinationModel {
	var m msTeamsAlertDestinationModel
	m.Id = types.StringUnknown()
	m.Etag = types.StringUnknown()
	m.IntegrationId = types.StringValue("i-1")
	m.Status = types.StringUnknown()
	m.ErrorMessage = types.StringUnknown()
	m.LastUsedTime = types.StringUnknown()
	m.CreateTime = types.StringUnknown()
	m.UpdateTime = types.StringUnknown()
	m.Channel = types.StringValue("alerts")
	m.WebhookUrl = types.StringValue("https://example.com/hook")
	m.WebhookUrlUpdateTime = types.StringUnknown()
	return m
}

func TestMsTeamsDestinationCreateSendsTypeAndKeepsTheWebhookURL(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"POST /v2/destinations": {status: http.StatusCreated, body: msTeamsDestinationJSON("e-1", "alerts")},
	})
	r := newMsTeamsDestinationForTest(server.URL)
	ctx := context.Background()
	s := r.schema(ctx)

	plan := msTeamsDestinationPlan()
	resp := resource.CreateResponse{State: v2State[msTeamsAlertDestinationModel](t, s, nil)}
	r.Create(ctx, resource.CreateRequest{Plan: tfPlan(t, s, &plan)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", resp.Diagnostics)
	}

	want := `{"integration_type":"ms_teams_webhook","integration_id":"i-1","channel":"alerts","webhook_url":"https://example.com/hook"}`
	if got := server.requests[0].Body; got != want {
		t.Errorf("create body:\n got %s\nwant %s", got, want)
	}

	var state msTeamsAlertDestinationModel
	resp.State.Get(ctx, &state)
	if state.WebhookUrl.ValueString() != "https://example.com/hook" || state.Id.ValueString() != "d-1" {
		t.Errorf("state should keep the webhook URL and take the id, got %+v", state)
	}
}

func TestAlertDestinationUpdateSendsOnlyChangedFields(t *testing.T) {
	state := alertDestinationSettings{
		Channel:    types.StringValue("alerts"),
		WebhookUrl: types.StringValue("https://example.com/old"),
	}
	plan := alertDestinationSettings{
		Channel:    types.StringValue("alerts"),
		WebhookUrl: types.StringValue("https://example.com/new"),
	}
	encoded, _ := json.Marshal(buildAlertDestinationUpdate("ms_teams_webhook", plan, state))
	if want := `{"integration_type":"ms_teams_webhook","webhook_url":"https://example.com/new"}`; string(encoded) != want {
		t.Errorf("update body:\n got %s\nwant %s", encoded, want)
	}
}

func TestAlertDestinationReadRejectsADifferentType(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"GET /v2/destinations/d-1": {status: http.StatusOK, body: msTeamsDestinationJSON("e-1", "alerts")},
	})
	r := NewEmailAlertDestinationResource().(*v2Resource[emailAlertDestinationModel, alertDestinationResponse])

	var state emailAlertDestinationModel
	state.Id = types.StringValue("d-1")
	resp := readV2(t, r, server.URL, state)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), `integration_type "ms_teams_webhook", not "email"`) {
		t.Errorf("a destination of another type should fail the read, got %v", resp.Diagnostics)
	}
}

func TestAlertDestinationDeleteBlockedByAMonitor(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"DELETE /v2/destinations/d-1": {status: http.StatusConflict,
			body: `{"status":409,"detail":"Destination is in use.","code":"delete_blocked_by_dependencies"}`},
	})
	r := newMsTeamsDestinationForTest(server.URL)

	state := msTeamsDestinationPlan()
	state.Id = types.StringValue("d-1")
	state.Etag = types.StringValue("e-1")
	resp := deleteV2(t, r, server.URL, state)
	if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != "MS Teams Alert Destination In Use" {
		t.Errorf("a blocked delete should explain the dependency, got %v", resp.Diagnostics)
	}
	if server.requests[0].IfMatch != "e-1" {
		t.Errorf("delete should send If-Match, got %q", server.requests[0].IfMatch)
	}
}
