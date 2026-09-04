// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"terraform-provider-select/internal/provider/resource_aws_connection"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func NewAwsConnectionResource() resource.Resource {
	return &v2Resource[resource_aws_connection.AwsConnectionModel, awsConnectionResponse]{
		typeNameSuffix:     "_aws_connection",
		schema:             resource_aws_connection.AwsConnectionResourceSchema,
		errors:             awsConnectionErrors,
		specificDiagnostic: awsConnectionSpecificDiagnostic,
		collectionEndpoint: awsAccountsEndpoint,
		itemEndpoint:       awsConnectionEndpoint,
		identity: func(m *resource_aws_connection.AwsConnectionModel) v2Identity {
			return v2Identity{Id: m.Id, Etag: m.Etag}
		},
		createPayload: v2Payload(buildAwsConnectionCreate),
		updatePayload: v2Patch(buildAwsConnectionUpdate),
		applyResponse: applyAwsConnectionResponse,
	}
}
