// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"terraform-provider-select/internal/provider/resource_usage_group_set"
)

// Usage group sets live on the v2 API. See v2_api.go for the conventions every
// resource on that surface shares.
const usageGroupSetsEndpoint = "/v2/usage-group-sets"

func usageGroupSetEndpoint(id string) string {
	return fmt.Sprintf("%s/%s", usageGroupSetsEndpoint, id)
}

// usageGroupSetVersionsEndpoint is the set's version collection. A POST here
// records the set's current groups as a new version; see
// APIClient.GetOrCreateVersion.
func usageGroupSetVersionsEndpoint(id string) string {
	return fmt.Sprintf("%s/versions", usageGroupSetEndpoint(id))
}

// usageGroupSetErrors words the failures every v2 resource can hit. A set has
// no resource-specific conflict — SELECT allows two sets to share a name — so
// there is no specificDiagnostic for this resource.
var usageGroupSetErrors = v2ErrorFormat{
	Noun:       "Usage Group Set",
	Subject:    "the usage group set",
	Object:     "the usage group set",
	Plural:     "usage group sets",
	ReadScope:  "usage_groups:read",
	WriteScope: "usage_groups:write",
}

// usageGroupSetCreatePayload mirrors UsageGroupSetCreateV2. team_id and public
// are omitted rather than sent as null, which for a create means the same thing.
type usageGroupSetCreatePayload struct {
	Name   string  `json:"name"`
	Order  int64   `json:"order"`
	TeamId *string `json:"team_id,omitempty"`
	Public *bool   `json:"public,omitempty"`
}

// usageGroupSetUpdatePayload mirrors UsageGroupSetUpdateV2, a JSON Merge Patch
// body where an omitted field is left unchanged.
//
// version is deliberately absent. UsageGroupSetUpdateV2 still carries it, as a
// deprecated way to snapshot a version by incrementing the count, but this
// provider records versions through POST .../versions instead — see
// APIClient.GetOrCreateVersion.
//
// team_id is the one field here the API lets a caller clear, so it uses
// *nullableString for the three-state omit/null/value a plain pointer cannot
// express. name, order and public are sent only when changed.
type usageGroupSetUpdatePayload struct {
	Name   *string         `json:"name,omitempty"`
	Order  *int64          `json:"order,omitempty"`
	Public *bool           `json:"public,omitempty"`
	TeamId *nullableString `json:"team_id,omitempty"`
}

// usageGroupSetResponse mirrors UsageGroupSetV2.
type usageGroupSetResponse struct {
	Id                  string  `json:"id"`
	Etag                string  `json:"etag"`
	Name                string  `json:"name"`
	Order               int64   `json:"order"`
	TeamId              *string `json:"team_id"`
	Public              bool    `json:"public"`
	Version             int64   `json:"version"`
	InsightsSyncPending bool    `json:"insights_sync_pending"`
}

// usageGroupSetVersionResponse mirrors UsageGroupSetVersionWithUsageGroupsV2.
// Only the identity is read: the provider records a version for the checkpoint
// it leaves behind and never refers to it again, so the groups it carries are
// not decoded.
type usageGroupSetVersionResponse struct {
	Id              string `json:"id"`
	CreateTime      string `json:"create_time"`
	CreatedBy       string `json:"created_by"`
	UsageGroupSetId string `json:"usage_group_set_id"`
}

func buildUsageGroupSetCreate(plan *resource_usage_group_set.UsageGroupSetModel) *usageGroupSetCreatePayload {
	return &usageGroupSetCreatePayload{
		Name:   plan.Name.ValueString(),
		Order:  plan.Order.ValueInt64(),
		TeamId: stringPointer(plan.TeamId),
		Public: boolPointer(plan.Public),
	}
}

// buildUsageGroupSetUpdate carries only the fields whose configured value
// differs from what state records. See usageGroupSetUpdatePayload.
func buildUsageGroupSetUpdate(plan, state *resource_usage_group_set.UsageGroupSetModel) *usageGroupSetUpdatePayload {
	return &usageGroupSetUpdatePayload{
		Name:   changedString(plan.Name, state.Name),
		Order:  changedInt64(plan.Order, state.Order),
		Public: changedBool(plan.Public, state.Public),
		TeamId: clearedString(plan.TeamId, state.TeamId),
	}
}

// applyUsageGroupSetResponse writes an API response onto the model. Everything
// comes straight from the response: a set holds no write-only value the API
// would blank, and no field of it round-trips through a representation
// Terraform would read back differently.
func applyUsageGroupSetResponse(ctx context.Context, model, source *resource_usage_group_set.UsageGroupSetModel, response *usageGroupSetResponse) diag.Diagnostics {
	model.Id = types.StringValue(response.Id)
	model.Etag = types.StringValue(response.Etag)
	model.Name = types.StringValue(response.Name)
	model.Order = types.Int64Value(response.Order)
	model.TeamId = stringValue(response.TeamId)
	model.Public = types.BoolValue(response.Public)
	model.Version = types.Int64Value(response.Version)
	model.InsightsSyncPending = types.BoolValue(response.InsightsSyncPending)

	return nil
}
