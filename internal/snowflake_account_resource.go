// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"terraform-provider-select/internal/provider/resource_snowflake_account"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = (*snowflakeAccountResource)(nil)
var _ resource.ResourceWithConfigure = (*snowflakeAccountResource)(nil)
var _ resource.ResourceWithImportState = (*snowflakeAccountResource)(nil)
var _ resource.ResourceWithValidateConfig = (*snowflakeAccountResource)(nil)

func NewSnowflakeAccountResource() resource.Resource {
	return &snowflakeAccountResource{}
}

type snowflakeAccountResource struct {
	client *APIClient
}

func (r *snowflakeAccountResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	providerData, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	r.client = providerData.Client
}

func (r *snowflakeAccountResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_snowflake_account"
}

func (r *snowflakeAccountResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resource_snowflake_account.SnowflakeAccountResourceSchema(ctx)
}

func (r *snowflakeAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan resource_snowflake_account.SnowflakeAccountModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	request, diags := buildSnowflakeAccountCreate(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var account snowflakeAccountResponse
	apiErr, diags := r.client.doRequest(ctx, http.MethodPost, snowflakeAccountsEndpoint, request, &account, requestOptions{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		resp.Diagnostics.Append(snowflakeAccountAPIDiagnostic("add the Snowflake account", apiErr))
		return
	}

	state := plan
	resp.Diagnostics.Append(applySnowflakeAccountResponse(ctx, &state, &plan, &account)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *snowflakeAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state resource_snowflake_account.SnowflakeAccountModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var account snowflakeAccountResponse
	endpoint := snowflakeAccountEndpoint(state.Id.ValueString())
	apiErr, diags := r.client.doRequest(ctx, http.MethodGet, endpoint, nil, &account, requestOptions{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		// The account is gone, or is no longer visible to this API key. Either
		// way Terraform should plan to recreate it rather than keep stale values.
		if apiErr.StatusCode == http.StatusNotFound {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.Append(snowflakeAccountAPIDiagnostic("read the Snowflake account", apiErr))
		return
	}

	refreshed := state
	resp.Diagnostics.Append(applySnowflakeAccountResponse(ctx, &refreshed, &state, &account)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &refreshed)...)
}

func (r *snowflakeAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state resource_snowflake_account.SnowflakeAccountModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	request, diags := buildSnowflakeAccountUpdate(ctx, &plan, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var account snowflakeAccountResponse
	endpoint := snowflakeAccountEndpoint(state.Id.ValueString())
	apiErr, diags := r.client.doRequest(ctx, http.MethodPatch, endpoint, request, &account, requestOptions{
		headers: ifMatchHeader(state.Etag),
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if apiErr != nil {
		resp.Diagnostics.Append(snowflakeAccountAPIDiagnostic("update the Snowflake account", apiErr))
		return
	}

	updated := plan
	resp.Diagnostics.Append(applySnowflakeAccountResponse(ctx, &updated, &plan, &account)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
}

func (r *snowflakeAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state resource_snowflake_account.SnowflakeAccountModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := snowflakeAccountEndpoint(state.Id.ValueString())
	apiErr, diags := r.client.doRequest(ctx, http.MethodDelete, endpoint, nil, nil, requestOptions{
		headers: ifMatchHeader(state.Etag),
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Already gone is the outcome delete was asked for.
	if apiErr != nil && apiErr.StatusCode != http.StatusNotFound {
		resp.Diagnostics.Append(snowflakeAccountAPIDiagnostic("delete the Snowflake account", apiErr))
	}
}

func (r *snowflakeAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ValidateConfig rejects at plan time the field combinations the API rejects on
// the way in, so a mistake costs a plan rather than a round trip to Snowflake.
// The rules mirror validate_account_field_combinations and SnowflakeCredentials'
// own validator in the API's schemas.
func (r *snowflakeAccountResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config resource_snowflake_account.SnowflakeAccountModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	stage := config.ExportStageName
	integration := config.ExportStorageIntegrationName
	if !stage.IsUnknown() && !integration.IsUnknown() {
		switch {
		case !stage.IsNull() && !integration.IsNull():
			resp.Diagnostics.AddError(
				"Conflicting Export Destination",
				"Set either export_stage_name or export_storage_integration_name, not both.",
			)
		case stage.IsNull() && integration.IsNull():
			resp.Diagnostics.AddError(
				"Missing Export Destination",
				"One of export_stage_name or export_storage_integration_name is required.",
			)
		}
	}

	if !config.CustomerSideSanitizationViews.IsNull() && !config.CustomerSideSanitizationViews.IsUnknown() &&
		config.CustomerSideSanitizationDatabaseSchema.IsNull() {
		resp.Diagnostics.AddError(
			"Missing Sanitization Schema",
			"customer_side_sanitization_views requires customer_side_sanitization_database_schema.",
		)
	}

	if schema := config.CustomerSideSanitizationDatabaseSchema; !schema.IsNull() && !schema.IsUnknown() {
		if strings.Count(schema.ValueString(), ".") != 1 {
			resp.Diagnostics.AddError(
				"Invalid Sanitization Schema",
				"customer_side_sanitization_database_schema must be in DATABASE.SCHEMA form.",
			)
		}
	}

	if expression := config.ExcludedUsersFilterExpression; !expression.IsNull() && !expression.IsUnknown() {
		if !json.Valid([]byte(expression.ValueString())) {
			resp.Diagnostics.AddError(
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
		resp.Diagnostics.AddError(
			"Incomplete Rate Override",
			"compute_cost_per_credit and storage_cost_per_tb must be set together.",
		)
	}

	resp.Diagnostics.Append(validateSnowflakeCredentials(config.Credentials)...)
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
