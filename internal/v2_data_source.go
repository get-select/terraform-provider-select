// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
)

// v2DataSource implements a v2 data source's Metadata, Schema, Configure and
// Read once, the same way v2Resource does for resources. Each data source's
// constructor gives it a schema and a lookup function. A lookup that lists
// rather than GETs uses v2ListAll or v2FindInList from v2_list.go.
type v2DataSource[TModel any] struct {
	client *APIClient

	// typeNameSuffix names the data source for Terraform, e.g. "_team".
	typeNameSuffix string
	schema         func(ctx context.Context) schema.Schema
	// read finds the object the configuration describes and writes it onto
	// model, which holds the configuration when read is called.
	read func(ctx context.Context, client *APIClient, model *TModel) diag.Diagnostics
}

var _ datasource.DataSource = (*v2DataSource[struct{}])(nil)
var _ datasource.DataSourceWithConfigure = (*v2DataSource[struct{}])(nil)

func (d *v2DataSource[TModel]) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	client, diags := apiClientFromProviderData("Data Source", req.ProviderData)
	resp.Diagnostics.Append(diags...)
	if client == nil {
		return
	}
	d.client = client
}

func (d *v2DataSource[TModel]) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + d.typeNameSuffix
}

func (d *v2DataSource[TModel]) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = d.schema(ctx)
}

func (d *v2DataSource[TModel]) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model TModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(d.read(ctx, d.client, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
