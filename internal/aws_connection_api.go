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
	Plural:     "AWS connections",
	ReadScope:  "aws_accounts:read",
	WriteScope: "aws_accounts:write",
}

// The `field` values the API sends on a 409, which is the only status here that
// covers more than one situation. Both carry the same issue, already_connected,
// so the field is what tells them apart.
const (
	fieldPayerAccountId = "payer_account_id"
	fieldS3Bucket       = "s3_bucket"
)

// awsCredentialsPayload mirrors the API's AwsCredentials. Both fields are
// required: the API has no way to change one without the other, so a rotation
// moves them together.
type awsCredentialsPayload struct {
	AccessKeyId     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

// awsConnectionCreatePayload mirrors AwsAccountCreate. sync_enabled has a schema
// default, so the plan always carries a value for it and there is nothing to
// omit; s3_prefix is the one genuinely optional field, and omitting it means the
// same thing as sending null on a create.
type awsConnectionCreatePayload struct {
	Name           string                `json:"name"`
	PayerAccountId string                `json:"payer_account_id"`
	S3Bucket       string                `json:"s3_bucket"`
	S3Prefix       *string               `json:"s3_prefix,omitempty"`
	Region         string                `json:"region"`
	Credentials    awsCredentialsPayload `json:"credentials"`
	SyncEnabled    bool                  `json:"sync_enabled"`
}

// awsConnectionUpdatePayload mirrors AwsAccountUpdate, a JSON Merge Patch body
// where an omitted field is left unchanged.
//
// Every field is omitted when it has not changed, the same shape the Databricks
// and BigQuery payloads use and for the same two reasons:
//
//   - s3_prefix is the only thing on this resource that can be cleared. The API
//     rejects an explicit null for name, payer_account_id, s3_bucket, region,
//     credentials and sync_enabled outright, so for those there is no removal
//     that has to reach the API as a null.
//   - SELECT re-validates against S3 when payer_account_id, s3_bucket,
//     s3_prefix, region or credentials are *present* in the patch, not when they
//     change. Sending them unconditionally would turn renaming a connection into
//     a live round trip to S3 that an AWS outage could fail. Omitting credentials
//     additionally avoids rewriting the stored secret on an apply that has
//     nothing to do with it.
//
// s3_prefix is a *nullableString rather than a *string because it has to say
// three things: omitted leaves the prefix alone, an explicit null moves the
// connection to the root of the bucket, and a value sets it. `omitempty` on a
// *string collapses the first two.
type awsConnectionUpdatePayload struct {
	Name           *string                `json:"name,omitempty"`
	PayerAccountId *string                `json:"payer_account_id,omitempty"`
	S3Bucket       *string                `json:"s3_bucket,omitempty"`
	S3Prefix       *nullableString        `json:"s3_prefix,omitempty"`
	Region         *string                `json:"region,omitempty"`
	Credentials    *awsCredentialsPayload `json:"credentials,omitempty"`
	SyncEnabled    *bool                  `json:"sync_enabled,omitempty"`
}

// awsConnectionResponse mirrors AwsAccount. It carries no credentials: the API
// stores the secret access key in a secret store and returns neither half of the
// pair on this resource.
type awsConnectionResponse struct {
	Id                     string  `json:"id"`
	Etag                   string  `json:"etag"`
	Name                   string  `json:"name"`
	ConnectionId           string  `json:"connection_id"`
	PayerAccountId         string  `json:"payer_account_id"`
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
		AccessKeyId:     credentials.AccessKeyId.ValueString(),
		SecretAccessKey: credentials.SecretAccessKey.ValueString(),
	}
}

func buildAwsConnectionCreate(plan *resource_aws_connection.AwsConnectionModel) *awsConnectionCreatePayload {
	return &awsConnectionCreatePayload{
		Name:           plan.Name.ValueString(),
		PayerAccountId: plan.PayerAccountId.ValueString(),
		S3Bucket:       plan.S3Bucket.ValueString(),
		S3Prefix:       stringPointer(plan.S3Prefix),
		Region:         plan.Region.ValueString(),
		Credentials:    buildAwsCredentials(plan.Credentials),
		SyncEnabled:    plan.SyncEnabled.ValueBool(),
	}
}

// buildAwsConnectionUpdate carries only the fields whose configured value differs
// from what state records. See awsConnectionUpdatePayload.
func buildAwsConnectionUpdate(plan, state *resource_aws_connection.AwsConnectionModel) *awsConnectionUpdatePayload {
	payload := &awsConnectionUpdatePayload{
		S3Prefix: clearedString(plan.S3Prefix, state.S3Prefix),
	}

	if !plan.Name.Equal(state.Name) {
		payload.Name = stringPointer(plan.Name)
	}
	if !plan.PayerAccountId.Equal(state.PayerAccountId) {
		payload.PayerAccountId = stringPointer(plan.PayerAccountId)
	}
	if !plan.S3Bucket.Equal(state.S3Bucket) {
		payload.S3Bucket = stringPointer(plan.S3Bucket)
	}
	if !plan.Region.Equal(state.Region) {
		payload.Region = stringPointer(plan.Region)
	}
	if !plan.Credentials.Equal(state.Credentials) {
		credentials := buildAwsCredentials(plan.Credentials)
		payload.Credentials = &credentials
	}
	if !plan.SyncEnabled.Equal(state.SyncEnabled) {
		payload.SyncEnabled = boolPointer(plan.SyncEnabled)
	}

	return payload
}

// applyAwsConnectionResponse writes an API response onto the model. The
// credentials block is carried over from source rather than the response, since
// the API returns neither the access key id nor the secret on this resource and
// Terraform's own configuration is the only record of them.
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
	model.PayerAccountId = types.StringValue(connection.PayerAccountId)
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
	if diagnostic := v2ValidationDiagnostic("AWS Connection Validation Failed", operation, apiErr); diagnostic != nil {
		return diagnostic
	}

	switch apiErr.StatusCode {
	case http.StatusPreconditionFailed:
		return awsConnectionErrors.preconditionFailed(operation, apiErr)
	case http.StatusPreconditionRequired:
		return awsConnectionErrors.preconditionRequired(operation, apiErr)
	case http.StatusConflict:
		// SELECT rejects a second connection covering ground it already reads,
		// which is either the same payer account or the same report location.
		// Both arrive as already_connected, so the field is what tells them apart.
		if apiErr.field() == fieldS3Bucket {
			return diag.NewErrorDiagnostic(
				"S3 Location Already Connected",
				fmt.Sprintf("SELECT could not %s because this bucket and prefix are already read by "+
					"another connection. Point this one at a different report location, or bring the "+
					"existing connection under Terraform with `terraform import`.\n\n%s",
					operation, apiErr.Detail),
			)
		}
		return diag.NewErrorDiagnostic(
			"AWS Connection Already Exists",
			fmt.Sprintf("SELECT could not %s because this payer account is already connected. "+
				"SELECT supports one connection per payer account; bring the existing one under "+
				"Terraform with `terraform import` instead of adding it again.\n\n%s",
				operation, apiErr.Detail),
		)
	case http.StatusForbidden:
		return awsConnectionErrors.forbidden(operation, apiErr)
	case http.StatusServiceUnavailable:
		// SELECT keeps the secret access key in a secret store it reaches on every
		// write, so this says nothing about the configuration and retrying works.
		return diag.NewErrorDiagnostic(
			"SELECT Credential Store Unavailable",
			fmt.Sprintf("SELECT could not %s because its credential store is temporarily "+
				"unavailable. Nothing about the configuration is wrong; run `terraform apply` "+
				"again.\n\n%s", operation, apiErr.Detail),
		)
	default:
		return awsConnectionErrors.unexpected(operation, apiErr)
	}
}
