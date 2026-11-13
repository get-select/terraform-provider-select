// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

func NewAlertIntegrationResource() resource.Resource {
	return &v2Resource[alertIntegrationModel, alertIntegrationResponse]{
		typeNameSuffix:     "_alert_integration",
		schema:             alertIntegrationResourceSchema,
		errors:             alertIntegrationErrors,
		specificDiagnostic: alertIntegrationConflict,
		collectionEndpoint: func(m *alertIntegrationModel) string {
			return alertIntegrationEnableEndpoints[m.IntegrationType.ValueString()]
		},
		itemEndpoint: func(m *alertIntegrationModel) string {
			return alertIntegrationEndpoint(m.Id.ValueString())
		},
		identity: func(m *alertIntegrationModel) v2Identity {
			return v2Identity{Id: m.Id, Etag: m.Etag}
		},
		// The enable actions take no body.
		createPayload: func(context.Context, *alertIntegrationModel) (any, diag.Diagnostics) {
			return nil, nil
		},
		updatePayload: v2NoUpdate[alertIntegrationModel]("Alert Integration"),
		// The import ID carries the type, so Read can check that the id
		// names an integration of that type.
		importState: v2ImportChild("Alert Integration", "integration_type", "integration_type/alert_integration_id"),
		applyResponse: func(ctx context.Context, model, source *alertIntegrationModel, response *alertIntegrationResponse) diag.Diagnostics {
			// A mismatch would plan to replace the integration, and the
			// replace deletes an integration this resource does not manage.
			want := source.IntegrationType.ValueString()
			if known(source.IntegrationType) && response.IntegrationType != want {
				return diag.Diagnostics{diag.NewErrorDiagnostic(
					"Wrong Alert Integration Type",
					fmt.Sprintf("Alert integration %q has integration_type %q, not %q.",
						response.Id, response.IntegrationType, want),
				)}
			}
			if _, ok := alertIntegrationEnableEndpoints[response.IntegrationType]; !ok {
				return diag.Diagnostics{diag.NewErrorDiagnostic(
					"Unsupported Alert Integration Type",
					fmt.Sprintf("Alert integration %q has integration_type %q, which this resource cannot manage. "+
						"Use the select_alert_integration data source to refer to it.",
						response.Id, response.IntegrationType),
				)}
			}
			applyAlertIntegrationResponse(model, response)
			return nil
		},
	}
}

func alertIntegrationResourceSchema(ctx context.Context) schema.Schema {
	enableable := slices.Sorted(maps.Keys(alertIntegrationEnableEndpoints))

	return schema.Schema{
		Description: "Enables an email or MS Teams webhook alert integration for the organization. " +
			"An organization has at most one integration of each type. When one is already " +
			"enabled, create adopts it. Destroy disables the integration for the full " +
			"organization, and SELECT refuses it while the integration has destinations. A " +
			"Slack integration needs a browser sign-in: connect it in the SELECT UI and use " +
			"the `select_alert_integration` data source.",
		Attributes: map[string]schema.Attribute{
			"integration_type": schema.StringAttribute{
				Required:    true,
				Description: "`email` or `ms_teams_webhook`. Changing it forces a new integration.",
				Validators: []validator.String{
					stringvalidator.OneOf(enableable...),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The integration's identifier in SELECT. Use it with `terraform import`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"etag": schema.StringAttribute{
				Computed:    true,
				Description: "Opaque strong ETag for this integration. Sent as `If-Match` on deletes.",
			},
			"display_name": schema.StringAttribute{
				Computed:    true,
				Description: "The integration's name, as SELECT shows it.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "The status of SELECT's connection: `unknown`, `pending`, `failing` or `connected`.",
			},
			"credentials_configured": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether SELECT holds credentials for this integration.",
			},
			"create_time": schema.StringAttribute{
				Computed:    true,
				Description: "When the integration was connected — RFC 3339 UTC.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"update_time": schema.StringAttribute{
				Computed:    true,
				Description: "When the integration was last changed — RFC 3339 UTC.",
			},
		},
	}
}
