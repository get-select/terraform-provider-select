// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"terraform-provider-select/internal/provider/resource_bigquery_connection"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func NewBigQueryConnectionResource() resource.Resource {
	return &v2Resource[resource_bigquery_connection.BigqueryConnectionModel, bigQueryConnectionResponse]{
		typeNameSuffix:     "_bigquery_connection",
		schema:             resource_bigquery_connection.BigqueryConnectionResourceSchema,
		errors:             bigQueryConnectionErrors,
		specificDiagnostic: bigQueryConnectionSpecificDiagnostic,
		collectionEndpoint: bigQueryConnectionsEndpoint,
		itemEndpoint:       bigQueryConnectionEndpoint,
		identity: func(m *resource_bigquery_connection.BigqueryConnectionModel) v2Identity {
			return v2Identity{Id: m.Id, Etag: m.Etag}
		},
		createPayload: v2Payload(buildBigQueryConnectionCreate),
		updatePayload: v2Patch(buildBigQueryConnectionUpdate),
		applyResponse: applyBigQueryConnectionResponse,
	}
}
