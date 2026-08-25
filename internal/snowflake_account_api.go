// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"terraform-provider-select/internal/provider/resource_snowflake_account"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Snowflake accounts live on the v2 API, which is mounted at /v2 alongside the v1
// routes the usage group resources use and scopes requests by the x-tenant-id
// header rather than an organization ID in the path.
const snowflakeAccountsEndpoint = "/v2/snowflake-accounts"

func snowflakeAccountEndpoint(id string) string {
	return fmt.Sprintf("%s/%s", snowflakeAccountsEndpoint, id)
}

// ifMatchHeader carries the account's ETag on a write. The API requires it: a
// write without If-Match is refused with 428, and one carrying a stale value
// with 412, so a change made outside Terraform cannot be silently overwritten.
func ifMatchHeader(etag types.String) map[string]string {
	if etag.IsNull() || etag.IsUnknown() {
		return nil
	}
	return map[string]string{"If-Match": etag.ValueString()}
}

// snowflakeCredentialsPayload mirrors the API's SnowflakeCredentials. Every field
// is omitted when empty because the API rejects a field that does not belong to
// the chosen authentication_method.
type snowflakeCredentialsPayload struct {
	AuthenticationMethod string  `json:"authentication_method"`
	Username             *string `json:"username,omitempty"`
	Password             *string `json:"password,omitempty"`
	PrivateKey           *string `json:"private_key,omitempty"`
	PrivateKeyPassphrase *string `json:"private_key_passphrase,omitempty"`
}

// snowflakeAccountCreatePayload mirrors SnowflakeAccountCreate. Optional fields
// are omitted rather than sent as null, which for a create means the same thing
// and keeps the request to what the configuration actually says.
type snowflakeAccountCreatePayload struct {
	Id                                     string                      `json:"id"`
	Name                                   string                      `json:"name"`
	Credentials                            snowflakeCredentialsPayload `json:"credentials"`
	Role                                   *string                     `json:"role,omitempty"`
	Warehouse                              *string                     `json:"warehouse,omitempty"`
	DescribeObjectSprocName                *string                     `json:"describe_object_sproc_name,omitempty"`
	ExportStorageIntegrationName           *string                     `json:"export_storage_integration_name,omitempty"`
	ExportStageName                        *string                     `json:"export_stage_name,omitempty"`
	ComputeCostPerCredit                   *float64                    `json:"compute_cost_per_credit,omitempty"`
	StorageCostPerTb                       *float64                    `json:"storage_cost_per_tb,omitempty"`
	CustomerSideSanitizationDatabaseSchema *string                     `json:"customer_side_sanitization_database_schema,omitempty"`
	CustomerSideSanitizationViews          *[]string                   `json:"customer_side_sanitization_views,omitempty"`
	ExcludedUsersFilterExpression          *string                     `json:"excluded_users_filter_expression,omitempty"`
	FivetranUsers                          *[]string                   `json:"fivetran_users,omitempty"`
	ModeWorkspace                          *string                     `json:"mode_workspace,omitempty"`
	ModeTokenId                            *string                     `json:"mode_token_id,omitempty"`
	ModeTokenSecret                        *string                     `json:"mode_token_secret,omitempty"`
	SyncEnabled                            bool                        `json:"sync_enabled"`
	QuerySanitizationEnabled               bool                        `json:"query_sanitization_enabled"`
}

// snowflakeAccountUpdatePayload mirrors SnowflakeAccountUpdate, a JSON Merge
// Patch body where an omitted field is left unchanged and null clears it.
//
// Terraform's configuration is the whole desired state, so every clearable field
// is sent on every update: a pointer left nil serializes as null and clears the
// field, which is what removing it from the configuration means. The exceptions
// are the fields the API refuses to clear — name, credentials, sync_enabled and
// query_sanitization_enabled, which cannot be null, and mode_token_secret, which
// is cleared by clearing mode_workspace instead.
//
// credentials is omitted when unchanged so an apply that touches something else
// does not re-write the stored secret or trigger a fresh Snowflake round trip.
type snowflakeAccountUpdatePayload struct {
	Name                                   string                       `json:"name"`
	Credentials                            *snowflakeCredentialsPayload `json:"credentials,omitempty"`
	Role                                   *string                      `json:"role"`
	Warehouse                              *string                      `json:"warehouse"`
	DescribeObjectSprocName                *string                      `json:"describe_object_sproc_name"`
	ExportStorageIntegrationName           *string                      `json:"export_storage_integration_name"`
	ExportStageName                        *string                      `json:"export_stage_name"`
	ComputeCostPerCredit                   *float64                     `json:"compute_cost_per_credit"`
	StorageCostPerTb                       *float64                     `json:"storage_cost_per_tb"`
	CustomerSideSanitizationDatabaseSchema *string                      `json:"customer_side_sanitization_database_schema"`
	CustomerSideSanitizationViews          *[]string                    `json:"customer_side_sanitization_views"`
	ExcludedUsersFilterExpression          *string                      `json:"excluded_users_filter_expression"`
	FivetranUsers                          *[]string                    `json:"fivetran_users"`
	ModeWorkspace                          *string                      `json:"mode_workspace"`
	ModeTokenId                            *string                      `json:"mode_token_id"`
	ModeTokenSecret                        *string                      `json:"mode_token_secret,omitempty"`
	SyncEnabled                            bool                         `json:"sync_enabled"`
	QuerySanitizationEnabled               bool                         `json:"query_sanitization_enabled"`
}

// snowflakeAccountResponse mirrors SnowflakeAccount. It carries no credentials:
// the API stores them in a secret store and never returns them.
type snowflakeAccountResponse struct {
	Id                                     string    `json:"id"`
	Etag                                   string    `json:"etag"`
	Name                                   string    `json:"name"`
	SnowflakeOrganizationName              string    `json:"snowflake_organization_name"`
	SnowflakeAccountName                   string    `json:"snowflake_account_name"`
	Locator                                *string   `json:"locator"`
	AccountRegion                          *string   `json:"account_region"`
	SnowsightUrl                           *string   `json:"snowsight_url"`
	SnowflakeReaderParentAccountName       *string   `json:"snowflake_reader_parent_account_name"`
	Role                                   *string   `json:"role"`
	Warehouse                              *string   `json:"warehouse"`
	DescribeObjectSprocName                *string   `json:"describe_object_sproc_name"`
	ExportStorageIntegrationName           *string   `json:"export_storage_integration_name"`
	ExportStageName                        *string   `json:"export_stage_name"`
	ComputeCostPerCredit                   *float64  `json:"compute_cost_per_credit"`
	StorageCostPerTb                       *float64  `json:"storage_cost_per_tb"`
	CustomerSideSanitizationDatabaseSchema *string   `json:"customer_side_sanitization_database_schema"`
	CustomerSideSanitizationViews          *[]string `json:"customer_side_sanitization_views"`
	ExcludedUsersFilterExpression          *string   `json:"excluded_users_filter_expression"`
	FivetranUsers                          *[]string `json:"fivetran_users"`
	ModeWorkspace                          *string   `json:"mode_workspace"`
	ModeTokenId                            *string   `json:"mode_token_id"`
	ModeCredentialsConfigured              bool      `json:"mode_credentials_configured"`
	SyncEnabled                            bool      `json:"sync_enabled"`
	QuerySanitizationEnabled               bool      `json:"query_sanitization_enabled"`
	HasOrgAdminAccess                      bool      `json:"has_org_admin_access"`
	HasDataShare                           bool      `json:"has_data_share"`
	HasManageWarehousesGrant               *bool     `json:"has_manage_warehouses_grant"`
	AddedByEmail                           *string   `json:"added_by_email"`
	LatestSyncTime                         *string   `json:"latest_sync_time"`
	LastSuccessfulSyncTime                 *string   `json:"last_successful_sync_time"`
	LatestHourlySpendTime                  *string   `json:"latest_hourly_spend_time"`
	CreateTime                             string    `json:"create_time"`
	UpdateTime                             string    `json:"update_time"`
}

func buildSnowflakeCredentials(credentials resource_snowflake_account.CredentialsValue) snowflakeCredentialsPayload {
	return snowflakeCredentialsPayload{
		AuthenticationMethod: credentials.AuthenticationMethod.ValueString(),
		Username:             stringPointer(credentials.Username),
		Password:             stringPointer(credentials.Password),
		PrivateKey:           stringPointer(credentials.PrivateKey),
		PrivateKeyPassphrase: stringPointer(credentials.PrivateKeyPassphrase),
	}
}

func buildSnowflakeAccountCreate(ctx context.Context, plan *resource_snowflake_account.SnowflakeAccountModel) (*snowflakeAccountCreatePayload, diag.Diagnostics) {
	var diags diag.Diagnostics

	sanitizationViews, viewDiags := stringListPointer(ctx, plan.CustomerSideSanitizationViews)
	diags.Append(viewDiags...)
	fivetranUsers, userDiags := stringListPointer(ctx, plan.FivetranUsers)
	diags.Append(userDiags...)
	if diags.HasError() {
		return nil, diags
	}

	return &snowflakeAccountCreatePayload{
		Id:                                     plan.Id.ValueString(),
		Name:                                   plan.Name.ValueString(),
		Credentials:                            buildSnowflakeCredentials(plan.Credentials),
		Role:                                   stringPointer(plan.Role),
		Warehouse:                              stringPointer(plan.Warehouse),
		DescribeObjectSprocName:                stringPointer(plan.DescribeObjectSprocName),
		ExportStorageIntegrationName:           stringPointer(plan.ExportStorageIntegrationName),
		ExportStageName:                        stringPointer(plan.ExportStageName),
		ComputeCostPerCredit:                   numberPointer(plan.ComputeCostPerCredit),
		StorageCostPerTb:                       numberPointer(plan.StorageCostPerTb),
		CustomerSideSanitizationDatabaseSchema: stringPointer(plan.CustomerSideSanitizationDatabaseSchema),
		CustomerSideSanitizationViews:          sanitizationViews,
		ExcludedUsersFilterExpression:          stringPointer(plan.ExcludedUsersFilterExpression),
		FivetranUsers:                          fivetranUsers,
		ModeWorkspace:                          stringPointer(plan.ModeWorkspace),
		ModeTokenId:                            stringPointer(plan.ModeTokenId),
		ModeTokenSecret:                        stringPointer(plan.ModeTokenSecret),
		SyncEnabled:                            plan.SyncEnabled.ValueBool(),
		QuerySanitizationEnabled:               plan.QuerySanitizationEnabled.ValueBool(),
	}, diags
}

func buildSnowflakeAccountUpdate(ctx context.Context, plan, state *resource_snowflake_account.SnowflakeAccountModel) (*snowflakeAccountUpdatePayload, diag.Diagnostics) {
	var diags diag.Diagnostics

	if modeTokenSecretClearedWithoutDisconnect(plan, state) {
		diags.AddError(
			"Mode Token Secret Cannot Be Cleared Alone",
			"mode_token_secret cannot be removed by itself. To disconnect Mode, remove mode_workspace; SELECT clears the workspace, token ID, and stored secret together.",
		)
		return nil, diags
	}

	sanitizationViews, viewDiags := stringListPointer(ctx, plan.CustomerSideSanitizationViews)
	diags.Append(viewDiags...)
	fivetranUsers, userDiags := stringListPointer(ctx, plan.FivetranUsers)
	diags.Append(userDiags...)
	if diags.HasError() {
		return nil, diags
	}

	payload := &snowflakeAccountUpdatePayload{
		Name:                                   plan.Name.ValueString(),
		Role:                                   stringPointer(plan.Role),
		Warehouse:                              stringPointer(plan.Warehouse),
		DescribeObjectSprocName:                stringPointer(plan.DescribeObjectSprocName),
		ExportStorageIntegrationName:           stringPointer(plan.ExportStorageIntegrationName),
		ExportStageName:                        stringPointer(plan.ExportStageName),
		ComputeCostPerCredit:                   numberPointer(plan.ComputeCostPerCredit),
		StorageCostPerTb:                       numberPointer(plan.StorageCostPerTb),
		CustomerSideSanitizationDatabaseSchema: stringPointer(plan.CustomerSideSanitizationDatabaseSchema),
		CustomerSideSanitizationViews:          sanitizationViews,
		ExcludedUsersFilterExpression:          stringPointer(plan.ExcludedUsersFilterExpression),
		FivetranUsers:                          fivetranUsers,
		ModeWorkspace:                          stringPointer(plan.ModeWorkspace),
		ModeTokenId:                            stringPointer(plan.ModeTokenId),
		SyncEnabled:                            plan.SyncEnabled.ValueBool(),
		QuerySanitizationEnabled:               plan.QuerySanitizationEnabled.ValueBool(),
	}

	if !plan.Credentials.Equal(state.Credentials) {
		credentials := buildSnowflakeCredentials(plan.Credentials)
		payload.Credentials = &credentials
	}
	// The API will not clear this secret on its own, so send it only when the
	// configuration names a new one. Removing Mode entirely is done by clearing
	// mode_workspace, which clears the secret with it.
	if !plan.ModeTokenSecret.Equal(state.ModeTokenSecret) {
		payload.ModeTokenSecret = stringPointer(plan.ModeTokenSecret)
	}

	return payload, diags
}

func modeTokenSecretClearedWithoutDisconnect(plan, state *resource_snowflake_account.SnowflakeAccountModel) bool {
	return !state.ModeTokenSecret.IsNull() &&
		plan.ModeTokenSecret.IsNull() &&
		!plan.ModeWorkspace.IsNull()
}

// applySnowflakeAccountResponse writes an API response onto the model. The
// credentials block and mode_token_secret are carried over from source rather
// than the response, since the API never returns a secret and Terraform's own
// configuration is the only record of them.
func applySnowflakeAccountResponse(
	ctx context.Context,
	model *resource_snowflake_account.SnowflakeAccountModel,
	source *resource_snowflake_account.SnowflakeAccountModel,
	account *snowflakeAccountResponse,
) diag.Diagnostics {
	var diags diag.Diagnostics

	model.Id = types.StringValue(account.Id)
	model.Etag = types.StringValue(account.Etag)
	model.Name = types.StringValue(account.Name)
	model.SnowflakeOrganizationName = types.StringValue(account.SnowflakeOrganizationName)
	model.SnowflakeAccountName = types.StringValue(account.SnowflakeAccountName)
	model.Locator = stringValue(account.Locator)
	model.AccountRegion = stringValue(account.AccountRegion)
	model.SnowsightUrl = stringValue(account.SnowsightUrl)
	model.SnowflakeReaderParentAccountName = stringValue(account.SnowflakeReaderParentAccountName)
	model.Role = stringValue(account.Role)
	model.Warehouse = stringValue(account.Warehouse)
	model.DescribeObjectSprocName = stringValue(account.DescribeObjectSprocName)
	model.ExportStorageIntegrationName = stringValue(account.ExportStorageIntegrationName)
	model.ExportStageName = stringValue(account.ExportStageName)
	model.ComputeCostPerCredit = preserveEquivalentNumber(source.ComputeCostPerCredit, account.ComputeCostPerCredit)
	model.StorageCostPerTb = preserveEquivalentNumber(source.StorageCostPerTb, account.StorageCostPerTb)
	model.CustomerSideSanitizationDatabaseSchema = stringValue(account.CustomerSideSanitizationDatabaseSchema)
	model.CustomerSideSanitizationViews = preserveEquivalentList(ctx, source.CustomerSideSanitizationViews, account.CustomerSideSanitizationViews)
	model.ExcludedUsersFilterExpression = preserveEquivalentJSON(source.ExcludedUsersFilterExpression, account.ExcludedUsersFilterExpression)
	model.FivetranUsers = preserveEquivalentList(ctx, source.FivetranUsers, account.FivetranUsers)
	model.ModeWorkspace = stringValue(account.ModeWorkspace)
	model.ModeTokenId = stringValue(account.ModeTokenId)
	model.ModeCredentialsConfigured = types.BoolValue(account.ModeCredentialsConfigured)
	model.SyncEnabled = types.BoolValue(account.SyncEnabled)
	model.QuerySanitizationEnabled = types.BoolValue(account.QuerySanitizationEnabled)
	model.HasOrgAdminAccess = types.BoolValue(account.HasOrgAdminAccess)
	model.HasDataShare = types.BoolValue(account.HasDataShare)
	model.HasManageWarehousesGrant = boolValue(account.HasManageWarehousesGrant)
	model.AddedByEmail = stringValue(account.AddedByEmail)
	model.LatestSyncTime = stringValue(account.LatestSyncTime)
	model.LastSuccessfulSyncTime = stringValue(account.LastSuccessfulSyncTime)
	model.LatestHourlySpendTime = stringValue(account.LatestHourlySpendTime)
	model.CreateTime = types.StringValue(account.CreateTime)
	model.UpdateTime = types.StringValue(account.UpdateTime)

	model.Credentials = source.Credentials
	model.ModeTokenSecret = source.ModeTokenSecret

	return diags
}

// snowflakeAccountValidationReport is the report the API attaches to the problem
// document when a configuration fails its checks against Snowflake.
type snowflakeAccountValidationReport struct {
	Success bool `json:"success"`
	Checks  []struct {
		Id      string  `json:"id"`
		Label   string  `json:"label"`
		Status  string  `json:"status"`
		Message *string `json:"message"`
	} `json:"checks"`
}

// snowflakeAccountAPIDiagnostic turns an API failure into the most useful
// diagnostic available for it: the failed validation checks when the API ran
// them, an explanation of the ETag contract for a precondition failure, and the
// problem document's detail otherwise.
func snowflakeAccountAPIDiagnostic(operation string, apiErr *apiError) diag.Diagnostic {
	if report := parseSnowflakeAccountValidationReport(apiErr.Body); report != nil {
		var lines []string
		for _, check := range report.Checks {
			if check.Status == "passed" {
				continue
			}
			line := fmt.Sprintf("  - %s: %s", check.Label, check.Status)
			if check.Message != nil && *check.Message != "" {
				line = fmt.Sprintf("  - %s: %s", check.Label, *check.Message)
			}
			lines = append(lines, line)
		}
		if len(lines) > 0 {
			return diag.NewErrorDiagnostic(
				"Snowflake Account Validation Failed",
				fmt.Sprintf("SELECT could not %s. These checks did not pass:\n%s",
					operation, strings.Join(lines, "\n")),
			)
		}
	}

	switch apiErr.StatusCode {
	case http.StatusPreconditionFailed:
		return diag.NewErrorDiagnostic(
			"Snowflake Account Changed Outside Terraform",
			fmt.Sprintf("SELECT could not %s because the account has changed since Terraform last read it. "+
				"Run `terraform apply -refresh-only` to pick up the current state, then apply again.\n\n%s",
				operation, apiErr.Detail),
		)
	case http.StatusPreconditionRequired:
		return diag.NewErrorDiagnostic(
			"Missing Snowflake Account ETag",
			fmt.Sprintf("SELECT could not %s because Terraform holds no ETag for it. "+
				"Run `terraform apply -refresh-only` to record one, then apply again.\n\n%s",
				operation, apiErr.Detail),
		)
	case http.StatusConflict:
		return diag.NewErrorDiagnostic(
			"Snowflake Account Already Added",
			fmt.Sprintf("SELECT could not %s because the account is already connected. "+
				"Bring it under Terraform with `terraform import` instead of adding it again.\n\n%s",
				operation, apiErr.Detail),
		)
	case http.StatusForbidden:
		return diag.NewErrorDiagnostic(
			"Insufficient API Key Scopes",
			fmt.Sprintf("SELECT could not %s. Managing Snowflake accounts needs an API key with the "+
				"snowflake_accounts:read and snowflake_accounts:write scopes.\n\n%s",
				operation, apiErr.Detail),
		)
	default:
		return diag.NewErrorDiagnostic(
			"Snowflake Account API Error",
			fmt.Sprintf("SELECT could not %s: %s", operation, apiErr.Error()),
		)
	}
}

func parseSnowflakeAccountValidationReport(body string) *snowflakeAccountValidationReport {
	var problem struct {
		ValidationReport *snowflakeAccountValidationReport `json:"validation_report"`
	}
	if err := json.Unmarshal([]byte(body), &problem); err != nil {
		return nil
	}
	return problem.ValidationReport
}
