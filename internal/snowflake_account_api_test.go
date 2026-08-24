// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"terraform-provider-select/internal/provider/resource_snowflake_account"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func credentials(method, username, password, privateKey, passphrase string) resource_snowflake_account.CredentialsValue {
	optional := func(value string) types.String {
		if value == "" {
			return types.StringNull()
		}
		return types.StringValue(value)
	}
	return resource_snowflake_account.NewCredentialsValueMust(
		resource_snowflake_account.CredentialsValue{}.AttributeTypes(context.Background()),
		map[string]attr.Value{
			"authentication_method":  types.StringValue(method),
			"username":               optional(username),
			"password":               optional(password),
			"private_key":            optional(privateKey),
			"private_key_passphrase": optional(passphrase),
		},
	)
}

// credentialsWithEmptyStrings builds a credentials block where the named fields
// are set to "" rather than left null.
func credentialsWithEmptyStrings(method, username string, empty ...string) resource_snowflake_account.CredentialsValue {
	attributes := map[string]attr.Value{
		"authentication_method":  types.StringValue(method),
		"username":               types.StringNull(),
		"password":               types.StringNull(),
		"private_key":            types.StringNull(),
		"private_key_passphrase": types.StringNull(),
	}
	if username != "" {
		attributes["username"] = types.StringValue(username)
	}
	for _, field := range empty {
		attributes[field] = types.StringValue("")
	}
	return resource_snowflake_account.NewCredentialsValueMust(
		resource_snowflake_account.CredentialsValue{}.AttributeTypes(context.Background()), attributes)
}

func minimalAccount() resource_snowflake_account.SnowflakeAccountModel {
	return resource_snowflake_account.SnowflakeAccountModel{
		Id:                                     types.StringValue("acme-us-east-1"),
		Name:                                   types.StringValue("Acme"),
		Credentials:                            credentials("password", "select_user", "hunter2", "", ""),
		ExportStageName:                        types.StringValue("db.schema.stage"),
		ExportStorageIntegrationName:           types.StringNull(),
		Role:                                   types.StringNull(),
		Warehouse:                              types.StringNull(),
		DescribeObjectSprocName:                types.StringNull(),
		ComputeCostPerCredit:                   types.NumberNull(),
		StorageCostPerTb:                       types.NumberNull(),
		CustomerSideSanitizationViews:          types.ListNull(types.StringType),
		FivetranUsers:                          types.ListNull(types.StringType),
		ExcludedUsersFilterExpression:          types.StringNull(),
		CustomerSideSanitizationDatabaseSchema: types.StringNull(),
		ModeWorkspace:                          types.StringNull(),
		ModeTokenId:                            types.StringNull(),
		ModeTokenSecret:                        types.StringNull(),
		SyncEnabled:                            types.BoolValue(true),
		QuerySanitizationEnabled:               types.BoolValue(false),
	}
}

func marshal(t *testing.T, payload any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshaling payload: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decoding payload: %v", err)
	}
	return decoded
}

// A create body should carry only what the configuration says, so an unset
// optional field is absent rather than an explicit null the API would have to
// interpret.
func TestCreatePayloadOmitsUnsetFields(t *testing.T) {
	plan := minimalAccount()

	payload, diags := buildSnowflakeAccountCreate(context.Background(), &plan)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	body := marshal(t, payload)

	for _, absent := range []string{"role", "warehouse", "compute_cost_per_credit", "fivetran_users", "export_storage_integration_name"} {
		if _, present := body[absent]; present {
			t.Errorf("expected %q to be omitted from the create body, got %v", absent, body[absent])
		}
	}
	if body["export_stage_name"] != "db.schema.stage" {
		t.Errorf("export_stage_name = %v, want db.schema.stage", body["export_stage_name"])
	}
	if body["sync_enabled"] != true {
		t.Errorf("sync_enabled = %v, want true", body["sync_enabled"])
	}
	creds, ok := body["credentials"].(map[string]any)
	if !ok {
		t.Fatalf("credentials missing from create body: %v", body)
	}
	if creds["password"] != "hunter2" || creds["username"] != "select_user" {
		t.Errorf("credentials = %v, want the configured username and password", creds)
	}
	if _, present := creds["private_key"]; present {
		t.Errorf("private_key should be omitted for the password method, got %v", creds["private_key"])
	}
}

// Merge patch treats an omitted field as unchanged, so removing an optional
// field from the configuration has to send an explicit null to clear it.
func TestUpdatePayloadClearsRemovedFields(t *testing.T) {
	state := minimalAccount()
	state.Warehouse = types.StringValue("SELECT_WH")
	state.Role = types.StringValue("SELECT_ROLE")

	plan := state
	plan.Warehouse = types.StringNull()

	payload, diags := buildSnowflakeAccountUpdate(context.Background(), &plan, &state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	body := marshal(t, payload)

	warehouse, present := body["warehouse"]
	if !present || warehouse != nil {
		t.Errorf("warehouse = %v (present=%v), want an explicit null", warehouse, present)
	}
	if body["role"] != "SELECT_ROLE" {
		t.Errorf("role = %v, want the unchanged value to still be sent", body["role"])
	}
}

// Re-sending unchanged credentials would rotate the stored secret and make the
// API re-validate against Snowflake for no reason.
func TestUpdatePayloadOnlySendsChangedSecrets(t *testing.T) {
	state := minimalAccount()
	state.ModeWorkspace = types.StringValue("acme")
	state.ModeTokenId = types.StringValue("token-1")
	state.ModeTokenSecret = types.StringValue("secret-1")

	unchanged := state
	unchanged.Name = types.StringValue("Acme Renamed")

	body := marshal(t, mustBuildUpdate(t, &unchanged, &state))
	if _, present := body["credentials"]; present {
		t.Errorf("credentials should be omitted when unchanged, got %v", body["credentials"])
	}
	if _, present := body["mode_token_secret"]; present {
		t.Errorf("mode_token_secret should be omitted when unchanged, got %v", body["mode_token_secret"])
	}

	rotated := state
	rotated.Credentials = credentials("key_pair", "select_user", "", "-----BEGIN PRIVATE KEY-----", "")
	rotated.ModeTokenSecret = types.StringValue("secret-2")

	body = marshal(t, mustBuildUpdate(t, &rotated, &state))
	creds, ok := body["credentials"].(map[string]any)
	if !ok {
		t.Fatalf("credentials should be sent when changed: %v", body)
	}
	if creds["authentication_method"] != "key_pair" {
		t.Errorf("authentication_method = %v, want key_pair", creds["authentication_method"])
	}
	if _, present := creds["password"]; present {
		t.Errorf("password should be omitted after switching to key_pair, got %v", creds["password"])
	}
	if body["mode_token_secret"] != "secret-2" {
		t.Errorf("mode_token_secret = %v, want secret-2", body["mode_token_secret"])
	}
}

func mustBuildUpdate(t *testing.T, plan, state *resource_snowflake_account.SnowflakeAccountModel) *snowflakeAccountUpdatePayload {
	t.Helper()
	payload, diags := buildSnowflakeAccountUpdate(context.Background(), plan, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	return payload
}

// A read must not overwrite the configured credentials with the nothing the API
// returns for them, and must not report reformatted JSON as drift.
func TestApplyResponsePreservesSecretsAndJSONSpelling(t *testing.T) {
	source := minimalAccount()
	source.ModeTokenSecret = types.StringValue("secret-1")
	source.ExcludedUsersFilterExpression = types.StringValue(`{"users":  ["a", "b"]}`)

	returned := `{"users":["a","b"]}`
	account := &snowflakeAccountResponse{
		Id:                            "acme-us-east-1",
		Etag:                          `"abc123"`,
		Name:                          "Acme",
		ExcludedUsersFilterExpression: &returned,
	}

	model := source
	if diags := applySnowflakeAccountResponse(context.Background(), &model, &source, account); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if !model.Credentials.Equal(source.Credentials) {
		t.Errorf("credentials = %v, want them carried over from the configuration", model.Credentials)
	}
	if model.ModeTokenSecret.ValueString() != "secret-1" {
		t.Errorf("mode_token_secret = %v, want secret-1", model.ModeTokenSecret)
	}
	if model.ExcludedUsersFilterExpression.ValueString() != source.ExcludedUsersFilterExpression.ValueString() {
		t.Errorf("excluded_users_filter_expression = %v, want the configured spelling kept",
			model.ExcludedUsersFilterExpression)
	}
	if model.Etag.ValueString() != `"abc123"` {
		t.Errorf("etag = %v, want the value from the response", model.Etag)
	}

	// A genuinely different document is drift and must be reported as such.
	different := `{"users":["c"]}`
	account.ExcludedUsersFilterExpression = &different
	if diags := applySnowflakeAccountResponse(context.Background(), &model, &source, account); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if model.ExcludedUsersFilterExpression.ValueString() != different {
		t.Errorf("excluded_users_filter_expression = %v, want the API's value on a real change",
			model.ExcludedUsersFilterExpression)
	}
}

// fivetran_users has a Postgres server_default of "{}" (see model.go); when
// the configuration never sets it, the real API returns [] rather than null,
// which must not read as a change the configuration never asked for.
func TestApplyResponseCollapsesEmptyFivetranUsersToNull(t *testing.T) {
	source := minimalAccount()
	source.FivetranUsers = types.ListNull(types.StringType)

	empty := []string{}
	account := &snowflakeAccountResponse{
		Id:            "acme-us-east-1",
		Etag:          `"abc123"`,
		Name:          "Acme",
		FivetranUsers: &empty,
	}

	model := source
	if diags := applySnowflakeAccountResponse(context.Background(), &model, &source, account); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !model.FivetranUsers.IsNull() {
		t.Errorf("fivetran_users = %v, want null preserved when unconfigured", model.FivetranUsers)
	}

	// An explicitly configured empty list is a different claim and must not be
	// collapsed away.
	source.FivetranUsers, _ = types.ListValueFrom(context.Background(), types.StringType, []string{})
	model = source
	if diags := applySnowflakeAccountResponse(context.Background(), &model, &source, account); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if model.FivetranUsers.IsNull() {
		t.Errorf("fivetran_users should stay [] when the configuration explicitly set it, got null")
	}

	// A genuinely non-empty list must never be collapsed.
	nonEmpty := []string{"FIVETRAN_USER"}
	account.FivetranUsers = &nonEmpty
	source.FivetranUsers = types.ListNull(types.StringType)
	model = source
	if diags := applySnowflakeAccountResponse(context.Background(), &model, &source, account); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	elements := model.FivetranUsers.Elements()
	if len(elements) != 1 {
		t.Errorf("fivetran_users = %v, want the API's non-empty value preserved", model.FivetranUsers)
	}
}

func TestValidateCredentialsMatchAuthenticationMethod(t *testing.T) {
	cases := []struct {
		name        string
		credentials resource_snowflake_account.CredentialsValue
		wantError   bool
	}{
		{"password complete", credentials("password", "u", "p", "", ""), false},
		{"password missing password", credentials("password", "u", "", "", ""), true},
		{"password with private key", credentials("password", "u", "p", "key", ""), true},
		{"key pair complete", credentials("key_pair", "u", "", "key", ""), false},
		{"key pair with passphrase", credentials("key_pair", "u", "", "key", "phrase"), false},
		{"key pair missing key", credentials("key_pair", "u", "", "", ""), true},
		{"workload identity bare", credentials("workload_identity_federation", "", "", "", ""), false},
		{"workload identity with username", credentials("workload_identity_federation", "u", "", "", ""), true},
		// The API treats an empty string as missing when deciding what is
		// required, but as supplied when deciding what does not belong.
		{"password with empty password", credentialsWithEmptyStrings("password", "u", "password"), true},
		{"workload identity with empty username", credentialsWithEmptyStrings("workload_identity_federation", "", "username"), true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			diags := validateSnowflakeCredentials(testCase.credentials)
			if diags.HasError() != testCase.wantError {
				t.Errorf("HasError() = %v, want %v (%v)", diags.HasError(), testCase.wantError, diags)
			}
		})
	}
}

// A failing create returns the checks that did not pass; without unpacking them
// the user sees only that validation failed.
func TestValidationReportBecomesADiagnostic(t *testing.T) {
	body := `{
		"title": "Validation failed",
		"status": 422,
		"detail": "The account's configuration did not pass validation.",
		"code": "validation_failed",
		"validation_report": {
			"success": false,
			"checks": [
				{"id": "connectivity", "label": "Connectivity", "status": "passed"},
				{"id": "permissions", "label": "Permissions", "status": "failed",
				 "message": "Grant imported privileges on database snowflake to SELECT's role."},
				{"id": "org_admin", "label": "Organization admin", "status": "skipped",
				 "message": "Skipped because permissions failed."}
			]
		}
	}`

	diagnostic := snowflakeAccountAPIDiagnostic("add the Snowflake account", newAPIError(422, body))
	detail := diagnostic.Detail()

	if !strings.Contains(detail, "Grant imported privileges") {
		t.Errorf("diagnostic should name the failing check's remedy, got: %s", detail)
	}
	if !strings.Contains(detail, "Skipped because permissions failed.") {
		t.Errorf("diagnostic should include skipped checks, got: %s", detail)
	}
	if strings.Contains(detail, "Connectivity") {
		t.Errorf("diagnostic should omit passing checks, got: %s", detail)
	}
}

// The ETag contract is the least obvious thing a user can hit, so a precondition
// failure has to say what to do about it.
func TestPreconditionFailureExplainsRefresh(t *testing.T) {
	diagnostic := snowflakeAccountAPIDiagnostic("update the Snowflake account",
		newAPIError(412, `{"detail":"The If-Match header does not match the resource's current ETag.","code":"precondition_failed"}`))

	if !strings.Contains(diagnostic.Detail(), "-refresh-only") {
		t.Errorf("diagnostic should tell the user how to recover, got: %s", diagnostic.Detail())
	}
}
