// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	} else {
		normalized, diags := normalizeAPIURL(apiURL)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		apiURL = normalized
	}

	client := NewAPIClient(apiKey, organizationId, apiURL)

	providerData := &ProviderData{
		Client: client,
	}

	resp.ResourceData = providerData
	resp.DataSourceData = providerData
}

// normalizeAPIURL checks that a configured select_api_url is one the provider
// can build endpoints from, and trims a trailing slash so it behaves the same
// as a value without one.
//
// A trailing slash is a typo worth absorbing silently: left alone, it doubles
// up with an endpoint's own leading slash and the API answers the resulting
// path with a plain 404 — indistinguishable, to Read, from the resource itself
// being gone. Three other mistakes fail the same invisible way: a URL with no
// scheme; a URL that ends in "/v2", such as "http://localhost:8000/v2", because
// the endpoint constants in internal/*_api.go already supply that prefix; and a
// URL with a query or fragment, which would swallow the appended path. Each is
// worth a clear diagnostic instead of a silent, wrong request. Any other path
// is kept, so the API can be served under a proxy prefix.
func normalizeAPIURL(raw string) (string, diag.Diagnostics) {
	invalid := func(detail string) (string, diag.Diagnostics) {
		return "", diag.Diagnostics{
			diag.NewErrorDiagnostic("Invalid select_api_url", detail),
		}
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return invalid(fmt.Sprintf("%q could not be parsed as a URL: %v", raw, err))
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return invalid(fmt.Sprintf("%q must start with http:// or https://.", raw))
	}
	if parsed.Host == "" {
		return invalid(fmt.Sprintf("%q has no host.", raw))
	}
	// A literal "?" or "#" opens a query or fragment even when nothing follows
	// it, and url.URL reports an empty one the same as none at all.
	if strings.ContainsAny(raw, "?#") {
		return invalid(fmt.Sprintf(
			"%q includes a query string or fragment. Every resource appends its own path to "+
				"select_api_url, so that path would land inside the query or fragment instead.",
			raw,
		))
	}
	if strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/v2") {
		return invalid(fmt.Sprintf(
			"%q ends in %q. Every resource already builds its own \"/v2/...\" path on top of "+
				"select_api_url, so this prefix would be applied twice. Remove it, as in "+
				`"https://api.select.dev".`,
			raw, "/v2",
		))
	}

	return strings.TrimRight(raw, "/"), nil
}

func (p *selectProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "select"
}

func (p *selectProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewTeamDataSource,
		NewUsersDataSource,
	}
}

func (p *selectProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewUsageGroupSetResource,
		NewUsageGroupResource,
		NewSnowflakeAccountResource,
		NewDatabricksConnectionResource,
		NewBigQueryConnectionResource,
		NewAwsConnectionResource,
		NewBudgetResource,
		NewTeamResource,
		NewTeamMemberResource,
		NewTeamRoleResource,
		NewUserRoleResource,
		NewDefaultRoleResource,
		NewSsoGroupResource,
	}
}
