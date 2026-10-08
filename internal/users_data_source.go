// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// usersErrors words the failures of the users list. The API key scope name
// follows the route's "users" tag; the spec does not name it.
var usersErrors = v2ErrorFormat{
	Noun:       "Users",
	Subject:    "the users",
	Object:     "the users",
	ReadScope:  "users:read",
	WriteScope: "users:write",
}

// usersDataSourceModel is the select_users data source's shape. It is
// hand-written: the generator makes a data source from a GET-by-id route,
// and this one lists all users.
type usersDataSourceModel struct {
	Emails types.Set  `tfsdk:"emails"`
	Users  types.List `tfsdk:"users"`
}

// userDataSourceModel is one element of the users list.
type userDataSourceModel struct {
	Id                 types.String `tfsdk:"id"`
	Email              types.String `tfsdk:"email"`
	Name               types.String `tfsdk:"name"`
	IdentityProvider   types.String `tfsdk:"identity_provider"`
	LastLoginSsoGroups types.List   `tfsdk:"last_login_sso_groups"`
	LastLoginTime      types.String `tfsdk:"last_login_time"`
}

// userResponse mirrors the UserV2 fields the data source exports.
type userResponse struct {
	Id                 string   `json:"id"`
	Name               *string  `json:"name"`
	EmailAddress       string   `json:"email_address"`
	IdentityProvider   string   `json:"identity_provider"`
	LastLoginSsoGroups []string `json:"last_login_sso_groups"`
	LastLoginTime      *string  `json:"last_login_time"`
}

func userAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"id":                    types.StringType,
		"email":                 types.StringType,
		"name":                  types.StringType,
		"identity_provider":     types.StringType,
		"last_login_sso_groups": types.ListType{ElemType: types.StringType},
		"last_login_time":       types.StringType,
	}
}

func NewUsersDataSource() datasource.DataSource {
	return &v2DataSource[usersDataSourceModel]{
		typeNameSuffix: "_users",
		schema:         usersDataSourceSchema,
		read:           readUsers,
	}
}

func usersDataSourceSchema(ctx context.Context) schema.Schema {
	return schema.Schema{
		Description: "Lists the users of the organization. Use it to check that an email address " +
			"belongs to a user before you grant a role to it.",
		Attributes: map[string]schema.Attribute{
			"emails": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "The email addresses to find. The match ignores case. If you omit it, the " +
					"data source returns all users. An email address that belongs to no user does not " +
					"cause an error: it is not in `users`. An empty set returns no users.",
			},
			"users": schema.ListNestedAttribute{
				Computed: true,
				Description: "The users that were found, sorted by email address. A person who has not " +
					"signed in to SELECT yet can be absent from the list.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:    true,
							Description: "The user's identifier in SELECT.",
						},
						"email": schema.StringAttribute{
							Computed:    true,
							Description: "The user's email address, as SELECT stores it.",
						},
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "The user's display name. Null if SELECT has none.",
						},
						"identity_provider": schema.StringAttribute{
							Computed:    true,
							Description: "The identity provider the user signs in through.",
						},
						"last_login_sso_groups": schema.ListAttribute{
							Computed:    true,
							ElementType: types.StringType,
							Description: "The SSO groups the user belonged to at their most recent login. Null if SELECT has no record.",
						},
						"last_login_time": schema.StringAttribute{
							Computed:    true,
							Description: "When the user last signed in — RFC 3339 UTC. Null if SELECT has no record.",
						},
					},
				},
			},
		},
	}
}

// readUsers lists every user on every page and keeps those that the emails
// filter selects. The API has no filter parameters, so the filter is applied
// here.
func readUsers(ctx context.Context, client *APIClient, model *usersDataSourceModel) diag.Diagnostics {
	wanted, diags := usersEmailFilter(ctx, model.Emails)
	if diags.HasError() {
		return diags
	}

	all, apiErr, listDiags := v2ListAll[userResponse](ctx, client, usersEndpoint, nil)
	diags.Append(listDiags...)
	if diags.HasError() {
		return diags
	}
	if apiErr != nil {
		return append(diags, usersErrors.diagnostic("list users", apiErr, nil))
	}

	users, valueDiags := usersValue(ctx, filterUsers(all, wanted))
	diags.Append(valueDiags...)
	if diags.HasError() {
		return diags
	}
	model.Users = users
	return diags
}

// usersEmailFilter returns the configured email addresses in lower case. It
// returns nil when the filter is null, which selects all users. An empty set
// gives an empty, non-nil map, which selects no users.
func usersEmailFilter(ctx context.Context, emails types.Set) (map[string]bool, diag.Diagnostics) {
	if emails.IsNull() || emails.IsUnknown() {
		return nil, nil
	}
	var values []string
	diags := emails.ElementsAs(ctx, &values, false)
	if diags.HasError() {
		return nil, diags
	}
	wanted := make(map[string]bool, len(values))
	for _, email := range values {
		wanted[strings.ToLower(email)] = true
	}
	return wanted, diags
}

// filterUsers keeps the users whose email address is in wanted, compared in
// lower case. A nil wanted keeps all users. The result is sorted by email
// address, then by id, so that the order does not depend on the API.
func filterUsers(users []userResponse, wanted map[string]bool) []userResponse {
	kept := []userResponse{}
	for _, user := range users {
		if wanted == nil || wanted[strings.ToLower(user.EmailAddress)] {
			kept = append(kept, user)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := strings.ToLower(kept[i].EmailAddress), strings.ToLower(kept[j].EmailAddress)
		if a != b {
			return a < b
		}
		return kept[i].Id < kept[j].Id
	})
	return kept
}

// usersValue converts the API users to the users attribute.
func usersValue(ctx context.Context, users []userResponse) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics
	elements := make([]userDataSourceModel, len(users))
	for i, user := range users {
		groups := types.ListNull(types.StringType)
		if user.LastLoginSsoGroups != nil {
			value, d := types.ListValueFrom(ctx, types.StringType, user.LastLoginSsoGroups)
			diags.Append(d...)
			groups = value
		}
		elements[i] = userDataSourceModel{
			Id:                 types.StringValue(user.Id),
			Email:              types.StringValue(user.EmailAddress),
			Name:               types.StringPointerValue(user.Name),
			IdentityProvider:   types.StringValue(user.IdentityProvider),
			LastLoginSsoGroups: groups,
			LastLoginTime:      types.StringPointerValue(user.LastLoginTime),
		}
	}
	if diags.HasError() {
		return types.ListNull(types.ObjectType{AttrTypes: userAttrTypes()}), diags
	}
	value, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: userAttrTypes()}, elements)
	diags.Append(d...)
	return value, diags
}
