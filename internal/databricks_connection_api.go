// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"terraform-provider-select/internal/provider/resource_databricks_connection"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Databricks connections live on the v2 API. See v2_api.go for the conventions
// every resource on that surface shares.
const databricksConnectionsEndpoint = "/v2/databricks-connections"

func databricksConnectionEndpoint(id string) string {
	return fmt.Sprintf("%s/%s", databricksConnectionsEndpoint, id)
}

// databricksConnectionErrors words the failures every v2 resource can hit.
var databricksConnectionErrors = v2ErrorFormat{
	Noun:       "Databricks Connection",
	Subject:    "the connection",
	Object:     "the Databricks connection",
	Plural:     "Databricks connections",
	ReadScope:  "databricks_connections:read",
	WriteScope: "databricks_connections:write",
}

// The `issue` values the API sends on a 409, which is the only status here that
// covers more than one situation.
const (
	issueMetastoreAlreadyConnected = "metastore_already_connected"
	issueRegionAlreadyConnected    = "region_already_connected"
)

// databricksCredentialsPayload mirrors the API's DatabricksCredentials. Both
// fields are required: the API has no way to change one without the other, so a
// rotation moves them together.
type databricksCredentialsPayload struct {
	ClientId     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// databricksConnectionCreatePayload mirrors DatabricksConnectionCreate. The two
// booleans have schema defaults, so the plan always carries a value for them and
// there is nothing to omit.
type databricksConnectionCreatePayload struct {
	Name                     string                       `json:"name"`
	DatabricksAccountId      string                       `json:"databricks_account_id"`
	PrimaryWorkspaceUrl      string                       `json:"primary_workspace_url"`
	WarehouseId              string                       `json:"warehouse_id"`
	Credentials              databricksCredentialsPayload `json:"credentials"`
	SyncEnabled              bool                         `json:"sync_enabled"`
	QuerySanitizationEnabled bool                         `json:"query_sanitization_enabled"`
}

// databricksConnectionUpdatePayload mirrors DatabricksConnectionUpdate, a JSON
// Merge Patch body where an omitted field is left unchanged.
//
// Every field is omitted when it has not changed, which is the opposite of the
// Snowflake account resource. Two things make it both safe and necessary here:
//
//   - Nothing on this resource can be cleared. The API rejects null for all six
//     patchable fields, so there is no removal that has to reach it as an
//     explicit null — the reason the Snowflake payload sends every field.
//   - SELECT re-validates against Databricks when primary_workspace_url,
//     warehouse_id or credentials are *present*, not when they change. Sending
//     them unconditionally would turn renaming a connection into a live round
//     trip to Databricks that a Databricks outage could fail. Omitting
//     credentials additionally avoids rewriting the stored secret on an apply
//     that has nothing to do with it.
type databricksConnectionUpdatePayload struct {
	Name                     *string                       `json:"name,omitempty"`
	PrimaryWorkspaceUrl      *string                       `json:"primary_workspace_url,omitempty"`
	WarehouseId              *string                       `json:"warehouse_id,omitempty"`
	Credentials              *databricksCredentialsPayload `json:"credentials,omitempty"`
	SyncEnabled              *bool                         `json:"sync_enabled,omitempty"`
	QuerySanitizationEnabled *bool                         `json:"query_sanitization_enabled,omitempty"`
}

// databricksConnectionResponse mirrors DatabricksConnection. It carries no
// credentials: the API stores the secret in a secret store and returns neither
// half of the pair on this resource.
type databricksConnectionResponse struct {
	Id                       string   `json:"id"`
	Etag                     string   `json:"etag"`
	Name                     string   `json:"name"`
	ConnectionId             string   `json:"connection_id"`
	DatabricksAccountId      string   `json:"databricks_account_id"`
	PrimaryWorkspaceUrl      string   `json:"primary_workspace_url"`
	WarehouseId              string   `json:"warehouse_id"`
	Metastore                *string  `json:"metastore"`
	WorkspaceIds             []string `json:"workspace_ids"`
	SyncEnabled              bool     `json:"sync_enabled"`
	QuerySanitizationEnabled bool     `json:"query_sanitization_enabled"`
	AddedByEmail             *string  `json:"added_by_email"`
	LastSuccessfulSyncTime   *string  `json:"last_successful_sync_time"`
	CreateTime               string   `json:"create_time"`
	UpdateTime               string   `json:"update_time"`
}

func buildDatabricksCredentials(credentials resource_databricks_connection.CredentialsValue) databricksCredentialsPayload {
	return databricksCredentialsPayload{
		ClientId:     credentials.ClientId.ValueString(),
		ClientSecret: credentials.ClientSecret.ValueString(),
	}
}

func buildDatabricksConnectionCreate(plan *resource_databricks_connection.DatabricksConnectionModel) *databricksConnectionCreatePayload {
	return &databricksConnectionCreatePayload{
		Name:                     plan.Name.ValueString(),
		DatabricksAccountId:      plan.DatabricksAccountId.ValueString(),
		PrimaryWorkspaceUrl:      plan.PrimaryWorkspaceUrl.ValueString(),
		WarehouseId:              plan.WarehouseId.ValueString(),
		Credentials:              buildDatabricksCredentials(plan.Credentials),
		SyncEnabled:              plan.SyncEnabled.ValueBool(),
		QuerySanitizationEnabled: plan.QuerySanitizationEnabled.ValueBool(),
	}
}

// buildDatabricksConnectionUpdate carries only the fields whose configured value
// differs from what state records. See databricksConnectionUpdatePayload.
func buildDatabricksConnectionUpdate(plan, state *resource_databricks_connection.DatabricksConnectionModel) *databricksConnectionUpdatePayload {
	payload := &databricksConnectionUpdatePayload{
		Name:                     changedString(plan.Name, state.Name),
		PrimaryWorkspaceUrl:      changedString(plan.PrimaryWorkspaceUrl, state.PrimaryWorkspaceUrl),
		WarehouseId:              changedString(plan.WarehouseId, state.WarehouseId),
		SyncEnabled:              changedBool(plan.SyncEnabled, state.SyncEnabled),
		QuerySanitizationEnabled: changedBool(plan.QuerySanitizationEnabled, state.QuerySanitizationEnabled),
	}

	if !plan.Credentials.Equal(state.Credentials) {
		credentials := buildDatabricksCredentials(plan.Credentials)
		payload.Credentials = &credentials
	}

	return payload
}

// applyDatabricksConnectionResponse writes an API response onto the model. The
// credentials block is carried over from source rather than the response, since
// the API returns neither the client ID nor the secret on this resource and
// Terraform's own configuration is the only record of them.
func applyDatabricksConnectionResponse(
	ctx context.Context,
	model *resource_databricks_connection.DatabricksConnectionModel,
	source *resource_databricks_connection.DatabricksConnectionModel,
	connection *databricksConnectionResponse,
) diag.Diagnostics {
	var diags diag.Diagnostics

	workspaceIds, listDiags := stringListValue(ctx, &connection.WorkspaceIds)
	diags.Append(listDiags...)
	if diags.HasError() {
		return diags
	}

	model.Id = types.StringValue(connection.Id)
	model.Etag = types.StringValue(connection.Etag)
	model.Name = types.StringValue(connection.Name)
	model.ConnectionId = types.StringValue(connection.ConnectionId)
	model.DatabricksAccountId = types.StringValue(connection.DatabricksAccountId)
	model.PrimaryWorkspaceUrl = types.StringValue(connection.PrimaryWorkspaceUrl)
	model.WarehouseId = types.StringValue(connection.WarehouseId)
	model.Metastore = stringValue(connection.Metastore)
	model.WorkspaceIds = workspaceIds
	model.SyncEnabled = types.BoolValue(connection.SyncEnabled)
	model.QuerySanitizationEnabled = types.BoolValue(connection.QuerySanitizationEnabled)
	model.AddedByEmail = stringValue(connection.AddedByEmail)
	model.LastSuccessfulSyncTime = stringValue(connection.LastSuccessfulSyncTime)
	model.CreateTime = types.StringValue(connection.CreateTime)
	model.UpdateTime = types.StringValue(connection.UpdateTime)

	model.Credentials = source.Credentials

	return diags
}

// databricksConnectionAPIDiagnostic turns an API failure into the most useful
// diagnostic available for it: the checks that did not pass when SELECT ran them
// against Databricks, an explanation of the ETag contract for a precondition
// failure, and the problem document's detail otherwise.
func databricksConnectionAPIDiagnostic(operation string, apiErr *apiError) diag.Diagnostic {
	return databricksConnectionErrors.diagnostic(operation, apiErr, databricksConnectionSpecificDiagnostic)
}

// databricksConnectionSpecificDiagnostic is this resource's own opinion about an
// API failure: a 409 here is either "this account and region, or this
// metastore, is already connected" or "this connection has no usable
// credentials". The remedies have nothing in common, so branch on the issue
// rather than guessing from the status.
func databricksConnectionSpecificDiagnostic(operation string, apiErr *apiError) diag.Diagnostic {
	if apiErr.StatusCode != http.StatusConflict {
		return nil
	}
	switch apiErr.issue() {
	case issueMetastoreAlreadyConnected, issueRegionAlreadyConnected:
		return diag.NewErrorDiagnostic(
			"Databricks Connection Already Exists",
			fmt.Sprintf("SELECT could not %s because this Databricks account and region are already "+
				"connected. SELECT supports one connection per account per region; bring the existing "+
				"one under Terraform with `terraform import` instead of adding it again.\n\n%s",
				operation, apiErr.Detail),
		)
	default:
		return diag.NewErrorDiagnostic(
			"Databricks Credentials Required",
			fmt.Sprintf("SELECT could not %s because the connection has no usable credentials stored. "+
				"Set the `credentials` block so this apply supplies them.\n\n%s",
				operation, apiErr.Detail),
		)
	}
}
