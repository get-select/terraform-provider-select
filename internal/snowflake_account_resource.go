// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"terraform-provider-select/internal/provider/resource_snowflake_account"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func NewSnowflakeAccountResource() resource.Resource {
	return &v2Resource[resource_snowflake_account.SnowflakeAccountModel, snowflakeAccountResponse]{
		typeNameSuffix:     "_snowflake_account",
		schema:             resource_snowflake_account.SnowflakeAccountResourceSchema,
		errors:             snowflakeAccountErrors,
		specificDiagnostic: snowflakeAccountSpecificDiagnostic,
		collectionEndpoint: snowflakeAccountsEndpoint,
		itemEndpoint:       snowflakeAccountEndpoint,
		identity: func(m *resource_snowflake_account.SnowflakeAccountModel) v2Identity {
			return v2Identity{Id: m.Id, Etag: m.Etag}
		},
		createPayload:  v2FalliblePayload(buildSnowflakeAccountCreate),
		updatePayload:  v2FalliblePatch(buildSnowflakeAccountUpdate),
		applyResponse:  applySnowflakeAccountResponse,
		validateConfig: validateSnowflakeAccountConfig,
	}
}

// validateSnowflakeAccountConfig rejects at plan time the field combinations the
// API rejects on the way in, so a mistake costs a plan rather than a round trip
// to Snowflake. The rules mirror validate_account_field_combinations and
// SnowflakeCredentials' own validator in the API's schemas.
func validateSnowflakeAccountConfig(ctx context.Context, config *resource_snowflake_account.SnowflakeAccountModel) diag.Diagnostics {
	var diags diag.Diagnostics

	stage := config.ExportStageName
	integration := config.ExportStorageIntegrationName
	if !stage.IsUnknown() && !integration.IsUnknown() {
		switch {
		case !stage.IsNull() && !integration.IsNull():
			diags.AddError(
				"Conflicting Export Destination",
				"Set either export_stage_name or export_storage_integration_name, not both.",
			)
		case stage.IsNull() && integration.IsNull():
			diags.AddError(
				"Missing Export Destination",
				"One of export_stage_name or export_storage_integration_name is required.",
			)
		}
	}

	if !config.CustomerSideSanitizationViews.IsNull() && !config.CustomerSideSanitizationViews.IsUnknown() &&
		config.CustomerSideSanitizationDatabaseSchema.IsNull() {
		diags.AddError(
			"Missing Sanitization Schema",
			"customer_side_sanitization_views requires customer_side_sanitization_database_schema.",
		)
	}

	if schema := config.CustomerSideSanitizationDatabaseSchema; !schema.IsNull() && !schema.IsUnknown() {
		if strings.Count(schema.ValueString(), ".") != 1 {
			diags.AddError(
				"Invalid Sanitization Schema",
				"customer_side_sanitization_database_schema must be in DATABASE.SCHEMA form.",
			)
		}
	}

	if expression := config.ExcludedUsersFilterExpression; !expression.IsNull() && !expression.IsUnknown() {
		if !json.Valid([]byte(expression.ValueString())) {
			diags.AddError(
				"Invalid Excluded Users Filter",
				"excluded_users_filter_expression must be a JSON-encoded filter.",
			)
		}
	}

	// Only one of compute_cost_per_credit / storage_cost_per_tb is not a usable
	// override: the API needs both rates or neither.
	compute := config.ComputeCostPerCredit
	storage := config.StorageCostPerTb
	if !compute.IsUnknown() && !storage.IsUnknown() && compute.IsNull() != storage.IsNull() {
		diags.AddError(
			"Incomplete Rate Override",
			"compute_cost_per_credit and storage_cost_per_tb must be set together.",
		)
	}

	diags.Append(validateSnowflakeCredentials(config.Credentials)...)

	return diags
}

// credentialFieldsByMethod records, per authentication method, which credential
// fields the API requires and which it accepts at all. A field outside the
// chosen method's allowed set is rejected rather than ignored, so a value left
// behind from an earlier method cannot quietly change how SELECT connects.
var credentialFieldsByMethod = map[string]struct {
	required []string
	allowed  []string
}{
	"password": {
		required: []string{"username", "password"},
		allowed:  []string{"username", "password"},
	},
	"key_pair": {
		required: []string{"username", "private_key"},
		allowed:  []string{"username", "private_key", "private_key_passphrase"},
	},
	"workload_identity_federation": {},
}

func validateSnowflakeCredentials(credentials resource_snowflake_account.CredentialsValue) diag.Diagnostics {
	var diags diag.Diagnostics

	if credentials.IsNull() || credentials.IsUnknown() || credentials.AuthenticationMethod.IsUnknown() {
		return diags
	}

	method := credentials.AuthenticationMethod.ValueString()
	fields, known := credentialFieldsByMethod[method]
	if !known {
		// The generated schema already validates the enum; anything else here
		// would duplicate that error.
		return diags
	}

	if credentials.Username.IsUnknown() || credentials.Password.IsUnknown() ||
		credentials.PrivateKey.IsUnknown() || credentials.PrivateKeyPassphrase.IsUnknown() {
		// A value only known after apply cannot be checked here; the API still
		// enforces the same rules.
		return diags
	}

	// The API treats an empty string as missing when deciding what is required,
	// but as supplied when deciding what does not belong — an empty password is
	// still a password field the workload-identity method has no use for. Mirror
	// both, so a configuration that passes here passes there too.
	supplied := map[string]bool{
		"username":               !credentials.Username.IsNull(),
		"password":               !credentials.Password.IsNull(),
		"private_key":            !credentials.PrivateKey.IsNull(),
		"private_key_passphrase": !credentials.PrivateKeyPassphrase.IsNull(),
	}
	present := map[string]bool{
		"username":               isSet(credentials.Username),
		"password":               isSet(credentials.Password),
		"private_key":            isSet(credentials.PrivateKey),
		"private_key_passphrase": isSet(credentials.PrivateKeyPassphrase),
	}

	var missing []string
	for _, field := range fields.required {
		if !present[field] {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		diags.AddError(
			"Incomplete Snowflake Credentials",
			fmt.Sprintf("credentials.%s is required for authentication_method %q.",
				strings.Join(missing, ", credentials."), method),
		)
	}

	allowed := map[string]bool{}
	for _, field := range fields.allowed {
		allowed[field] = true
	}
	var unexpected []string
	for field, set := range supplied {
		if set && !allowed[field] {
			unexpected = append(unexpected, field)
		}
	}
	if len(unexpected) > 0 {
		// Sorted so the message does not depend on map iteration order.
		sort.Strings(unexpected)
		diags.AddError(
			"Unused Snowflake Credentials",
			fmt.Sprintf("credentials.%s is not valid for authentication_method %q.",
				strings.Join(unexpected, ", credentials."), method),
		)
	}

	return diags
}

func isSet(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown() && value.ValueString() != ""
}
