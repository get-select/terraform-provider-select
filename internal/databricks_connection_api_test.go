// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"
	"testing"

	"terraform-provider-select/internal/provider/resource_databricks_connection"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func databricksCredentials(clientId, clientSecret string) resource_databricks_connection.CredentialsValue {
	return resource_databricks_connection.NewCredentialsValueMust(
		resource_databricks_connection.CredentialsValue{}.AttributeTypes(context.Background()),
		map[string]attr.Value{
			"client_id":     types.StringValue(clientId),
			"client_secret": types.StringValue(clientSecret),
		},
	)
}

func databricksConnection() resource_databricks_connection.DatabricksConnectionModel {
	return resource_databricks_connection.DatabricksConnectionModel{
		Id:                       types.StringValue("2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01"),
		Etag:                     types.StringValue(`"abc123"`),
		Name:                     types.StringValue("Acme Production"),
		DatabricksAccountId:      types.StringValue("11111111-2222-3333-4444-555555555555"),
		PrimaryWorkspaceUrl:      types.StringValue("https://acme.cloud.databricks.com"),
		WarehouseId:              types.StringValue("abc123def456"),
		Credentials:              databricksCredentials("client-one", "secret-one"),
		SyncEnabled:              types.BoolValue(true),
		QuerySanitizationEnabled: types.BoolValue(false),
	}
}

// A create body has to carry every field the API requires, including the two
// booleans, whose schema defaults mean the plan always has a value for them.
func TestDatabricksCreatePayloadCarriesEveryRequiredField(t *testing.T) {
	plan := databricksConnection()

	payload := marshal(t, buildDatabricksConnectionCreate(&plan))

	for field, expected := range map[string]any{
		"name":                       "Acme Production",
		"databricks_account_id":      "11111111-2222-3333-4444-555555555555",
		"primary_workspace_url":      "https://acme.cloud.databricks.com",
		"warehouse_id":               "abc123def456",
		"sync_enabled":               true,
		"query_sanitization_enabled": false,
	} {
		if payload[field] != expected {
			t.Errorf("%s should be %v, got %v", field, expected, payload[field])
		}
	}

	credentials, ok := payload["credentials"].(map[string]any)
	if !ok {
		t.Fatalf("credentials should be an object, got %T", payload["credentials"])
	}
	if credentials["client_id"] != "client-one" || credentials["client_secret"] != "secret-one" {
		t.Errorf("credentials should carry both halves of the pair, got %v", credentials)
	}
}

// SELECT re-validates against Databricks whenever primary_workspace_url,
// warehouse_id or credentials are present in the patch — present, not changed.
// A rename must therefore leave all three out, or renaming a connection would
// depend on Databricks being reachable.
func TestDatabricksUpdatePayloadOmitsUnchangedAccessFields(t *testing.T) {
	state := databricksConnection()
	plan := databricksConnection()
	plan.Name = types.StringValue("Acme Production (EU)")

	payload := marshal(t, buildDatabricksConnectionUpdate(&plan, &state))

	if payload["name"] != "Acme Production (EU)" {
		t.Errorf("the changed name should be sent, got %v", payload["name"])
	}
	for _, field := range []string{"primary_workspace_url", "warehouse_id", "credentials", "sync_enabled", "query_sanitization_enabled"} {
		if _, present := payload[field]; present {
			t.Errorf("%s did not change and should have been omitted, got %v", field, payload[field])
		}
	}
}

func TestDatabricksUpdatePayloadSendsChangedFields(t *testing.T) {
	state := databricksConnection()
	plan := databricksConnection()
	plan.WarehouseId = types.StringValue("newwarehouse")
	plan.SyncEnabled = types.BoolValue(false)

	payload := marshal(t, buildDatabricksConnectionUpdate(&plan, &state))

	if payload["warehouse_id"] != "newwarehouse" {
		t.Errorf("the changed warehouse should be sent, got %v", payload["warehouse_id"])
	}
	// A pointer to false is still a value: omitempty must not swallow it.
	if payload["sync_enabled"] != false {
		t.Errorf("sync_enabled should be sent as false, got %v", payload["sync_enabled"])
	}
	if _, present := payload["name"]; present {
		t.Errorf("name did not change and should have been omitted")
	}
}

// Rotating credentials moves both halves together, and only when the
// configuration names a new pair — an unrelated apply must not rewrite the
// stored secret.
func TestDatabricksUpdatePayloadRotatesCredentialsTogether(t *testing.T) {
	state := databricksConnection()
	plan := databricksConnection()
	plan.Credentials = databricksCredentials("client-one", "secret-two")

	payload := marshal(t, buildDatabricksConnectionUpdate(&plan, &state))

	credentials, ok := payload["credentials"].(map[string]any)
	if !ok {
		t.Fatalf("credentials should be sent when they change, got %T", payload["credentials"])
	}
	if credentials["client_id"] != "client-one" || credentials["client_secret"] != "secret-two" {
		t.Errorf("both credential fields should be sent, got %v", credentials)
	}
}

// The API rejects null for every patchable field, so a patch that clears nothing
// must contain nothing rather than an explicit null.
func TestDatabricksUpdatePayloadNeverSendsNull(t *testing.T) {
	state := databricksConnection()
	plan := databricksConnection()

	payload := marshal(t, buildDatabricksConnectionUpdate(&plan, &state))

	if len(payload) != 0 {
		t.Errorf("a patch with no changes should be empty, got %v", payload)
	}
}

// The API returns no credentials at all for this resource, so the configuration
// is their only record and a response must not overwrite them.
func TestDatabricksApplyResponsePreservesCredentials(t *testing.T) {
	source := databricksConnection()
	model := databricksConnection()

	metastore := "aws:us-east-1:1234"
	syncTime := "2026-08-27T00:00:00Z"
	response := &databricksConnectionResponse{
		Id:                     "2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01",
		Etag:                   `"def456"`,
		Name:                   "Acme Production",
		ConnectionId:           "9c9c9c9c-0000-1111-2222-333333333333",
		DatabricksAccountId:    "11111111-2222-3333-4444-555555555555",
		PrimaryWorkspaceUrl:    "https://acme.cloud.databricks.com",
		WarehouseId:            "abc123def456",
		Metastore:              &metastore,
		WorkspaceIds:           []string{"111", "222"},
		SyncEnabled:            true,
		LastSuccessfulSyncTime: &syncTime,
		CreateTime:             "2026-08-01T00:00:00Z",
		UpdateTime:             "2026-08-27T00:00:00Z",
	}

	diags := applyDatabricksConnectionResponse(context.Background(), &model, &source, response)
	if diags.HasError() {
		t.Fatalf("applying the response: %v", diags)
	}

	if !model.Credentials.Equal(source.Credentials) {
		t.Errorf("credentials should be carried over from the configuration, got %v", model.Credentials)
	}
	if model.Etag.ValueString() != `"def456"` {
		t.Errorf("the response's ETag should land in state, got %v", model.Etag)
	}
	if model.Metastore.ValueString() != metastore {
		t.Errorf("metastore should be taken from the response, got %v", model.Metastore)
	}
	if model.ConnectionId.ValueString() != "9c9c9c9c-0000-1111-2222-333333333333" {
		t.Errorf("connection_id should be taken from the response, got %v", model.ConnectionId)
	}
	if length := len(model.WorkspaceIds.Elements()); length != 2 {
		t.Errorf("workspace_ids should hold both discovered workspaces, got %d", length)
	}
	// query_sanitization_enabled is absent from the response above, so it decodes
	// as false — the API's value, not the plan's, is what state records.
	if model.QuerySanitizationEnabled.ValueBool() {
		t.Errorf("query_sanitization_enabled should follow the response")
	}
}

// A failing check is the most actionable thing the API can tell us, so it should
// reach the user ahead of the status code, with the documentation link it
// carries.
func TestDatabricksValidationReportBecomesADiagnostic(t *testing.T) {
	body := `{
		"title": "Validation failed",
		"status": 422,
		"detail": "SELECT could not reach the workspace.",
		"code": "validation_failed",
		"details": [{"field": "body", "issue": "check_failed:connectivity"}],
		"validation_report": {
			"success": false,
			"checks": [
				{"id": "connectivity", "label": "Connectivity", "status": "failed",
				 "message": "The service principal could not authenticate to the workspace.",
				 "docs_link": "https://select.dev/docs/databricks"},
				{"id": "metastore", "label": "Metastore", "status": "skipped",
				 "message": "Not run because an earlier check failed."}
			]
		}
	}`

	detail := databricksConnectionAPIDiagnostic("add the Databricks connection", newAPIError(422, body)).Detail()

	if !strings.Contains(detail, "could not authenticate") {
		t.Errorf("diagnostic should name the failing check's remedy, got: %s", detail)
	}
	if !strings.Contains(detail, "https://select.dev/docs/databricks") {
		t.Errorf("diagnostic should include the check's docs link, got: %s", detail)
	}
	if !strings.Contains(detail, "Not run because an earlier check failed.") {
		t.Errorf("diagnostic should include skipped checks, got: %s", detail)
	}
}

// The two situations behind a 409 have nothing in common, so each has to name
// its own remedy rather than share one message keyed on the status.
func TestDatabricksConflictDiagnosticsBranchOnIssue(t *testing.T) {
	alreadyConnected := databricksConnectionAPIDiagnostic("add the Databricks connection", newAPIError(409,
		`{"detail":"Databricks account 'acme' already has a connection in 'aws:us-east-1'.","code":"conflict",
		  "details":[{"field":"databricks_account_id","issue":"region_already_connected"}]}`))
	if !strings.Contains(alreadyConnected.Detail(), "terraform import") {
		t.Errorf("an already-connected account should point at import, got: %s", alreadyConnected.Detail())
	}

	needsCredentials := databricksConnectionAPIDiagnostic("update the Databricks connection", newAPIError(409,
		`{"detail":"This connection has no credentials configured.","code":"conflict",
		  "details":[{"field":"credentials","issue":"required"}]}`))
	if !strings.Contains(needsCredentials.Detail(), "`credentials` block") {
		t.Errorf("a credentials conflict should point at the credentials block, got: %s", needsCredentials.Detail())
	}
}

// The ETag contract and the API key's scopes are the two failures a user is
// least likely to diagnose unaided.
func TestDatabricksPreconditionAndScopeDiagnostics(t *testing.T) {
	stale := databricksConnectionAPIDiagnostic("update the Databricks connection",
		newAPIError(412, `{"detail":"The If-Match header does not match the resource's current ETag.","code":"precondition_failed"}`))
	if !strings.Contains(stale.Detail(), "-refresh-only") {
		t.Errorf("a stale ETag should tell the user how to recover, got: %s", stale.Detail())
	}

	missing := databricksConnectionAPIDiagnostic("delete the Databricks connection",
		newAPIError(428, `{"detail":"This is a configurable resource; If-Match is required for writes.","code":"precondition_required"}`))
	if !strings.Contains(missing.Detail(), "-refresh-only") {
		t.Errorf("a missing ETag should tell the user how to recover, got: %s", missing.Detail())
	}

	forbidden := databricksConnectionAPIDiagnostic("add the Databricks connection",
		newAPIError(403, `{"detail":"This caller lacks the databricks_connections:write scope.","code":"forbidden"}`))
	if !strings.Contains(forbidden.Detail(), "databricks_connections:write") {
		t.Errorf("a scope failure should name the scopes needed, got: %s", forbidden.Detail())
	}
}

// The API has no pattern for primary_workspace_url, so a value missing its
// scheme would otherwise only fail after SELECT had tried to connect.
func TestDatabricksWorkspaceURLMustBeAbsolute(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		url      types.String
		rejected bool
	}{
		{name: "https URL", url: types.StringValue("https://acme.cloud.databricks.com")},
		{name: "no scheme", url: types.StringValue("acme.cloud.databricks.com"), rejected: true},
		{name: "scheme but no host", url: types.StringValue("https://"), rejected: true},
		{name: "unknown until apply", url: types.StringUnknown()},
		{name: "null", url: types.StringNull()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			diags := validateWorkspaceURL(testCase.url)
			if diags.HasError() != testCase.rejected {
				t.Errorf("rejected = %t, want %t (%v)", diags.HasError(), testCase.rejected, diags)
			}
		})
	}
}
