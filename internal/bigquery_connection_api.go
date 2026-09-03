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
//
// The API also supports is_doit, which reads a project's spend from DoiT's own
// billing data instead of an export in the customer's project. This resource
// has no knowledge of it: adding one needs a signed-in user's credential this
// provider never holds, and in practice neither DoiT staff nor customers
// manage one through Terraform. bigquery_dataset_id and billing_account_id are
// therefore always required and settable here, matching the create request's
// non-DoiT shape; a DoiT-managed connection is out of this resource's scope
// entirely.
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
type bigQueryConnectionCreatePayload struct {
	Name                     string  `json:"name"`
	GcpProjectId             string  `json:"gcp_project_id"`
	BigqueryDatasetId        *string `json:"bigquery_dataset_id,omitempty"`
	BillingAccountId         *string `json:"billing_account_id,omitempty"`
	ServiceAccount           string  `json:"service_account"`
	SyncEnabled              bool    `json:"sync_enabled"`
	QuerySanitizationEnabled bool    `json:"query_sanitization_enabled"`
}

// bigQueryConnectionUpdatePayload mirrors BigQueryConnectionUpdate, a JSON
// Merge Patch body where an omitted field is left unchanged.
//
// Every field is omitted when it has not changed, the same shape
// databricksConnectionUpdatePayload uses and for the same two reasons:
//
//   - Nothing this resource can create is ever clearable. The API rejects null
//     for name, gcp_project_id, service_account, sync_enabled and
//     query_sanitization_enabled directly; bigquery_dataset_id and
//     billing_account_id are only clearable on a DoiT-managed connection,
//     which this resource never creates or knowingly manages.
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

// bigQueryConnectionResponse mirrors BigQueryConnection. is_doit and
// doit_billing_status are not read: this resource never sets is_doit, so
// they carry no information a create or update of a resource managed here
// needs to act on.
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
	ConnectionId             string   `json:"connection_id"`
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
	model.ConnectionId = types.StringValue(connection.ConnectionId)
	model.SyncEnabled = types.BoolValue(connection.SyncEnabled)
	model.QuerySanitizationEnabled = types.BoolValue(connection.QuerySanitizationEnabled)
	model.AddedByEmail = stringValue(connection.AddedByEmail)
	model.LastSuccessfulSyncTime = stringValue(connection.LastSuccessfulSyncTime)
	model.CreateTime = types.StringValue(connection.CreateTime)
	model.UpdateTime = types.StringValue(connection.UpdateTime)
	// Never read from the API (see bigQueryConnectionResponse's own comment) and
	// always null for a connection managed by this resource, per the schema's own
	// description — but a computed attribute still has to resolve to something
	// after apply, or Terraform's post-apply consistency check fails the whole
	// operation, exactly as it did before this line existed.
	model.DoitBillingStatus = types.StringNull()

	return diags
}

// bigQueryConnectionAPIDiagnostic turns an API failure into the most useful
// diagnostic available for it: the checks that did not pass when SELECT ran
// them against BigQuery, an explanation of the ETag contract for a
// precondition failure, and the problem document's detail otherwise.
func bigQueryConnectionAPIDiagnostic(operation string, apiErr *apiError) diag.Diagnostic {
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
		return bigQueryConnectionErrors.forbidden(operation, apiErr)
	default:
		return bigQueryConnectionErrors.unexpected(operation, apiErr)
	}
}
