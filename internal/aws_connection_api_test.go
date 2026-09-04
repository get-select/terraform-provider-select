// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"
	"testing"

	"terraform-provider-select/internal/provider/resource_aws_connection"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func awsCredentials(accessKeyId, secretAccessKey string) resource_aws_connection.CredentialsValue {
	return resource_aws_connection.NewCredentialsValueMust(
		resource_aws_connection.CredentialsValue{}.AttributeTypes(context.Background()),
		map[string]attr.Value{
			"access_key_id":     types.StringValue(accessKeyId),
			"secret_access_key": types.StringValue(secretAccessKey),
		},
	)
}

func awsConnection() resource_aws_connection.AwsConnectionModel {
	return resource_aws_connection.AwsConnectionModel{
		Id:             types.StringValue("2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01"),
		Etag:           types.StringValue(`"abc123"`),
		Name:           types.StringValue("Acme Production"),
		ConnectionId:   types.StringValue("8b2d1f0a-3c4e-4a5b-9c6d-7e8f9a0b1c2d"),
		PayerAccountId: types.StringValue("123456789012"),
		S3Bucket:       types.StringValue("acme-cur"),
		S3Prefix:       types.StringValue("reports/prod"),
		Region:         types.StringValue("us-east-1"),
		Credentials:    awsCredentials("AKIAEXAMPLEONE", "secret-one"),
		SyncEnabled:    types.BoolValue(true),
	}
}

// A create body has to carry every field the API requires, including
// sync_enabled, whose schema default means the plan always has a value for it.
func TestAwsCreatePayloadCarriesEveryRequiredField(t *testing.T) {
	plan := awsConnection()

	payload := marshal(t, buildAwsConnectionCreate(&plan))

	for field, expected := range map[string]any{
		"name":             "Acme Production",
		"payer_account_id": "123456789012",
		"s3_bucket":        "acme-cur",
		"s3_prefix":        "reports/prod",
		"region":           "us-east-1",
		"sync_enabled":     true,
	} {
		if payload[field] != expected {
			t.Errorf("%s should be %v, got %v", field, expected, payload[field])
		}
	}

	credentials, ok := payload["credentials"].(map[string]any)
	if !ok {
		t.Fatalf("credentials should be an object, got %v", payload["credentials"])
	}
	if credentials["access_key_id"] != "AKIAEXAMPLEONE" || credentials["secret_access_key"] != "secret-one" {
		t.Errorf("both halves of the key pair should be sent, got %v", credentials)
	}
}

// A report delivered at the root of the bucket has no prefix, and the API takes
// an absent s3_prefix to mean exactly that — so an unset one is omitted rather
// than sent as a null the API would have to interpret.
func TestAwsCreatePayloadOmitsUnsetPrefix(t *testing.T) {
	plan := awsConnection()
	plan.S3Prefix = types.StringNull()

	payload := marshal(t, buildAwsConnectionCreate(&plan))

	if _, present := payload["s3_prefix"]; present {
		t.Errorf("an unset s3_prefix should be omitted, got %v", payload["s3_prefix"])
	}
}

// SELECT re-validates against S3 whenever payer_account_id, s3_bucket,
// s3_prefix, region or credentials are present in the patch — present, not
// changed. A rename must therefore leave all five out, or renaming a connection
// would depend on S3 being reachable.
func TestAwsUpdatePayloadOmitsUnchangedAccessFields(t *testing.T) {
	state := awsConnection()
	plan := awsConnection()
	plan.Name = types.StringValue("Acme Production (EU)")

	payload := marshal(t, buildAwsConnectionUpdate(&plan, &state))

	if payload["name"] != "Acme Production (EU)" {
		t.Errorf("the changed name should be sent, got %v", payload["name"])
	}
	for _, field := range []string{
		"payer_account_id", "s3_bucket", "s3_prefix", "region", "credentials", "sync_enabled",
	} {
		if _, present := payload[field]; present {
			t.Errorf("%s did not change and should have been omitted, got %v", field, payload[field])
		}
	}
}

func TestAwsUpdatePayloadSendsChangedFields(t *testing.T) {
	state := awsConnection()
	plan := awsConnection()
	plan.Region = types.StringValue("eu-west-1")
	plan.Credentials = awsCredentials("AKIAEXAMPLETWO", "secret-two")
	plan.SyncEnabled = types.BoolValue(false)

	payload := marshal(t, buildAwsConnectionUpdate(&plan, &state))

	if payload["region"] != "eu-west-1" {
		t.Errorf("the changed region should be sent, got %v", payload["region"])
	}
	credentials, ok := payload["credentials"].(map[string]any)
	if !ok {
		t.Fatalf("rotated credentials should be sent as an object, got %v", payload["credentials"])
	}
	if credentials["access_key_id"] != "AKIAEXAMPLETWO" || credentials["secret_access_key"] != "secret-two" {
		t.Errorf("a rotation should move both halves together, got %v", credentials)
	}
	// A pointer to false is still a value: omitempty must not swallow it.
	if payload["sync_enabled"] != false {
		t.Errorf("sync_enabled should be sent as false, got %v", payload["sync_enabled"])
	}
	if _, present := payload["name"]; present {
		t.Errorf("name did not change and should have been omitted")
	}
}

// s3_prefix is the one field the API lets a caller clear, and the only way to
// say so is an explicit null — omitting it means "leave the prefix alone". Every
// other field has to stay absent, since the API rejects a null for each of them
// outright.
func TestAwsUpdatePayloadClearsPrefixWithExplicitNull(t *testing.T) {
	state := awsConnection()
	plan := awsConnection()
	plan.S3Prefix = types.StringNull()

	payload := marshal(t, buildAwsConnectionUpdate(&plan, &state))

	value, present := payload["s3_prefix"]
	if !present {
		t.Fatalf("clearing the prefix has to reach the API as an explicit null, but the field was omitted")
	}
	if value != nil {
		t.Errorf("s3_prefix should be null, got %v", value)
	}
	for _, field := range []string{"name", "payer_account_id", "s3_bucket", "region", "credentials", "sync_enabled"} {
		if _, present := payload[field]; present {
			t.Errorf("%s did not change and should have been omitted, got %v", field, payload[field])
		}
	}
}

// A patch that clears nothing must contain no nulls at all: the API rejects an
// explicit null for every field but s3_prefix.
func TestAwsUpdatePayloadNeverSendsNull(t *testing.T) {
	state := awsConnection()
	plan := awsConnection()
	plan.Name = types.StringValue("Acme Production (EU)")

	payload := marshal(t, buildAwsConnectionUpdate(&plan, &state))

	for field, value := range payload {
		if value == nil {
			t.Errorf("%s was sent as null, which the API rejects", field)
		}
	}
}

// SELECT returns neither half of the key pair, so a response can only ever blank
// the credentials block. What the configuration says is the whole record of it.
func TestAwsApplyResponseKeepsCredentialsFromState(t *testing.T) {
	state := awsConnection()
	model := awsConnection()

	response := &awsConnectionResponse{
		Id:             "2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01",
		Etag:           `"def456"`,
		Name:           "Acme Production",
		ConnectionId:   "8b2d1f0a-3c4e-4a5b-9c6d-7e8f9a0b1c2d",
		PayerAccountId: "123456789012",
		S3Bucket:       "acme-cur",
		Region:         "us-east-1",
		SyncEnabled:    true,
		CreateTime:     "2025-01-01T00:00:00Z",
		UpdateTime:     "2025-01-02T00:00:00Z",
	}

	if diags := applyAwsConnectionResponse(context.Background(), &model, &state, response); diags.HasError() {
		t.Fatalf("applying the response: %v", diags)
	}

	if !model.Credentials.Equal(state.Credentials) {
		t.Errorf("credentials should be carried over from state, got %v", model.Credentials)
	}
	if model.Etag.ValueString() != `"def456"` {
		t.Errorf("the response's ETag should replace the one in state, got %v", model.Etag)
	}
	// A report moved to the root of the bucket comes back with no prefix, and
	// state has to follow rather than keep the old one.
	if !model.S3Prefix.IsNull() {
		t.Errorf("a null s3_prefix in the response should clear it in state, got %v", model.S3Prefix)
	}
}

// A failing check is the most actionable thing the API can tell us, so it should
// reach the user ahead of the status code.
func TestAwsValidationReportBecomesADiagnostic(t *testing.T) {
	body := `{
		"title": "Validation failed",
		"status": 422,
		"detail": "Cannot access S3 bucket 'acme-cur': AccessDenied",
		"code": "validation_failed",
		"details": [{"field": "body", "issue": "check_failed:bucket_access"}],
		"validation_report": {
			"success": false,
			"checks": [
				{"id": "bucket_access", "label": "Reach the S3 bucket", "status": "failed",
				 "message": "Cannot access S3 bucket 'acme-cur': AccessDenied"},
				{"id": "cur_manifest_discovery", "label": "Find a CUR manifest under the prefix",
				 "status": "skipped", "message": "Not run because an earlier check failed."}
			]
		}
	}`

	detail := awsConnectionAPIDiagnostic("add the AWS connection", newAPIError(422, body)).Detail()

	if !strings.Contains(detail, "AccessDenied") {
		t.Errorf("diagnostic should name the failing check's remedy, got: %s", detail)
	}
	if !strings.Contains(detail, "Not run because an earlier check failed.") {
		t.Errorf("diagnostic should include skipped checks, got: %s", detail)
	}
}

// The ETag contract and the API key's scopes are the two failures a user is
// least likely to diagnose unaided.
func TestAwsPreconditionAndScopeDiagnostics(t *testing.T) {
	stale := awsConnectionAPIDiagnostic("update the AWS connection",
		newAPIError(412, `{"detail":"The If-Match header does not match the resource's current ETag.","code":"precondition_failed"}`))
	if !strings.Contains(stale.Detail(), "-refresh-only") {
		t.Errorf("a stale ETag should tell the user how to recover, got: %s", stale.Detail())
	}

	missing := awsConnectionAPIDiagnostic("delete the AWS connection",
		newAPIError(428, `{"detail":"This is a configurable resource; If-Match is required for writes.","code":"precondition_required"}`))
	if !strings.Contains(missing.Detail(), "-refresh-only") {
		t.Errorf("a missing ETag should tell the user how to recover, got: %s", missing.Detail())
	}

	forbidden := awsConnectionAPIDiagnostic("add the AWS connection",
		newAPIError(403, `{"detail":"This caller lacks the aws_accounts:write scope.","code":"forbidden"}`))
	if !strings.Contains(forbidden.Detail(), "aws_accounts:write") {
		t.Errorf("a scope failure should name the scopes needed, got: %s", forbidden.Detail())
	}
}

// Both AWS conflicts arrive as already_connected, so unlike Databricks the field
// rather than the issue is what tells them apart — and they have different
// remedies. A duplicate payer account can only be imported; a duplicate report
// location can also be moved.
func TestAwsConflictDiagnosticsDifferByField(t *testing.T) {
	payer := awsConnectionAPIDiagnostic("add the AWS connection", newAPIError(409, `{
		"detail": "AWS payer account '123456789012' is already connected via 'Acme'.",
		"code": "conflict",
		"details": [{"field": "payer_account_id", "issue": "already_connected"}]
	}`))
	if !strings.Contains(payer.Detail(), "terraform import") {
		t.Errorf("an already-connected payer account should point at import, got: %s", payer.Detail())
	}

	bucket := awsConnectionAPIDiagnostic("add the AWS connection", newAPIError(409, `{
		"detail": "S3 location s3://acme-cur/reports is already connected via 'Acme'.",
		"code": "conflict",
		"details": [{"field": "s3_bucket", "issue": "already_connected"}]
	}`))
	if bucket.Summary() == payer.Summary() {
		t.Errorf("the two conflicts should not read as the same failure, both said: %s", bucket.Summary())
	}
	if !strings.Contains(bucket.Detail(), "report location") {
		t.Errorf("a duplicate report location should say so, got: %s", bucket.Detail())
	}
}

// A third conflict cause, added to the API after this resource was first
// built: two connections cannot share a display name. It carries its own
// issue, unlike the other two, but still has to be told apart by field —
// nothing here should assume only two field values are possible.
func TestAwsConflictDiagnosticNamesDuplicateConnectionName(t *testing.T) {
	name := awsConnectionAPIDiagnostic("add the AWS connection", newAPIError(409, `{
		"detail": "A connection named 'Acme Production' already exists.",
		"code": "conflict",
		"details": [{"field": "name", "issue": "name_already_exists"}]
	}`))
	if strings.Contains(name.Detail(), "payer account") || strings.Contains(name.Detail(), "report location") {
		t.Errorf("a duplicate name should not be misreported as a duplicate payer account or report location, got: %s", name.Detail())
	}
	if !strings.Contains(name.Detail(), "unique") {
		t.Errorf("a duplicate name should explain that names must be unique, got: %s", name.Detail())
	}
}

// The credential store is something SELECT reaches on every write, so its being
// down says nothing about the configuration and the remedy is to apply again.
func TestAwsCredentialStoreUnavailableIsRetryable(t *testing.T) {
	unavailable := awsConnectionAPIDiagnostic("update the AWS connection",
		newAPIError(503, `{"detail":"The credential store is temporarily unavailable.","code":"service_unavailable"}`))

	if !strings.Contains(unavailable.Detail(), "again") {
		t.Errorf("a transient failure should tell the user to retry, got: %s", unavailable.Detail())
	}
}
