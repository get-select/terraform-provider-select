// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Alert destinations live on the v2 API. The create, update and response
// bodies are oneOf unions with the discriminator integration_type, so the
// generator cannot produce them. Each destination type is its own resource,
// and all three share the code in this file and in
// alert_destination_resource.go.
const destinationsEndpoint = "/v2/destinations"

func destinationEndpoint(id string) string {
	return fmt.Sprintf("%s/%s", destinationsEndpoint, id)
}

// alertDestinationErrors words the failures of one destination type.
func alertDestinationErrors(noun string) v2ErrorFormat {
	return v2ErrorFormat{
		Noun:       noun,
		Subject:    "the destination",
		Object:     "the destination",
		ReadScope:  "alert_destinations:read",
		WriteScope: "alert_destinations:write",
	}
}

// deleteBlockedByDependencies is the problem code of a delete that the API
// refuses because something still refers to the item.
const deleteBlockedByDependencies = "delete_blocked_by_dependencies"

// alertDestinationConflict explains a delete that a monitor or a dashboard
// digest blocks.
func alertDestinationConflict(noun string) v2Diagnostic {
	return func(operation string, apiErr *apiError) diag.Diagnostic {
		if apiErr.StatusCode != http.StatusConflict || apiErr.Code != deleteBlockedByDependencies {
			return nil
		}
		return diag.NewErrorDiagnostic(
			noun+" In Use",
			fmt.Sprintf("SELECT could not %s because a monitor or a dashboard digest still sends to it. "+
				"Remove the destination from those monitors and digests first.\n\n%s",
				operation, apiErr.Detail),
		)
	}
}

// alertDestinationCommon holds the attributes that every destination type
// has. Each destination model embeds it. One shared model for all three types
// is not possible: the framework fails when a tfsdk tag has no attribute in
// the schema.
type alertDestinationCommon struct {
	Id            types.String `tfsdk:"id"`
	Etag          types.String `tfsdk:"etag"`
	IntegrationId types.String `tfsdk:"integration_id"`
	Status        types.String `tfsdk:"status"`
	ErrorMessage  types.String `tfsdk:"error_message"`
	LastUsedTime  types.String `tfsdk:"last_used_time"`
	CreateTime    types.String `tfsdk:"create_time"`
	UpdateTime    types.String `tfsdk:"update_time"`
}

// alertDestinationSettings holds the type-specific fields of a destination.
// A field that the type does not have is null.
type alertDestinationSettings struct {
	Channel      types.String
	EmailAddress types.String
	WebhookUrl   types.String
}

// alertDestinationPayload holds the fields of every create and update
// variant. A nil field is omitted. No destination field accepts null.
type alertDestinationPayload struct {
	IntegrationType string  `json:"integration_type"`
	IntegrationId   *string `json:"integration_id,omitempty"`
	Channel         *string `json:"channel,omitempty"`
	EmailAddress    *string `json:"email_address,omitempty"`
	WebhookUrl      *string `json:"webhook_url,omitempty"`
}

// alertDestinationResponse holds the fields of every response variant.
type alertDestinationResponse struct {
	Id                   string  `json:"id"`
	Etag                 string  `json:"etag"`
	IntegrationId        string  `json:"integration_id"`
	IntegrationType      string  `json:"integration_type"`
	Status               string  `json:"status"`
	ErrorMessage         *string `json:"error_message"`
	LastUsedTime         *string `json:"last_used_time"`
	CreateTime           string  `json:"create_time"`
	UpdateTime           string  `json:"update_time"`
	Channel              *string `json:"channel"`
	EmailAddress         *string `json:"email_address"`
	WebhookUrlUpdateTime *string `json:"webhook_url_update_time"`
}

// buildAlertDestinationCreate builds a create body.
func buildAlertDestinationCreate(integrationType string, common *alertDestinationCommon, settings alertDestinationSettings) *alertDestinationPayload {
	return &alertDestinationPayload{
		IntegrationType: integrationType,
		IntegrationId:   stringPointer(common.IntegrationId),
		Channel:         stringPointer(settings.Channel),
		EmailAddress:    stringPointer(settings.EmailAddress),
		WebhookUrl:      stringPointer(settings.WebhookUrl),
	}
}

// buildAlertDestinationUpdate builds a merge patch with only the changed
// fields. The API requires integration_type in every update. integration_id
// forces a new resource, so it is never in an update.
func buildAlertDestinationUpdate(integrationType string, plan, state alertDestinationSettings) *alertDestinationPayload {
	return &alertDestinationPayload{
		IntegrationType: integrationType,
		Channel:         changedString(plan.Channel, state.Channel),
		EmailAddress:    changedString(plan.EmailAddress, state.EmailAddress),
		WebhookUrl:      changedString(plan.WebhookUrl, state.WebhookUrl),
	}
}

// applyAlertDestinationCommon writes the shared response fields onto a model.
// It returns an error when the response is a destination of a different
// type, which happens when an import uses the id of a different type of
// destination.
func applyAlertDestinationCommon(noun, integrationType string, model *alertDestinationCommon, response *alertDestinationResponse) diag.Diagnostics {
	if response.IntegrationType != integrationType {
		return diag.Diagnostics{diag.NewErrorDiagnostic(
			"Wrong "+noun+" Type",
			fmt.Sprintf("Destination %q has integration_type %q, not %q. "+
				"Use the resource for that type of destination.",
				response.Id, response.IntegrationType, integrationType),
		)}
	}

	model.Id = types.StringValue(response.Id)
	model.Etag = types.StringValue(response.Etag)
	model.IntegrationId = types.StringValue(response.IntegrationId)
	model.Status = types.StringValue(response.Status)
	model.ErrorMessage = stringValue(response.ErrorMessage)
	model.LastUsedTime = stringValue(response.LastUsedTime)
	model.CreateTime = types.StringValue(response.CreateTime)
	model.UpdateTime = types.StringValue(response.UpdateTime)
	return nil
}
