// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"terraform-provider-select/internal/provider/resource_aws_connection"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// AWS connections live on the v2 API, where the resource is called an AWS
// account and the collection is /aws-accounts. See v2_api.go for the conventions
// every resource on that surface shares.
const awsAccountsEndpoint = "/v2/aws-accounts"

func awsConnectionEndpoint(id string) string {
	return fmt.Sprintf("%s/%s", awsAccountsEndpoint, id)
}

// awsConnectionErrors words the failures every v2 resource can hit.
var awsConnectionErrors = v2ErrorFormat{
	Noun:       "AWS Connection",
	Subject:    "the connection",
	Object:     "the AWS connection",
	ReadScope:  "aws_accounts:read",
	WriteScope: "aws_accounts:write",
}

// The `field` values the API sends on a 409, which is the only status here that
// covers more than one situation. Both carry the same issue, already_connected,
// so the field is what tells them apart.
const (
	fieldAwsAccountId = "aws_account_id"
	fieldS3Bucket     = "s3_bucket"
	fieldName         = "name"
)

// awsCredentialsPayload mirrors the API's AwsCredentials: the role SELECT
// assumes to read the account's report.
type awsCredentialsPayload struct {
	RoleArn string `json:"role_arn"`
}

// awsConnectionCreatePayload mirrors AwsAccountCreate. sync_enabled has a schema
// default, so the plan always carries a value for it and there is nothing to
// omit; s3_prefix is the one genuinely optional field, and omitting it means the
// same thing as sending null on a create.
type awsConnectionCreatePayload struct {
	Name         string                `json:"name"`
	AwsAccountId string                `json:"aws_account_id"`
	S3Bucket     string                `json:"s3_bucket"`
	S3Prefix     *string               `json:"s3_prefix,omitempty"`
	Region       string                `json:"region"`
	Credentials  awsCredentialsPayload `json:"credentials"`
	SyncEnabled  bool                  `json:"sync_enabled"`
}

// awsConnectionUpdatePayload mirrors AwsAccountUpdate, a JSON Merge Patch body
// where an omitted field is left unchanged.
//
// Every field is omitted when it has not changed, the same shape the Databricks
// and BigQuery payloads use and for the same two reasons:
//
//   - s3_prefix is the only thing on this resource that can be cleared. The API
//     rejects an explicit null for name, aws_account_id, s3_bucket, region,
//     credentials and sync_enabled outright, so for those there is no removal
//     that has to reach the API as a null.
//   - SELECT re-validates against S3 when aws_account_id, s3_bucket,
//     s3_prefix, region or credentials are *present* in the patch, not when they
//     change. Sending them unconditionally would turn renaming a connection into
//     a live round trip to S3 that an AWS outage could fail.
//
// s3_prefix is a *nullableString rather than a *string because it has to say
// three things: omitted leaves the prefix alone, an explicit null moves the
// connection to the root of the bucket, and a value sets it. `omitempty` on a
// *string collapses the first two.
type awsConnectionUpdatePayload struct {
	Name         *string                `json:"name,omitempty"`
	AwsAccountId *string                `json:"aws_account_id,omitempty"`
	S3Bucket     *string                `json:"s3_bucket,omitempty"`
	S3Prefix     *nullableString        `json:"s3_prefix,omitempty"`
	Region       *string                `json:"region,omitempty"`
	Credentials  *awsCredentialsPayload `json:"credentials,omitempty"`
	SyncEnabled  *bool                  `json:"sync_enabled,omitempty"`
}

// awsConnectionResponse mirrors AwsAccount. It carries no credentials, so the
// role comes from the configuration (see applyAwsConnectionResponse).
type awsConnectionResponse struct {
	Id                     string  `json:"id"`
	Etag                   string  `json:"etag"`
	Name                   string  `json:"name"`
	ConnectionId           string  `json:"connection_id"`
	AwsAccountId           string  `json:"aws_account_id"`
	S3Bucket               string  `json:"s3_bucket"`
	S3Prefix               *string `json:"s3_prefix"`
	Region                 string  `json:"region"`
	SyncEnabled            bool    `json:"sync_enabled"`
	AddedByEmail           *string `json:"added_by_email"`
	LastSuccessfulSyncTime *string `json:"last_successful_sync_time"`
	CreateTime             string  `json:"create_time"`
	UpdateTime             string  `json:"update_time"`
}

func buildAwsCredentials(credentials resource_aws_connection.CredentialsValue) awsCredentialsPayload {
	return awsCredentialsPayload{
		RoleArn: credentials.RoleArn.ValueString(),
	}
}

func buildAwsConnectionCreate(plan *resource_aws_connection.AwsConnectionModel) *awsConnectionCreatePayload {
	return &awsConnectionCreatePayload{
		Name:         plan.Name.ValueString(),
		AwsAccountId: plan.AwsAccountId.ValueString(),
		S3Bucket:     plan.S3Bucket.ValueString(),
		S3Prefix:     stringPointer(plan.S3Prefix),
		Region:       plan.Region.ValueString(),
		Credentials:  buildAwsCredentials(plan.Credentials),
		SyncEnabled:  plan.SyncEnabled.ValueBool(),
	}
}

// buildAwsConnectionUpdate carries only the fields whose configured value differs
// from what state records. See awsConnectionUpdatePayload.
func buildAwsConnectionUpdate(plan, state *resource_aws_connection.AwsConnectionModel) *awsConnectionUpdatePayload {
	payload := &awsConnectionUpdatePayload{
		Name:         changedString(plan.Name, state.Name),
		AwsAccountId: changedString(plan.AwsAccountId, state.AwsAccountId),
		S3Bucket:     changedString(plan.S3Bucket, state.S3Bucket),
		S3Prefix:     clearedString(plan.S3Prefix, state.S3Prefix),
		Region:       changedString(plan.Region, state.Region),
		SyncEnabled:  changedBool(plan.SyncEnabled, state.SyncEnabled),
	}

	if !plan.Credentials.Equal(state.Credentials) {
		credentials := buildAwsCredentials(plan.Credentials)
		payload.Credentials = &credentials
	}

	return payload
}

// applyAwsConnectionResponse writes an API response onto the model. The
// credentials block is carried over from source rather than the response, since
// the API returns no credentials on this resource and Terraform's own
// configuration is the only record of them.
//
// Nothing here goes through a preserveEquivalent* helper: SELECT stores every
// field on this resource exactly as given and returns it unchanged, so there is
// no normalization to absorb.
func applyAwsConnectionResponse(
	ctx context.Context,
	model *resource_aws_connection.AwsConnectionModel,
	source *resource_aws_connection.AwsConnectionModel,
	connection *awsConnectionResponse,
) diag.Diagnostics {
	var diags diag.Diagnostics

	model.Id = types.StringValue(connection.Id)
	model.Etag = types.StringValue(connection.Etag)
	model.Name = types.StringValue(connection.Name)
	model.ConnectionId = types.StringValue(connection.ConnectionId)
	model.AwsAccountId = types.StringValue(connection.AwsAccountId)
	model.S3Bucket = types.StringValue(connection.S3Bucket)
	model.S3Prefix = stringValue(connection.S3Prefix)
	model.Region = types.StringValue(connection.Region)
	model.SyncEnabled = types.BoolValue(connection.SyncEnabled)
	model.AddedByEmail = stringValue(connection.AddedByEmail)
	model.LastSuccessfulSyncTime = stringValue(connection.LastSuccessfulSyncTime)
	model.CreateTime = types.StringValue(connection.CreateTime)
	model.UpdateTime = types.StringValue(connection.UpdateTime)

	model.Credentials = source.Credentials

	return diags
}

// awsConnectionAPIDiagnostic turns an API failure into the most useful
// diagnostic available for it: the checks that did not pass when SELECT ran them
// against S3, an explanation of the ETag contract for a precondition failure, and
// the problem document's detail otherwise.
func awsConnectionAPIDiagnostic(operation string, apiErr *apiError) diag.Diagnostic {
	return awsConnectionErrors.diagnostic(operation, apiErr, awsConnectionSpecificDiagnostic)
}

// awsConnectionSpecificDiagnostic is this resource's own opinion about an API
// failure: the several situations a 409 conflict covers. Everything else this resource can hit — the
// validation report, precondition failures, and missing scopes — is worded the
// same for every v2 resource, so it is handled by v2ErrorFormat.diagnostic
// instead of here.
func awsConnectionSpecificDiagnostic(operation string, apiErr *apiError) diag.Diagnostic {
	switch apiErr.StatusCode {
	case http.StatusConflict:
		// SELECT rejects a second connection covering ground it already reads —
		// the same AWS account or the same report location — and, separately,
		// a connection name already used by another connection in the
		// organization. All three arrive as the same conflict status; field is
		// what tells them apart, since the first two both carry the issue
		// already_connected.
		switch apiErr.field() {
		case fieldS3Bucket:
			return diag.NewErrorDiagnostic(
				"S3 Location Already Connected",
				fmt.Sprintf("SELECT could not %s because this bucket and prefix are already read by "+
					"another connection. Point this one at a different report location, or bring the "+
					"existing connection under Terraform with `terraform import`.\n\n%s",
					operation, apiErr.Detail),
			)
		case fieldName:
			return diag.NewErrorDiagnostic(
				"Connection Name Already Exists",
				fmt.Sprintf("SELECT could not %s because another connection is already named that. "+
					"Connection names must be unique within an organization; choose a different "+
					"name.\n\n%s", operation, apiErr.Detail),
			)
		default:
			return diag.NewErrorDiagnostic(
				"AWS Connection Already Exists",
				fmt.Sprintf("SELECT could not %s because this AWS account is already connected. "+
					"SELECT supports one connection per AWS account; bring the existing one under "+
					"Terraform with `terraform import` instead of adding it again.\n\n%s",
					operation, apiErr.Detail),
			)
		}
	default:
		return nil
	}
}
