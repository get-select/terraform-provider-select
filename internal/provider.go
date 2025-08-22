// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = (*selectProvider)(nil)

type ProviderModel struct {
	ApiKey         types.String `tfsdk:"api_key"`
	OrganizationId types.String `tfsdk:"organization_id"`
	ApiURL         types.String `tfsdk:"select_api_url"`
}

type ProviderData struct {
	Client *APIClient
}

func New() func() provider.Provider {
	return func() provider.Provider {
		return &selectProvider{}
	}
}

type selectProvider struct{}

func (p *selectProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "API key for authentication with the Select API",
			},
			"organization_id": schema.StringAttribute{
				Required:    true,
				Description: "Organization ID for the Select API",
			},
			"select_api_url": schema.StringAttribute{
				Optional:    true,
				Description: "Base URL for the Select API",
			},
		},
	}
}

func (p *selectProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config ProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if config.ApiKey.IsNull() || config.ApiKey.IsUnknown() {
		resp.Diagnostics.AddError(
			"Missing API Key",
			"The provider requires an api_key to be configured.",
		)
		return
	}

	apiKey := config.ApiKey.ValueString()
	if apiKey == "" {
		resp.Diagnostics.AddError(
			"Empty API Key",
			"The api_key cannot be empty.",
		)
		return
	}

	if config.OrganizationId.IsNull() || config.OrganizationId.IsUnknown() {
		resp.Diagnostics.AddError(
			"Missing Organization ID",
			"The provider requires an organization_id to be configured.",
		)
		return
	}

	organizationId := config.OrganizationId.ValueString()
	if organizationId == "" {
		resp.Diagnostics.AddError(
			"Empty Organization ID",
			"The organization_id cannot be empty.",
		)
		return
	}

	apiURL := config.ApiURL.ValueString()
	if apiURL == "" {
		apiURL = "https://api.select.dev"
	}

	client := NewAPIClient(apiKey, organizationId, apiURL)

	providerData := &ProviderData{
		Client: client,
	}

	resp.ResourceData = providerData
	resp.DataSourceData = providerData
}

func (p *selectProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "select"
}

func (p *selectProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

func (p *selectProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewUsageGroupSetResource,
		NewUsageGroupResource,
	}
}
