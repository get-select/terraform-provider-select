// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"maps"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// alertDestinationSpec is what one destination type gives to
// newAlertDestinationResource.
type alertDestinationSpec[TModel any] struct {
	// typeNameSuffix names the resource for Terraform, e.g.
	// "_email_alert_destination".
	typeNameSuffix string
	// integrationType is the API's discriminator for this type, e.g. "email".
	integrationType string
	// noun names the type in diagnostics, e.g. "Email Alert Destination".
	noun        string
	description string
	// attributes are the type's own schema attributes. The shared attributes
	// are added to them.
	attributes map[string]schema.Attribute
	// common returns the model's shared fields.
	common func(model *TModel) *alertDestinationCommon
	// settings returns the model's type-specific fields.
	settings func(model *TModel) alertDestinationSettings
	// applySettings writes the type-specific response fields onto model.
	// source is the plan or the prior state.
	applySettings func(model, source *TModel, response *alertDestinationResponse)
}

func newAlertDestinationResource[TModel any](spec alertDestinationSpec[TModel]) resource.Resource {
	errors := alertDestinationErrors(spec.noun)
	return &v2Resource[TModel, alertDestinationResponse]{
		typeNameSuffix:     spec.typeNameSuffix,
		schema:             func(context.Context) schema.Schema { return alertDestinationSchema(spec.description, spec.attributes) },
		errors:             errors,
		specificDiagnostic: alertDestinationConflict(spec.noun),
		collectionEndpoint: func(*TModel) string { return destinationsEndpoint },
		itemEndpoint: func(m *TModel) string {
			return destinationEndpoint(spec.common(m).Id.ValueString())
		},
		identity: func(m *TModel) v2Identity {
			common := spec.common(m)
			return v2Identity{Id: common.Id, Etag: common.Etag}
		},
		createPayload: func(ctx context.Context, plan *TModel) (any, diag.Diagnostics) {
			return buildAlertDestinationCreate(spec.integrationType, spec.common(plan), spec.settings(plan)), nil
		},
		updatePayload: func(ctx context.Context, plan, state *TModel) (any, diag.Diagnostics) {
			return buildAlertDestinationUpdate(spec.integrationType, spec.settings(plan), spec.settings(state)), nil
		},
		applyResponse: func(ctx context.Context, model, source *TModel, response *alertDestinationResponse) diag.Diagnostics {
			diags := applyAlertDestinationCommon(spec.noun, spec.integrationType, spec.common(model), response)
			if diags.HasError() {
				return diags
			}
			spec.applySettings(model, source, response)
			return diags
		},
	}
}

// alertDestinationSchema adds the shared attributes to a type's own
// attributes.
func alertDestinationSchema(description string, own map[string]schema.Attribute) schema.Schema {
	attributes := map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:    true,
			Description: "The destination's identifier in SELECT. Use it with `terraform import`.",
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			},
		},
		"etag": schema.StringAttribute{
			Computed: true,
			Description: "Opaque strong ETag for this destination. Sent as `If-Match` on updates and " +
				"deletes so a change made outside Terraform cannot be silently overwritten.",
		},
		"integration_id": schema.StringAttribute{
			Required: true,
			Description: "The id of the alert integration this destination sends through. Its " +
				"integration type must be the same as the destination's. Changing it forces a new destination.",
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(),
			},
		},
		"status": schema.StringAttribute{
			Computed: true,
			Description: "The delivery status: `unknown` until the first delivery, then `connected` " +
				"or `failing`, or `pending`.",
		},
		"error_message": schema.StringAttribute{
			Computed:    true,
			Description: "Why the most recent delivery failed. Null unless status is `failing`.",
		},
		"last_used_time": schema.StringAttribute{
			Computed:    true,
			Description: "When the destination last sent an alert or a test message — RFC 3339 UTC.",
		},
		"create_time": schema.StringAttribute{
			Computed:    true,
			Description: "When the destination was created — RFC 3339 UTC.",
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			},
		},
		"update_time": schema.StringAttribute{
			Computed:    true,
			Description: "When the destination was last changed — RFC 3339 UTC.",
		},
	}
	maps.Copy(attributes, own)
	return schema.Schema{
		Description: description,
		Attributes:  attributes,
	}
}

// slackAlertDestinationModel is the select_slack_alert_destination shape.
type slackAlertDestinationModel struct {
	alertDestinationCommon
	Channel types.String `tfsdk:"channel"`
}

func NewSlackAlertDestinationResource() resource.Resource {
	return newAlertDestinationResource(alertDestinationSpec[slackAlertDestinationModel]{
		typeNameSuffix:  "_slack_alert_destination",
		integrationType: "slack",
		noun:            "Slack Alert Destination",
		description: "A Slack channel that monitors send alerts to. The Slack integration must " +
			"exist first: connect it in the SELECT UI, then find it with the " +
			"`select_alert_integration` data source.",
		attributes: map[string]schema.Attribute{
			"channel": schema.StringAttribute{
				Required: true,
				Description: "The name of the Slack channel, without the leading `#`. Add the SELECT " +
					"app to a private channel before you use it.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
		},
		common: func(m *slackAlertDestinationModel) *alertDestinationCommon { return &m.alertDestinationCommon },
		settings: func(m *slackAlertDestinationModel) alertDestinationSettings {
			return alertDestinationSettings{Channel: m.Channel}
		},
		applySettings: func(model, _ *slackAlertDestinationModel, response *alertDestinationResponse) {
			model.Channel = stringValue(response.Channel)
		},
	})
}

// emailAlertDestinationModel is the select_email_alert_destination shape.
type emailAlertDestinationModel struct {
	alertDestinationCommon
	EmailAddress types.String `tfsdk:"email_address"`
}

func NewEmailAlertDestinationResource() resource.Resource {
	return newAlertDestinationResource(alertDestinationSpec[emailAlertDestinationModel]{
		typeNameSuffix:  "_email_alert_destination",
		integrationType: "email",
		noun:            "Email Alert Destination",
		description: "An email address that monitors send alerts to. It belongs to the " +
			"organization's email alert integration.",
		attributes: map[string]schema.Attribute{
			"email_address": schema.StringAttribute{
				Required:    true,
				Description: "The email address alerts are sent to.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 254),
				},
			},
		},
		common: func(m *emailAlertDestinationModel) *alertDestinationCommon { return &m.alertDestinationCommon },
		settings: func(m *emailAlertDestinationModel) alertDestinationSettings {
			return alertDestinationSettings{EmailAddress: m.EmailAddress}
		},
		applySettings: func(model, source *emailAlertDestinationModel, response *alertDestinationResponse) {
			model.EmailAddress = preserveEquivalentFold(source.EmailAddress, response.EmailAddress)
		},
	})
}

// msTeamsAlertDestinationModel is the select_ms_teams_alert_destination
// shape.
type msTeamsAlertDestinationModel struct {
	alertDestinationCommon
	Channel              types.String `tfsdk:"channel"`
	WebhookUrl           types.String `tfsdk:"webhook_url"`
	WebhookUrlUpdateTime types.String `tfsdk:"webhook_url_update_time"`
}

func NewMsTeamsAlertDestinationResource() resource.Resource {
	return newAlertDestinationResource(alertDestinationSpec[msTeamsAlertDestinationModel]{
		typeNameSuffix:  "_ms_teams_alert_destination",
		integrationType: "ms_teams_webhook",
		noun:            "MS Teams Alert Destination",
		description: "A Microsoft Teams channel that monitors send alerts to through an incoming " +
			"webhook. It belongs to the organization's MS Teams webhook alert integration.",
		attributes: map[string]schema.Attribute{
			"channel": schema.StringAttribute{
				Required: true,
				Description: "The name of the Microsoft Teams channel, as SELECT shows it. The " +
					"webhook URL, not this name, sets where alerts go.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"webhook_url": schema.StringAttribute{
				Required:  true,
				Sensitive: true,
				Description: "The Microsoft Teams incoming webhook or workflow URL that alerts are " +
					"posted to. SELECT never returns it, so Terraform cannot find a change made " +
					"to it outside Terraform.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 2083),
				},
			},
			"webhook_url_update_time": schema.StringAttribute{
				Computed:    true,
				Description: "When webhook_url was last set — RFC 3339 UTC.",
			},
		},
		common: func(m *msTeamsAlertDestinationModel) *alertDestinationCommon { return &m.alertDestinationCommon },
		settings: func(m *msTeamsAlertDestinationModel) alertDestinationSettings {
			return alertDestinationSettings{Channel: m.Channel, WebhookUrl: m.WebhookUrl}
		},
		applySettings: func(model, source *msTeamsAlertDestinationModel, response *alertDestinationResponse) {
			model.Channel = stringValue(response.Channel)
			// The API never returns the URL, so state keeps the value from
			// the plan or the prior state.
			model.WebhookUrl = source.WebhookUrl
			model.WebhookUrlUpdateTime = stringValue(response.WebhookUrlUpdateTime)
		},
	})
}
