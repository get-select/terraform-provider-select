// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func scopeObject(t *testing.T, scopeType string, id types.String) types.Object {
	t.Helper()
	value, diags := types.ObjectValueFrom(context.Background(), roleGrantScopeAttrTypes(), roleGrantScopeModel{
		Type: types.StringValue(scopeType),
		Id:   id,
	})
	if diags.HasError() {
		t.Fatalf("building scope: %v", diags)
	}
	return value
}

func nullScope() types.Object {
	return types.ObjectNull(roleGrantScopeAttrTypes())
}

// entityTypeFor is the PermissionEntityType the API reports for a scope type.
func entityTypeFor(scopeType string) string {
	if scopeType == roleGrantScopeOrganization {
		return "select_organization"
	}
	return scopeType
}

// grantResponse is the scope part of a role grant response, as the API would
// send it for a grant made with the given scope.
func grantResponse(t *testing.T, body string) roleGrantScopeFields {
	t.Helper()
	// Decoded through an embedding struct, the way a grant's response struct
	// embeds roleGrantScopeFields, so the test also covers the json tags.
	var response struct {
		Id   string `json:"id"`
		Role string `json:"role"`
		roleGrantScopeFields
	}
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("decoding %s: %v", body, err)
	}
	if response.Id != "g-1" {
		t.Fatalf("the embedding struct's own fields should decode too, got id %q", response.Id)
	}
	return response.roleGrantScopeFields
}

// Every scope type the API accepts must be accepted by the schema.
func TestRoleGrantScopeTypesMatchTheAPIEnum(t *testing.T) {
	want := "organization,snowflake_organization,snowflake_account,databricks_account," +
		"databricks_connection,bigquery_connection,aws_account,tableau_site,usage_group"
	if got := strings.Join(roleGrantScopeTypes, ","); got != want {
		t.Errorf("roleGrantScopeTypes should mirror RoleGrantScopeType\n got: %s\nwant: %s", got, want)
	}
	for _, scopeType := range roleGrantScopeTypes {
		if _, ok := roleGrantEntityTypes[entityTypeFor(scopeType)]; !ok {
			t.Errorf("no entity type maps back to scope type %q", scopeType)
		}
	}
}

// The scope attribute has to work both as a resource's own attribute and
// inside the element of a set, which is how select_sso_group's roles carry it.
func TestRoleGrantScopeAttributeNestsInASetElement(t *testing.T) {
	attribute := roleGrantScopeAttribute()
	if !attribute.IsOptional() || attribute.IsRequired() {
		t.Error("scope should be optional: an omitted scope means the whole organization")
	}

	roles := schema.SetNestedAttribute{
		Required: true,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"role":  schema.StringAttribute{Required: true},
				"scope": roleGrantScopeAttribute(),
			},
		},
	}
	element := roles.GetType().(types.SetType).ElemType.(types.ObjectType)
	scopeType, ok := element.AttrTypes["scope"].(types.ObjectType)
	if !ok {
		t.Fatalf("scope should be an object inside the set element, got %T", element.AttrTypes["scope"])
	}
	if len(scopeType.AttrTypes) != 2 || scopeType.AttrTypes["type"] != types.StringType || scopeType.AttrTypes["id"] != types.StringType {
		t.Errorf("scope should hold exactly type and id, got %v", scopeType.AttrTypes)
	}
}

func TestBuildRoleGrantScopeOmitsANullScope(t *testing.T) {
	payload, diags := buildRoleGrantScope(context.Background(), nullScope())
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if payload != nil {
		t.Errorf("a null scope should be omitted, which grants on the organization, got %+v", payload)
	}

	// A create payload's scope field is a pointer with omitempty, so nil
	// leaves the key out entirely.
	body := marshal(t, struct {
		Role  string                 `json:"role"`
		Scope *roleGrantScopePayload `json:"scope,omitempty"`
	}{Role: "viewer", Scope: payload})
	if _, present := body["scope"]; present {
		t.Errorf("an omitted scope should not reach the request, got %v", body["scope"])
	}
}

func TestBuildRoleGrantScopeSendsEveryScopeType(t *testing.T) {
	for _, scopeType := range roleGrantScopeTypes {
		id := types.StringValue("res-" + scopeType)
		if scopeType == roleGrantScopeOrganization {
			id = types.StringNull()
		}

		payload, diags := buildRoleGrantScope(context.Background(), scopeObject(t, scopeType, id))
		if diags.HasError() {
			t.Fatalf("%s: unexpected diagnostics: %v", scopeType, diags)
		}
		body := marshal(t, payload)
		if body["type"] != scopeType {
			t.Errorf("%s: type should be sent as configured, got %v", scopeType, body["type"])
		}
		if scopeType == roleGrantScopeOrganization {
			if _, present := body["id"]; present {
				t.Errorf("the organization scope should send no id, got %v", body["id"])
			}
		} else if body["id"] != "res-"+scopeType {
			t.Errorf("%s: id should be sent as configured, got %v", scopeType, body["id"])
		}
	}
}

// The entity is the main source of a response's scope, and select_organization
// maps back to organization with no id.
func TestRoleGrantScopeFromResponseReadsTheEntity(t *testing.T) {
	cases := map[string]struct {
		body     string
		wantType string
		wantId   string
	}{
		"organization": {
			body:     `{"id":"g-1","entity":{"type":"select_organization","id":"org-1","display_name":"Acme"}}`,
			wantType: "organization",
		},
		"databricks account": {
			body:     `{"id":"g-1","entity":{"type":"databricks_account","id":"dbx-acct","display_name":"x"}}`,
			wantType: "databricks_account", wantId: "dbx-acct",
		},
		"databricks connection": {
			body:     `{"id":"g-1","entity":{"type":"databricks_connection","id":"dbx-conn","display_name":"x"}}`,
			wantType: "databricks_connection", wantId: "dbx-conn",
		},
		"bigquery connection": {
			body:     `{"id":"g-1","entity":{"type":"bigquery_connection","id":"bq-conn","display_name":"x"}}`,
			wantType: "bigquery_connection", wantId: "bq-conn",
		},
		"aws account": {
			body:     `{"id":"g-1","entity":{"type":"aws_account","id":"aws-1","display_name":"x"}}`,
			wantType: "aws_account", wantId: "aws-1",
		},
		"tableau site": {
			body:     `{"id":"g-1","entity":{"type":"tableau_site","id":"luid-1","display_name":"x"}}`,
			wantType: "tableau_site", wantId: "luid-1",
		},
		// For these three the flat column holds the stored value and wins
		// over the entity's id.
		"snowflake organization": {
			body:     `{"id":"g-1","snowflake_organization_name":"ACME","entity":{"type":"snowflake_organization","id":"acme","display_name":"x"}}`,
			wantType: "snowflake_organization", wantId: "ACME",
		},
		"snowflake account": {
			body:     `{"id":"g-1","snowflake_account_uuid":"uuid-col","entity":{"type":"snowflake_account","id":"uuid-entity","display_name":"x"}}`,
			wantType: "snowflake_account", wantId: "uuid-col",
		},
		"usage group": {
			body:     `{"id":"g-1","usage_group_id":"ug-col","entity":{"type":"usage_group","id":"ug-entity","display_name":"x"}}`,
			wantType: "usage_group", wantId: "ug-col",
		},
		"usage group without its column": {
			body:     `{"id":"g-1","usage_group_id":null,"entity":{"type":"usage_group","id":"ug-entity","display_name":"x"}}`,
			wantType: "usage_group", wantId: "ug-entity",
		},
	}

	for name, tc := range cases {
		scope, err := roleGrantScopeFromResponse(grantResponse(t, tc.body))
		if err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
			continue
		}
		if scope.Type != tc.wantType {
			t.Errorf("%s: type = %q, want %q", name, scope.Type, tc.wantType)
		}
		switch {
		case tc.wantId == "" && scope.Id != nil:
			t.Errorf("%s: expected no id, got %q", name, *scope.Id)
		case tc.wantId != "" && (scope.Id == nil || *scope.Id != tc.wantId):
			t.Errorf("%s: id = %v, want %q", name, scope.Id, tc.wantId)
		}
	}
}

// entity is required in every grant schema, but if it is ever absent the flat
// columns still say what the scope is.
func TestRoleGrantScopeFromResponseFallsBackToTheColumns(t *testing.T) {
	cases := map[string]struct{ body, wantType, wantId string }{
		"usage group":            {`{"id":"g-1","usage_group_id":"ug-1","snowflake_account_uuid":"u-1"}`, "usage_group", "ug-1"},
		"snowflake account":      {`{"id":"g-1","snowflake_account_uuid":"u-1","snowflake_organization_name":"o"}`, "snowflake_account", "u-1"},
		"snowflake organization": {`{"id":"g-1","snowflake_organization_name":"o"}`, "snowflake_organization", "o"},
		"organization":           {`{"id":"g-1"}`, "organization", ""},
	}
	for name, tc := range cases {
		scope, err := roleGrantScopeFromResponse(grantResponse(t, tc.body))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if scope.Type != tc.wantType {
			t.Errorf("%s: type = %q, want %q", name, scope.Type, tc.wantType)
		}
		if tc.wantId == "" && scope.Id != nil || tc.wantId != "" && (scope.Id == nil || *scope.Id != tc.wantId) {
			t.Errorf("%s: id = %v, want %q", name, scope.Id, tc.wantId)
		}
	}
}

// A role cannot be granted on a team, so an entity of type team means the API
// changed in a way this provider does not understand.
func TestRoleGrantScopeFromResponseRejectsAnUnknownEntityType(t *testing.T) {
	_, err := roleGrantScopeFromResponse(grantResponse(t, `{"id":"g-1","entity":{"type":"team","id":"t-1","display_name":"x"}}`))
	if err == nil || !strings.Contains(err.Error(), `"team"`) {
		t.Errorf("an entity of type team should be an error naming it, got %v", err)
	}

	_, diags := roleGrantScopeValue(context.Background(), nullScope(),
		grantResponse(t, `{"id":"g-1","entity":{"type":"team","id":"t-1","display_name":"x"}}`))
	if !diags.HasError() {
		t.Error("roleGrantScopeValue should report the unknown entity type as a diagnostic")
	}
}

// A configuration that omits scope means the organization, so the API's
// organization answer must keep the attribute null. Otherwise every apply
// would fail with an inconsistent result.
func TestRoleGrantScopeValueKeepsAnOmittedScopeNull(t *testing.T) {
	value, diags := roleGrantScopeValue(context.Background(), nullScope(),
		grantResponse(t, `{"id":"g-1","entity":{"type":"select_organization","id":"org-1","display_name":"Acme"}}`))
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !value.IsNull() {
		t.Errorf("an omitted organization scope should stay null, got %v", value)
	}
}

// A configuration can also say the organization explicitly, and that must
// survive the round trip as written.
func TestRoleGrantScopeValueKeepsAnExplicitOrganizationScope(t *testing.T) {
	configured := scopeObject(t, roleGrantScopeOrganization, types.StringNull())
	value, diags := roleGrantScopeValue(context.Background(), configured,
		grantResponse(t, `{"id":"g-1","entity":{"type":"select_organization","id":"org-1","display_name":"Acme"}}`))
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !value.Equal(configured) {
		t.Errorf("an explicit organization scope should round-trip, got %v", value)
	}
}

// Every scope type sent through buildRoleGrantScope and read back the way the
// API reports it must give back the configured value.
func TestRoleGrantScopeRoundTripsEveryScopeType(t *testing.T) {
	ctx := context.Background()
	for _, scopeType := range roleGrantScopeTypes {
		id := types.StringValue("res-" + scopeType)
		if scopeType == roleGrantScopeOrganization {
			id = types.StringNull()
		}
		configured := scopeObject(t, scopeType, id)

		payload, diags := buildRoleGrantScope(ctx, configured)
		if diags.HasError() {
			t.Fatalf("%s: %v", scopeType, diags)
		}

		entityId := "org-1"
		if payload.Id != nil {
			entityId = *payload.Id
		}
		fields := roleGrantScopeFields{Entity: roleGrantEntity{Type: entityTypeFor(scopeType), Id: entityId, DisplayName: "x"}}
		switch scopeType {
		case "snowflake_organization":
			fields.SnowflakeOrganizationName = payload.Id
		case "snowflake_account":
			fields.SnowflakeAccountUuid = payload.Id
		case "usage_group":
			fields.UsageGroupId = payload.Id
		}

		value, diags := roleGrantScopeValue(ctx, configured, fields)
		if diags.HasError() {
			t.Fatalf("%s: %v", scopeType, diags)
		}
		if !value.Equal(configured) {
			t.Errorf("%s: scope should round-trip, got %v, want %v", scopeType, value, configured)
		}
	}
}

// After an import there is no configured scope, so the API's answer is all
// there is.
func TestRoleGrantScopeValueTakesTheResponseWhenNothingIsConfigured(t *testing.T) {
	value, diags := roleGrantScopeValue(context.Background(), nullScope(),
		grantResponse(t, `{"id":"g-1","usage_group_id":"ug-1","entity":{"type":"usage_group","id":"ug-1","display_name":"x"}}`))
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !value.Equal(scopeObject(t, "usage_group", types.StringValue("ug-1"))) {
		t.Errorf("an imported scope should come from the response, got %v", value)
	}
}

// A Snowflake identifier can come back in a different case. The configured
// spelling has to stay, or Terraform reports an inconsistent result.
func TestRoleGrantScopeValueKeepsTheConfiguredCaseOfTheId(t *testing.T) {
	configured := scopeObject(t, "snowflake_organization", types.StringValue("acme"))
	value, diags := roleGrantScopeValue(context.Background(), configured,
		grantResponse(t, `{"id":"g-1","snowflake_organization_name":"ACME","entity":{"type":"snowflake_organization","id":"ACME","display_name":"x"}}`))
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !value.Equal(configured) {
		t.Errorf("an id that differs only in case should keep its configured spelling, got %v", value)
	}

	// A real change still shows.
	value, _ = roleGrantScopeValue(context.Background(), configured,
		grantResponse(t, `{"id":"g-1","snowflake_organization_name":"OTHER","entity":{"type":"snowflake_organization","id":"OTHER","display_name":"x"}}`))
	if value.Equal(configured) {
		t.Error("a different id should not be hidden")
	}
}

func TestValidateRoleGrantScope(t *testing.T) {
	at := path.Root("scope")
	cases := map[string]struct {
		scope   roleGrantScopeModel
		wantErr string
	}{
		"organization without id": {
			scope: roleGrantScopeModel{Type: types.StringValue("organization"), Id: types.StringNull()},
		},
		"organization with id": {
			scope:   roleGrantScopeModel{Type: types.StringValue("organization"), Id: types.StringValue("org-1")},
			wantErr: "must be unset",
		},
		"usage group with id": {
			scope: roleGrantScopeModel{Type: types.StringValue("usage_group"), Id: types.StringValue("ug-1")},
		},
		"usage group without id": {
			scope:   roleGrantScopeModel{Type: types.StringValue("usage_group"), Id: types.StringNull()},
			wantErr: `required when scope.type is "usage_group"`,
		},
		"usage group with empty id": {
			scope:   roleGrantScopeModel{Type: types.StringValue("usage_group"), Id: types.StringValue("")},
			wantErr: "required",
		},
		"id known only after apply": {
			scope: roleGrantScopeModel{Type: types.StringValue("usage_group"), Id: types.StringUnknown()},
		},
	}

	for name, tc := range cases {
		diags := validateRoleGrantScope(at, tc.scope)
		if tc.wantErr == "" {
			if diags.HasError() {
				t.Errorf("%s: unexpected error: %v", name, diags)
			}
			continue
		}
		if !diags.HasError() || !strings.Contains(diags[0].Detail(), tc.wantErr) {
			t.Errorf("%s: expected an error containing %q, got %v", name, tc.wantErr, diags)
		}
	}
}
