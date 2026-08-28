// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"terraform-provider-select/internal/provider/resource_bigquery_connection"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// BigQuery connections live on the v2 API. See v2_api.go for the conventions
// every resource on that surface shares.
const bigQueryConnectionsEndpoint = "/v2/bigquery-connections"

func bigQueryConnectionEndpoint(id string) string {
	return fmt.Sprintf("%s/%s", bigQueryConnectionsEndpoint, id)
}

// bigQueryConnectionErrors words the failures every v2 resource can hit.
var bigQueryConnectionErrors = v2ErrorFormat{
	Noun:       "BigQuery Connection",
	Subject:    "the connection",
	Plural:     "BigQuery connections",
	ReadScope:  "bigquery_connections:read",
	WriteScope: "bigquery_connections:write",
}

// bigQueryConnectionCreatePayload mirrors BigQueryConnectionCreate.
// bigquery_dataset_id and billing_account_id are omitted rather than sent as
// null: the API requires both when is_doit is false and rejects either being
// present at all when it is true, so a DoiT create must not send them.
type bigQueryConnectionCreatePayload struct {
	Name                     string  `json:"name"`
	GcpProjectId             string  `json:"gcp_project_id"`
	BigqueryDatasetId        *string `json:"bigquery_dataset_id,omitempty"`
	BillingAccountId         *string `json:"billing_account_id,omitempty"`
	ServiceAccount           string  `json:"service_account"`
	IsDoit                   bool    `json:"is_doit"`
	SyncEnabled              bool    `json:"sync_enabled"`
	QuerySanitizationEnabled bool    `json:"query_sanitization_enabled"`
}

// bigQueryConnectionUpdatePayload mirrors BigQueryConnectionUpdate, a JSON
// Merge Patch body where an omitted field is left unchanged.
//
// Every field is omitted when it has not changed, the same shape
// databricksConnectionUpdatePayload uses and for the same two reasons:
//
//   - Nothing on this resource can be cleared. The API rejects null for name,
//     gcp_project_id, service_account, sync_enabled and
//     query_sanitization_enabled directly, and for bigquery_dataset_id and
//     billing_account_id through a check against the connection's stored
//     is_doit — so there is no removal that has to reach the API as an
//     explicit null.
//   - SELECT re-validates against BigQuery when gcp_project_id,
//     bigquery_dataset_id, billing_account_id or service_account are
//     *present* in the patch, not when they change. Sending them
//     unconditionally would turn renaming a connection into a live round trip
//     to BigQuery that a GCP outage could fail.
type bigQueryConnectionUpdatePayload struct {
	Name                     *string `json:"name,omitempty"`
	GcpProjectId             *string `json:"gcp_project_id,omitempty"`
	BigqueryDatasetId        *string `json:"bigquery_dataset_id,omitempty"`
	BillingAccountId         *string `json:"billing_account_id,omitempty"`
	ServiceAccount           *string `json:"service_account,omitempty"`
	SyncEnabled              *bool   `json:"sync_enabled,omitempty"`
	QuerySanitizationEnabled *bool   `json:"query_sanitization_enabled,omitempty"`
}

// bigQueryConnectionResponse mirrors BigQueryConnection.
type bigQueryConnectionResponse struct {
	Id                       string   `json:"id"`
	Etag                     string   `json:"etag"`
	Name                     string   `json:"name"`
	GcpProjectId             string   `json:"gcp_project_id"`
	BigqueryDatasetId        *string  `json:"bigquery_dataset_id"`
	BillingAccountId         *string  `json:"billing_account_id"`
	ServiceAccount           *string  `json:"service_account"`
	GcpOrganizationId        *string  `json:"gcp_organization_id"`
	GcpOrganizationName      *string  `json:"gcp_organization_name"`
	Regions                  []string `json:"regions"`
	IsDoit                   bool     `json:"is_doit"`
	DoitBillingStatus        *string  `json:"doit_billing_status"`
	SyncEnabled              bool     `json:"sync_enabled"`
	QuerySanitizationEnabled bool     `json:"query_sanitization_enabled"`
	AddedByEmail             *string  `json:"added_by_email"`
	LastSuccessfulSyncTime   *string  `json:"last_successful_sync_time"`
	CreateTime               string   `json:"create_time"`
	UpdateTime               string   `json:"update_time"`
}

func buildBigQueryConnectionCreate(plan *resource_bigquery_connection.BigqueryConnectionModel) *bigQueryConnectionCreatePayload {
	return &bigQueryConnectionCreatePayload{
		Name:                     plan.Name.ValueString(),
		GcpProjectId:             plan.GcpProjectId.ValueString(),
		BigqueryDatasetId:        stringPointer(plan.BigqueryDatasetId),
		BillingAccountId:         stringPointer(plan.BillingAccountId),
		ServiceAccount:           plan.ServiceAccount.ValueString(),
		IsDoit:                   plan.IsDoit.ValueBool(),
		SyncEnabled:              plan.SyncEnabled.ValueBool(),
		QuerySanitizationEnabled: plan.QuerySanitizationEnabled.ValueBool(),
	}
}

// buildBigQueryConnectionUpdate carries only the fields whose configured value
// differs from what state records. See bigQueryConnectionUpdatePayload.
func buildBigQueryConnectionUpdate(plan, state *resource_bigquery_connection.BigqueryConnectionModel) *bigQueryConnectionUpdatePayload {
	payload := &bigQueryConnectionUpdatePayload{}

	if !plan.Name.Equal(state.Name) {
		payload.Name = stringPointer(plan.Name)
	}
	if !plan.GcpProjectId.Equal(state.GcpProjectId) {
		payload.GcpProjectId = stringPointer(plan.GcpProjectId)
	}
	if !plan.BigqueryDatasetId.Equal(state.BigqueryDatasetId) {
		payload.BigqueryDatasetId = stringPointer(plan.BigqueryDatasetId)
	}
	if !plan.BillingAccountId.Equal(state.BillingAccountId) {
		payload.BillingAccountId = stringPointer(plan.BillingAccountId)
	}
	if !plan.ServiceAccount.Equal(state.ServiceAccount) {
		payload.ServiceAccount = stringPointer(plan.ServiceAccount)
	}
	if !plan.SyncEnabled.Equal(state.SyncEnabled) {
		payload.SyncEnabled = boolPointer(plan.SyncEnabled)
	}
	if !plan.QuerySanitizationEnabled.Equal(state.QuerySanitizationEnabled) {
		payload.QuerySanitizationEnabled = boolPointer(plan.QuerySanitizationEnabled)
	}

	return payload
}

// applyBigQueryConnectionResponse writes an API response onto the model.
// billing_account_id goes through preserveEquivalentFold because the API
// uppercases it, so a lowercase configured value would otherwise read as drift
// on every plan.
func applyBigQueryConnectionResponse(
	ctx context.Context,
	model *resource_bigquery_connection.BigqueryConnectionModel,
	source *resource_bigquery_connection.BigqueryConnectionModel,
	connection *bigQueryConnectionResponse,
) diag.Diagnostics {
	var diags diag.Diagnostics

	regions, listDiags := stringListValue(ctx, &connection.Regions)
	diags.Append(listDiags...)
	if diags.HasError() {
		return diags
	}

	model.Id = types.StringValue(connection.Id)
	model.Etag = types.StringValue(connection.Etag)
	model.Name = types.StringValue(connection.Name)
	model.GcpProjectId = types.StringValue(connection.GcpProjectId)
	model.BigqueryDatasetId = stringValue(connection.BigqueryDatasetId)
	model.BillingAccountId = preserveEquivalentFold(source.BillingAccountId, connection.BillingAccountId)
	model.ServiceAccount = stringValue(connection.ServiceAccount)
	model.GcpOrganizationId = stringValue(connection.GcpOrganizationId)
	model.GcpOrganizationName = stringValue(connection.GcpOrganizationName)
	model.Regions = regions
	model.IsDoit = types.BoolValue(connection.IsDoit)
	model.DoitBillingStatus = stringValue(connection.DoitBillingStatus)
	model.SyncEnabled = types.BoolValue(connection.SyncEnabled)
	model.QuerySanitizationEnabled = types.BoolValue(connection.QuerySanitizationEnabled)
	model.AddedByEmail = stringValue(connection.AddedByEmail)
	model.LastSuccessfulSyncTime = stringValue(connection.LastSuccessfulSyncTime)
	model.CreateTime = types.StringValue(connection.CreateTime)
	model.UpdateTime = types.StringValue(connection.UpdateTime)

	return diags
}

// bigQueryConnectionAPIDiagnostic turns an API failure into the most useful
// diagnostic available for it. isDoitCreate is true only when the failure came
// from creating a connection with is_doit set: the API answers that case with
// the same 403/forbidden code as a missing scope, so which explanation applies
// has to come from what this request was, not from anything in the response.
func bigQueryConnectionAPIDiagnostic(operation string, isDoitCreate bool, apiErr *apiError) diag.Diagnostic {
	if diagnostic := v2ValidationDiagnostic("BigQuery Connection Validation Failed", operation, apiErr); diagnostic != nil {
		return diagnostic
	}

	switch apiErr.StatusCode {
	case http.StatusPreconditionFailed:
		return bigQueryConnectionErrors.preconditionFailed(operation, apiErr)
	case http.StatusPreconditionRequired:
		return bigQueryConnectionErrors.preconditionRequired(operation, apiErr)
	case http.StatusConflict:
		return diag.NewErrorDiagnostic(
			"BigQuery Connection Already Exists",
			fmt.Sprintf("SELECT could not %s because this GCP project is already connected. "+
				"SELECT supports one connection per project; bring the existing one under "+
				"Terraform with `terraform import` instead of adding it again.\n\n%s",
				operation, apiErr.Detail),
		)
	case http.StatusForbidden:
		if isDoitCreate {
			return diag.NewErrorDiagnostic(
				"DoiT-Managed Connection Requires a User Credential",
				fmt.Sprintf("SELECT could not %s because a DoiT-managed connection (is_doit = true) "+
					"can only be added by a signed-in user, not an API key. Add it in the SELECT UI, "+
					"then bring it under Terraform with `terraform import`.\n\n%s",
					operation, apiErr.Detail),
			)
		}
		return bigQueryConnectionErrors.forbidden(operation, apiErr)
	default:
		return bigQueryConnectionErrors.unexpected(operation, apiErr)
	}
}
