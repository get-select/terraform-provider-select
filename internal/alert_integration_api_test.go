// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func alertIntegrationJSON(id, integrationType, displayName string) string {
	return `{"id":"` + id + `","etag":"e-1","display_name":"` + displayName + `","integration_type":"` + integrationType + `",` +
		`"status":"connected","credentials_configured":true,"create_time":"2026-11-13T00:00:00Z",` +
		`"update_time":"2026-11-13T00:00:00Z","slack_workspace_id":null,"slack_workspace_name":null}`
}

func TestAlertIntegrationCreatePostsTheEnableActionWithNoBody(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"POST /v2/alert-integrations/actions/enable-ms-teams-webhook": {
			status: http.StatusOK, body: alertIntegrationJSON("i-1", "ms_teams_webhook", "Microsoft Teams"),
		},
	})
	r := NewAlertIntegrationResource().(*v2Resource[alertIntegrationModel, alertIntegrationResponse])
	r.client = NewAPIClient("key", "org", server.URL)
	ctx := context.Background()
	s := r.schema(ctx)

	plan := alertIntegrationModel{
		Id:                    types.StringUnknown(),
		Etag:                  types.StringUnknown(),
		IntegrationType:       types.StringValue("ms_teams_webhook"),
		DisplayName:           types.StringUnknown(),
		Status:                types.StringUnknown(),
		CredentialsConfigured: types.BoolUnknown(),
		CreateTime:            types.StringUnknown(),
		UpdateTime:            types.StringUnknown(),
	}
	resp := resource.CreateResponse{State: v2State[alertIntegrationModel](t, s, nil)}
	r.Create(ctx, resource.CreateRequest{Plan: tfPlan(t, s, &plan)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", resp.Diagnostics)
	}
	if body := server.requests[0].Body; body != "" {
		t.Errorf("the enable action takes no body, got %q", body)
	}

	var state alertIntegrationModel
	resp.State.Get(ctx, &state)
	if state.Id.ValueString() != "i-1" || state.DisplayName.ValueString() != "Microsoft Teams" {
		t.Errorf("state should come from the response, got %+v", state)
	}
}

func TestAlertIntegrationDeleteBlockedByDestinations(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"DELETE /v2/alert-integrations/i-1": {status: http.StatusConflict,
			body: `{"status":409,"detail":"Integration has destinations.","code":"delete_blocked_by_dependencies"}`},
	})
	r := NewAlertIntegrationResource().(*v2Resource[alertIntegrationModel, alertIntegrationResponse])

	state := alertIntegrationModel{Id: types.StringValue("i-1"), Etag: types.StringValue("e-1"), IntegrationType: types.StringValue("email")}
	resp := deleteV2(t, r, server.URL, state)
	if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != "Alert Integration In Use" {
		t.Errorf("a blocked delete should explain the dependency, got %v", resp.Diagnostics)
	}
}

func TestMatchAlertIntegration(t *testing.T) {
	workspace := func(name string) *string { return &name }
	candidates := []alertIntegrationResponse{
		{Id: "i-1", IntegrationType: "slack", DisplayName: "Slack", SlackWorkspaceName: workspace("Acme")},
		{Id: "i-2", IntegrationType: "slack", DisplayName: "Slack", SlackWorkspaceName: workspace("Beta")},
	}

	if got, d := matchAlertIntegration("slack", types.StringNull(), types.StringValue("Beta"), candidates); d != nil || got.Id != "i-2" {
		t.Errorf("the workspace filter should find i-2, got %v %v", got, d)
	}

	_, d := matchAlertIntegration("slack", types.StringNull(), types.StringNull(), candidates)
	if d == nil || d.Summary() != "Multiple Alert Integrations Found" || !strings.Contains(d.Detail(), "i-1") {
		t.Errorf("two matches should name both, got %v", d)
	}

	_, d = matchAlertIntegration("slack", types.StringNull(), types.StringValue("Gamma"), candidates)
	if d == nil || d.Summary() != "Alert Integration Not Found" || !strings.Contains(d.Detail(), `workspace "Acme"`) {
		t.Errorf("no match should name the candidates, got %v", d)
	}
}

func TestAlertIntegrationReadRejectsADifferentType(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"GET /v2/alert-integrations/i-9": {status: http.StatusOK, body: alertIntegrationJSON("i-9", "ms_teams_webhook", "Microsoft Teams")},
	})
	r := NewAlertIntegrationResource().(*v2Resource[alertIntegrationModel, alertIntegrationResponse])

	resp := readV2(t, r, server.URL, alertIntegrationModel{Id: types.StringValue("i-9"), IntegrationType: types.StringValue("email")})
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), `"ms_teams_webhook", not "email"`) {
		t.Errorf("an MS Teams id imported as email should fail the read, got %v", resp.Diagnostics)
	}
}

func TestAlertIntegrationImportCarriesTheType(t *testing.T) {
	r := NewAlertIntegrationResource().(*v2Resource[alertIntegrationModel, alertIntegrationResponse])
	ctx := context.Background()
	s := r.schema(ctx)

	resp := resource.ImportStateResponse{State: v2State[alertIntegrationModel](t, s, nil)}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "email/i-1"}, &resp)
	var state alertIntegrationModel
	resp.State.Get(ctx, &state)
	if resp.Diagnostics.HasError() || state.IntegrationType.ValueString() != "email" || state.Id.ValueString() != "i-1" {
		t.Errorf("the import should set integration_type and id, got %+v %v", state, resp.Diagnostics)
	}

	resp = resource.ImportStateResponse{State: v2State[alertIntegrationModel](t, s, nil)}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "i-1"}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Error("an import ID without the type should fail")
	}
}

func TestAlertIntegrationReadRejectsASlackIntegration(t *testing.T) {
	server := newRoutedServer(t, map[string]cannedResponse{
		"GET /v2/alert-integrations/i-9": {status: http.StatusOK, body: alertIntegrationJSON("i-9", "slack", "Slack")},
	})
	r := NewAlertIntegrationResource().(*v2Resource[alertIntegrationModel, alertIntegrationResponse])

	resp := readV2(t, r, server.URL, alertIntegrationModel{Id: types.StringValue("i-9"), IntegrationType: types.StringValue("slack")})
	if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != "Unsupported Alert Integration Type" {
		t.Errorf("an imported Slack integration should fail the read, got %v", resp.Diagnostics)
	}
}
