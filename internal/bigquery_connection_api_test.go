// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"
	"testing"

	"terraform-provider-select/internal/provider/resource_bigquery_connection"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func bigQueryConnection() resource_bigquery_connection.BigqueryConnectionModel {
	return resource_bigquery_connection.BigqueryConnectionModel{
		Id:                       types.StringValue("2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01"),
		Etag:                     types.StringValue(`"abc123"`),
		Name:                     types.StringValue("Acme Production"),
		GcpProjectId:             types.StringValue("acme-prod"),
		BigqueryDatasetId:        types.StringValue("billing_export"),
		BillingAccountId:         types.StringValue("0123ab-4567cd-89ef01"),
		ServiceAccount:           types.StringValue("select@acme-prod.iam.gserviceaccount.com"),
		IsDoit:                   types.BoolValue(false),
		SyncEnabled:              types.BoolValue(true),
		QuerySanitizationEnabled: types.BoolValue(false),
	}
}

// A create body has to carry every field the API requires, including the three
// booleans, whose schema defaults mean the plan always has a value for them.
func TestBigQueryCreatePayloadCarriesEveryRequiredField(t *testing.T) {
	plan := bigQueryConnection()

	payload := marshal(t, buildBigQueryConnectionCreate(&plan))

	for field, expected := range map[string]any{
		"name":                       "Acme Production",
		"gcp_project_id":             "acme-prod",
		"bigquery_dataset_id":        "billing_export",
		"billing_account_id":         "0123ab-4567cd-89ef01",
		"service_account":            "select@acme-prod.iam.gserviceaccount.com",
		"is_doit":                    false,
		"sync_enabled":               true,
		"query_sanitization_enabled": false,
	} {
		if payload[field] != expected {
			t.Errorf("%s should be %v, got %v", field, expected, payload[field])
		}
	}
}

// A DoiT-managed connection reads DoiT's own billing data, so the API rejects
// bigquery_dataset_id and billing_account_id even being present, not just set.
func TestBigQueryCreatePayloadOmitsDatasetAndBillingWhenDoit(t *testing.T) {
	plan := bigQueryConnection()
	plan.IsDoit = types.BoolValue(true)
	plan.BigqueryDatasetId = types.StringNull()
	plan.BillingAccountId = types.StringNull()

	payload := marshal(t, buildBigQueryConnectionCreate(&plan))

	for _, field := range []string{"bigquery_dataset_id", "billing_account_id"} {
		if _, present := payload[field]; present {
			t.Errorf("%s should be omitted on a DoiT-managed create, got %v", field, payload[field])
		}
	}
	if payload["is_doit"] != true {
		t.Errorf("is_doit should be sent as true, got %v", payload["is_doit"])
	}
}

// SELECT re-validates against BigQuery whenever gcp_project_id,
// bigquery_dataset_id, billing_account_id or service_account are present in the
// patch — present, not changed. A rename must therefore leave all four out, or
// renaming a connection would depend on BigQuery being reachable.
func TestBigQueryUpdatePayloadOmitsUnchangedAccessFields(t *testing.T) {
	state := bigQueryConnection()
	plan := bigQueryConnection()
	plan.Name = types.StringValue("Acme Production (EU)")

	payload := marshal(t, buildBigQueryConnectionUpdate(&plan, &state))

	if payload["name"] != "Acme Production (EU)" {
		t.Errorf("the changed name should be sent, got %v", payload["name"])
	}
	for _, field := range []string{
		"gcp_project_id", "bigquery_dataset_id", "billing_account_id", "service_account",
		"sync_enabled", "query_sanitization_enabled",
	} {
		if _, present := payload[field]; present {
			t.Errorf("%s did not change and should have been omitted, got %v", field, payload[field])
		}
	}
}

func TestBigQueryUpdatePayloadSendsChangedFields(t *testing.T) {
	state := bigQueryConnection()
	plan := bigQueryConnection()
	plan.ServiceAccount = types.StringValue("new-sa@acme-prod.iam.gserviceaccount.com")
	plan.SyncEnabled = types.BoolValue(false)

	payload := marshal(t, buildBigQueryConnectionUpdate(&plan, &state))

	if payload["service_account"] != "new-sa@acme-prod.iam.gserviceaccount.com" {
		t.Errorf("the changed service account should be sent, got %v", payload["service_account"])
	}
	// A pointer to false is still a value: omitempty must not swallow it.
	if payload["sync_enabled"] != false {
		t.Errorf("sync_enabled should be sent as false, got %v", payload["sync_enabled"])
	}
	if _, present := payload["name"]; present {
		t.Errorf("name did not change and should have been omitted")
	}
}

// The API rejects null for every patchable field — directly for most of them,
// and for bigquery_dataset_id/billing_account_id through a check against the
// connection's stored is_doit — so a patch that clears nothing must contain
// nothing rather than an explicit null.
func TestBigQueryUpdatePayloadNeverSendsNull(t *testing.T) {
	state := bigQueryConnection()
	plan := bigQueryConnection()

	payload := marshal(t, buildBigQueryConnectionUpdate(&plan, &state))

	if len(payload) != 0 {
		t.Errorf("a patch with no changes should be empty, got %v", payload)
	}
}

// The API returns no credentials for this resource — a BigQuery connection
// authenticates by service account impersonation, not a stored secret — so
// there is nothing this resource needs to carry over from source the way
// Snowflake and Databricks carry over their credential blocks. What it does
// need to preserve is the configured case of billing_account_id, which the API
// normalizes to upper case on the way in.
func TestBigQueryApplyResponsePreservesBillingAccountFold(t *testing.T) {
	source := bigQueryConnection()
	model := bigQueryConnection()

	orgId := "123456789012"
	orgName := "acme.com"
	syncTime := "2026-08-27T00:00:00Z"
	serviceAccount := "select@acme-prod.iam.gserviceaccount.com"
	response := &bigQueryConnectionResponse{
		Id:                     "2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01",
		Etag:                   `"def456"`,
		Name:                   "Acme Production",
		GcpProjectId:           "acme-prod",
		BigqueryDatasetId:      strPtr("billing_export"),
		BillingAccountId:       strPtr("0123AB-4567CD-89EF01"),
		ServiceAccount:         &serviceAccount,
		GcpOrganizationId:      &orgId,
		GcpOrganizationName:    &orgName,
		Regions:                []string{"us-east1"},
		IsDoit:                 false,
		SyncEnabled:            true,
		LastSuccessfulSyncTime: &syncTime,
		CreateTime:             "2026-08-01T00:00:00Z",
		UpdateTime:             "2026-08-27T00:00:00Z",
	}

	diags := applyBigQueryConnectionResponse(context.Background(), &model, &source, response)
	if diags.HasError() {
		t.Fatalf("applying the response: %v", diags)
	}

	if model.BillingAccountId.ValueString() != "0123ab-4567cd-89ef01" {
		t.Errorf("billing_account_id should keep its configured lower-case spelling, got %v", model.BillingAccountId)
	}
	if model.Etag.ValueString() != `"def456"` {
		t.Errorf("the response's ETag should land in state, got %v", model.Etag)
	}
	if model.GcpOrganizationId.ValueString() != orgId {
		t.Errorf("gcp_organization_id should be taken from the response, got %v", model.GcpOrganizationId)
	}
	if length := len(model.Regions.Elements()); length != 1 {
		t.Errorf("regions should hold the detected region, got %d", length)
	}
	// query_sanitization_enabled is absent from the response above, so it decodes
	// as false — the API's value, not the plan's, is what state records.
	if model.QuerySanitizationEnabled.ValueBool() {
		t.Errorf("query_sanitization_enabled should follow the response")
	}
}

// A configured value that differs from the API's returned casing only must not
// read as drift on a later apply.
func TestBigQueryApplyResponseBillingAccountFoldMismatchUsesResponse(t *testing.T) {
	source := bigQueryConnection()
	source.BillingAccountId = types.StringValue("999999-999999-999999")
	model := bigQueryConnection()

	serviceAccount := "select@acme-prod.iam.gserviceaccount.com"
	response := &bigQueryConnectionResponse{
		Id:               "2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01",
		Etag:             `"def456"`,
		Name:             "Acme Production",
		GcpProjectId:     "acme-prod",
		ServiceAccount:   &serviceAccount,
		Regions:          []string{},
		BillingAccountId: strPtr("0123AB-4567CD-89EF01"),
		SyncEnabled:      true,
		CreateTime:       "2026-08-01T00:00:00Z",
		UpdateTime:       "2026-08-27T00:00:00Z",
	}

	diags := applyBigQueryConnectionResponse(context.Background(), &model, &source, response)
	if diags.HasError() {
		t.Fatalf("applying the response: %v", diags)
	}

	if model.BillingAccountId.ValueString() != "0123AB-4567CD-89EF01" {
		t.Errorf("a genuinely different value should come from the response, got %v", model.BillingAccountId)
	}
}

func strPtr(s string) *string { return &s }

// bigquery_dataset_id and billing_account_id flip between required and
// forbidden depending on is_doit, a rule the generated schema cannot express on
// its own.
func TestBigQueryValidateConfigDatasetAndBillingRules(t *testing.T) {
	present := types.StringValue("value")
	absent := types.StringNull()

	for _, testCase := range []struct {
		name             string
		isDoit           types.Bool
		datasetId        types.String
		billingAccountId types.String
		rejected         bool
	}{
		{name: "not doit, both present", isDoit: types.BoolValue(false), datasetId: present, billingAccountId: present},
		{name: "not doit, dataset missing", isDoit: types.BoolValue(false), datasetId: absent, billingAccountId: present, rejected: true},
		{name: "not doit, billing missing", isDoit: types.BoolValue(false), datasetId: present, billingAccountId: absent, rejected: true},
		{name: "not doit, both missing", isDoit: types.BoolValue(false), datasetId: absent, billingAccountId: absent, rejected: true},
		{name: "doit, both absent", isDoit: types.BoolValue(true), datasetId: absent, billingAccountId: absent},
		{name: "doit, dataset set", isDoit: types.BoolValue(true), datasetId: present, billingAccountId: absent, rejected: true},
		{name: "doit, billing set", isDoit: types.BoolValue(true), datasetId: absent, billingAccountId: present, rejected: true},
		// is_doit defaults to false and that default is applied during plan
		// modification, not config validation, so an omitted is_doit reaches
		// ValidateConfig as null and must be treated as false.
		{name: "is_doit omitted, both present", isDoit: types.BoolNull(), datasetId: present, billingAccountId: present},
		{name: "is_doit omitted, both missing", isDoit: types.BoolNull(), datasetId: absent, billingAccountId: absent, rejected: true},
		{name: "is_doit unknown", isDoit: types.BoolUnknown(), datasetId: absent, billingAccountId: absent},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			diags := validateDatasetAndBillingAccount(testCase.isDoit, testCase.datasetId, testCase.billingAccountId)
			if diags.HasError() != testCase.rejected {
				t.Errorf("rejected = %t, want %t (%v)", diags.HasError(), testCase.rejected, diags)
			}
		})
	}
}

// A failing check is the most actionable thing the API can tell us, so it
// should reach the user ahead of the status code, with the documentation link
// it carries.
func TestBigQueryValidationReportBecomesADiagnostic(t *testing.T) {
	body := `{
		"title": "Validation failed",
		"status": 422,
		"detail": "The connection's configuration did not pass validation.",
		"code": "validation_failed",
		"details": [{"field": "body", "issue": "check_failed:project_access"}],
		"validation_report": {
			"success": false,
			"checks": [
				{"id": "project_access", "label": "Project Access", "status": "failed",
				 "message": "The service account cannot read the project.",
				 "docs_link": "https://select.dev/docs/bigquery"},
				{"id": "billing_export", "label": "Billing Export", "status": "skipped",
				 "message": "Not run because an earlier check failed."}
			]
		}
	}`

	detail := bigQueryConnectionAPIDiagnostic("add the BigQuery connection", false, newAPIError(422, body)).Detail()

	if !strings.Contains(detail, "cannot read the project") {
		t.Errorf("diagnostic should name the failing check's remedy, got: %s", detail)
	}
	if !strings.Contains(detail, "https://select.dev/docs/bigquery") {
		t.Errorf("diagnostic should include the check's docs link, got: %s", detail)
	}
	if !strings.Contains(detail, "Not run because an earlier check failed.") {
		t.Errorf("diagnostic should include skipped checks, got: %s", detail)
	}
}

// The ETag contract and the API key's scopes are the two failures a user is
// least likely to diagnose unaided.
func TestBigQueryPreconditionAndScopeDiagnostics(t *testing.T) {
	stale := bigQueryConnectionAPIDiagnostic("update the BigQuery connection", false,
		newAPIError(412, `{"detail":"The If-Match header does not match the resource's current ETag.","code":"precondition_failed"}`))
	if !strings.Contains(stale.Detail(), "-refresh-only") {
		t.Errorf("a stale ETag should tell the user how to recover, got: %s", stale.Detail())
	}

	missing := bigQueryConnectionAPIDiagnostic("delete the BigQuery connection", false,
		newAPIError(428, `{"detail":"This is a configurable resource; If-Match is required for writes.","code":"precondition_required"}`))
	if !strings.Contains(missing.Detail(), "-refresh-only") {
		t.Errorf("a missing ETag should tell the user how to recover, got: %s", missing.Detail())
	}

	forbidden := bigQueryConnectionAPIDiagnostic("add the BigQuery connection", false,
		newAPIError(403, `{"detail":"This caller lacks the bigquery_connections:write scope.","code":"forbidden"}`))
	if !strings.Contains(forbidden.Detail(), "bigquery_connections:write") {
		t.Errorf("a scope failure should name the scopes needed, got: %s", forbidden.Detail())
	}
}

func TestBigQueryConflictDiagnosticNamesImport(t *testing.T) {
	conflict := bigQueryConnectionAPIDiagnostic("add the BigQuery connection", false,
		newAPIError(409, `{"detail":"GCP project 'acme-prod' is already connected.","code":"conflict"}`))
	if !strings.Contains(conflict.Detail(), "terraform import") {
		t.Errorf("an already-connected project should point at import, got: %s", conflict.Detail())
	}
}

// The API answers a DoiT create rejected for lacking a user credential with the
// same 403/forbidden shape as an ordinary missing-scope failure — there is
// nothing in the response to branch on, so the diagnostic has to come from
// knowing this request was a DoiT create.
func TestBigQueryDoitCreateForbiddenNamesImportPath(t *testing.T) {
	body := `{"detail":"Adding a DoiT-managed connection requires a user credential, not an API key.","code":"forbidden"}`

	doitCreate := bigQueryConnectionAPIDiagnostic("add the BigQuery connection", true, newAPIError(403, body))
	if !strings.Contains(doitCreate.Detail(), "terraform import") {
		t.Errorf("a DoiT create's 403 should point at the UI and import, got: %s", doitCreate.Detail())
	}

	plainForbidden := bigQueryConnectionAPIDiagnostic("add the BigQuery connection", false, newAPIError(403, body))
	if strings.Contains(plainForbidden.Detail(), "terraform import") {
		t.Errorf("a non-DoiT-create 403 should not mention import, got: %s", plainForbidden.Detail())
	}
	if !strings.Contains(plainForbidden.Detail(), "bigquery_connections:write") {
		t.Errorf("a non-DoiT-create 403 should name the scopes, got: %s", plainForbidden.Detail())
	}
}
