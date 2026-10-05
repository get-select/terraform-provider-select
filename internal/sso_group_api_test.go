// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func ssoRole(role string, scope types.Object) ssoGroupRoleModel {
	return ssoGroupRoleModel{Role: types.StringValue(role), Scope: scope}
}

func ssoRolesSet(t *testing.T, roles ...ssoGroupRoleModel) types.Set {
	t.Helper()
	set, diags := types.SetValueFrom(context.Background(), ssoGroupRoleObjectType(), roles)
	if diags.HasError() {
		t.Fatalf("building roles set: %v", diags)
	}
	return set
}

func ssoRoles(t *testing.T, roles ...ssoGroupRoleModel) []ssoGroupRole {
	t.Helper()
	parsed, diags := ssoGroupRolesFromSet(context.Background(), ssoRolesSet(t, roles...))
	if diags.HasError() {
		t.Fatalf("reading roles set: %v", diags)
	}
	return parsed
}

// ssoGrants decodes a list of SsoGroupRoleGrantV2 items.
func ssoGrants(t *testing.T, items string) []ssoGroupRoleResponse {
	t.Helper()
	var grants []ssoGroupRoleResponse
	if err := json.Unmarshal([]byte(items), &grants); err != nil {
		t.Fatalf("decoding grants: %v", err)
	}
	return grants
}

// ssoRolesOf reads a roles set back into its elements, sorted by role.
func ssoRolesOf(t *testing.T, set types.Set) []ssoGroupRoleModel {
	t.Helper()
	var models []ssoGroupRoleModel
	if diags := set.ElementsAs(context.Background(), &models, false); diags.HasError() {
		t.Fatalf("reading roles set: %v", diags)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Role.ValueString() < models[j].Role.ValueString() })
	return models
}

func keysOf(roles []ssoGroupRole) []string {
	var keys []string
	for _, role := range roles {
		keys = append(keys, role.key.Role+"@"+role.key.ScopeType+":"+role.key.ScopeId)
	}
	sort.Strings(keys)
	return keys
}

const (
	orgGrant     = `{"id":"g-org","role":"admin","entity":{"type":"select_organization","id":"org","display_name":"Org"},"create_time":"x"}`
	editorGrant  = `{"id":"g-ed","role":"editor","entity":{"type":"select_organization","id":"org","display_name":"Org"},"create_time":"x"}`
	monitorGrant = `{"id":"g-mon","role":"monitor_editor","entity":{"type":"select_organization","id":"org","display_name":"Org"},"create_time":"x"}`
	groupGrant   = `{"id":"g-ug","role":"viewer","usage_group_id":"ug-1","entity":{"type":"usage_group","id":"ug-1","display_name":"Set: Group"},"create_time":"x"}`
)

func TestSsoGroupSchema(t *testing.T) {
	s := ssoGroupResourceSchema(context.Background())

	attribute, ok := s.Attributes["roles"].(schema.SetNestedAttribute)
	if !ok {
		t.Fatalf("roles should be a set nested attribute, got %T", s.Attributes["roles"])
	}
	if !attribute.Required {
		t.Error("roles should be required: the API needs at least one role to create a group")
	}
	if len(attribute.Validators) != 1 || !strings.Contains(attribute.Validators[0].Description(context.Background()), "at least 1") {
		t.Errorf("roles should hold at least one element, got validators %v", attribute.Validators)
	}
	element := attribute.NestedObject.Attributes
	if len(element) != 2 {
		t.Errorf("an element should hold only role and scope, got %v", element)
	}
	for name, a := range element {
		if a.IsComputed() {
			t.Errorf("%s is computed: a computed value in a set element is unknown in the plan and makes the element churn", name)
		}
	}

	name := s.Attributes["name"].(schema.StringAttribute)
	if !name.Required || requiresReplace(name) {
		t.Error("name should be required and change in place: SELECT renames the group")
	}
	id := s.Attributes["id"].(schema.StringAttribute)
	var followsName bool
	for _, modifier := range id.PlanModifiers {
		_, followsName = modifier.(ssoGroupIdFromName)
	}
	if !id.Computed || !followsName {
		t.Error("id should be computed and planned from name")
	}
	if _, present := s.Attributes["etag"]; !present {
		t.Error("the group has an ETag, which renames and deletes send as If-Match")
	}
}

func TestBuildSsoGroupCreate(t *testing.T) {
	roles := ssoRoles(t,
		ssoRole("admin", nullScope()),
		ssoRole("viewer", scopeObject(t, "usage_group", types.StringValue("ug-1"))),
	)

	body := marshal(t, buildSsoGroupCreate(types.StringValue("Data Team"), roles))
	if body["name"] != "Data Team" {
		t.Errorf("name should be sent as planned, got %v", body)
	}
	sent, _ := body["roles"].([]any)
	if len(sent) != 2 {
		t.Fatalf("every role should be sent in the create request, got %v", body["roles"])
	}
	var sawOrg, sawGroup bool
	for _, raw := range sent {
		role := raw.(map[string]any)
		switch role["role"] {
		case "admin":
			if _, present := role["scope"]; present {
				t.Errorf("a null scope should omit the key, which means the organization, got %v", role)
			}
			sawOrg = true
		case "viewer":
			scope, _ := role["scope"].(map[string]any)
			if scope["type"] != "usage_group" || scope["id"] != "ug-1" {
				t.Errorf("the scope should be sent as configured, got %v", role)
			}
			sawGroup = true
		}
	}
	if !sawOrg || !sawGroup {
		t.Errorf("both roles should be sent, got %v", sent)
	}
}

func TestSsoGroupUpdatePayloadCarriesOnlyTheName(t *testing.T) {
	body := marshal(t, ssoGroupUpdatePayload{Name: "New"})
	if len(body) != 1 || body["name"] != "New" {
		t.Errorf("SsoGroupUpdateV2 has only name, got %v", body)
	}
}

// Roles are compared by role and scope. A case-only change of the scope id,
// and an omitted scope versus an explicit organization scope, are the same
// grant, so they cause no grant and no revoke.
func TestDiffSsoGroupRoles(t *testing.T) {
	usageGroup := func(id string) types.Object { return scopeObject(t, "usage_group", types.StringValue(id)) }
	organization := scopeObject(t, "organization", types.StringNull())

	cases := []struct {
		name              string
		plan, state       []ssoGroupRoleModel
		wantAdd, wantDrop []string
	}{
		{
			name:  "unchanged",
			plan:  []ssoGroupRoleModel{ssoRole("admin", nullScope()), ssoRole("viewer", usageGroup("ug-1"))},
			state: []ssoGroupRoleModel{ssoRole("admin", nullScope()), ssoRole("viewer", usageGroup("ug-1"))},
		},
		{
			name:     "add one, remove one",
			plan:     []ssoGroupRoleModel{ssoRole("viewer", usageGroup("ug-1")), ssoRole("monitor_editor", nullScope())},
			state:    []ssoGroupRoleModel{ssoRole("editor", nullScope()), ssoRole("viewer", usageGroup("ug-1"))},
			wantAdd:  []string{"monitor_editor@organization:"},
			wantDrop: []string{"editor@organization:"},
		},
		{
			name:  "scope id changes only in case",
			plan:  []ssoGroupRoleModel{ssoRole("viewer", usageGroup("UG-1"))},
			state: []ssoGroupRoleModel{ssoRole("viewer", usageGroup("ug-1"))},
		},
		{
			name:  "null scope becomes an explicit organization",
			plan:  []ssoGroupRoleModel{ssoRole("admin", organization)},
			state: []ssoGroupRoleModel{ssoRole("admin", nullScope())},
		},
		{
			name:     "new scope id",
			plan:     []ssoGroupRoleModel{ssoRole("viewer", usageGroup("ug-2"))},
			state:    []ssoGroupRoleModel{ssoRole("viewer", usageGroup("ug-1"))},
			wantAdd:  []string{"viewer@usage_group:ug-2"},
			wantDrop: []string{"viewer@usage_group:ug-1"},
		},
		{
			name:     "new role on the same scope",
			plan:     []ssoGroupRoleModel{ssoRole("editor", nullScope())},
			state:    []ssoGroupRoleModel{ssoRole("admin", nullScope())},
			wantAdd:  []string{"editor@organization:"},
			wantDrop: []string{"admin@organization:"},
		},
		{
			name:    "after import, state has no roles",
			plan:    []ssoGroupRoleModel{ssoRole("admin", nullScope())},
			wantAdd: []string{"admin@organization:"},
		},
	}
	for _, c := range cases {
		var state []ssoGroupRole
		if len(c.state) > 0 {
			state = ssoRoles(t, c.state...)
		}
		add, remove := diffSsoGroupRoles(ssoRoles(t, c.plan...), state)
		if got := keysOf(add); strings.Join(got, ",") != strings.Join(c.wantAdd, ",") {
			t.Errorf("%s: add = %v, want %v", c.name, got, c.wantAdd)
		}
		if got := keysOf(remove); strings.Join(got, ",") != strings.Join(c.wantDrop, ",") {
			t.Errorf("%s: remove = %v, want %v", c.name, got, c.wantDrop)
		}
	}
}

// A removed role is matched to its grant id in the listed grants by role and
// scope, with the scope id compared without case.
func TestSsoGroupGrantsFor(t *testing.T) {
	grants := ssoGrants(t, `[`+orgGrant+`,`+groupGrant+`,
		{"id":"g-team","role":"viewer","entity":{"type":"team","id":"t-1","display_name":"Team"},"create_time":"x"}]`)

	removed := ssoRoles(t, ssoRole("viewer", scopeObject(t, "usage_group", types.StringValue("UG-1"))))[0]
	matches := ssoGroupGrantsFor(removed.key, grants)
	if len(matches) != 1 || matches[0].Id != "g-ug" {
		t.Errorf("the usage group grant should match without case, got %+v", matches)
	}

	removed = ssoRoles(t, ssoRole("admin", nullScope()))[0]
	matches = ssoGroupGrantsFor(removed.key, grants)
	if len(matches) != 1 || matches[0].Id != "g-org" {
		t.Errorf("a null scope should match the organization grant, got %+v", matches)
	}

	removed = ssoRoles(t, ssoRole("editor", nullScope()))[0]
	if matches := ssoGroupGrantsFor(removed.key, grants); len(matches) != 0 {
		t.Errorf("a role the group does not hold should match nothing, got %+v", matches)
	}
}

// Read pairs each listed grant with the configured element that has the same
// role and scope, so the configured value survives: a null scope for the
// organization, an explicit organization scope, and the configured case of a
// scope id.
func TestSsoGroupRolesValuePairsGrantsWithConfiguredRoles(t *testing.T) {
	ctx := context.Background()
	grants := ssoGrants(t, `[`+orgGrant+`,`+groupGrant+`,
		{"id":"g-ed","role":"editor","entity":{"type":"select_organization","id":"org","display_name":"Org"},"create_time":"x"}]`)

	configured := ssoRoles(t,
		ssoRole("admin", nullScope()),
		ssoRole("viewer", scopeObject(t, "usage_group", types.StringValue("UG-1"))),
		ssoRole("editor", scopeObject(t, "organization", types.StringNull())),
	)
	set, diags := ssoGroupRolesValue(ctx, configured, grants)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	got := ssoRolesOf(t, set)
	if len(got) != 3 {
		t.Fatalf("expected three roles, got %v", got)
	}
	if got[0].Role.ValueString() != "admin" || !got[0].Scope.IsNull() {
		t.Errorf("a null configured scope should stay null for the organization grant, got %v", got[0])
	}
	if !got[1].Scope.Equal(scopeObject(t, "organization", types.StringNull())) {
		t.Errorf("an explicit organization scope should stay explicit, got %v", got[1])
	}
	if !got[2].Scope.Equal(scopeObject(t, "usage_group", types.StringValue("UG-1"))) {
		t.Errorf("the scope id should keep its configured case, got %v", got[2])
	}

	// The set of the configuration and the set Read builds must be equal, or
	// every plan shows a change.
	if want := ssoRolesSet(t, ssoRole("admin", nullScope()),
		ssoRole("viewer", scopeObject(t, "usage_group", types.StringValue("UG-1"))),
		ssoRole("editor", scopeObject(t, "organization", types.StringNull()))); !set.Equal(want) {
		t.Errorf("Read should give back the configured set\n got: %v\nwant: %v", set, want)
	}
}

// After import, or for a grant made outside Terraform, there is no configured
// element. The organization is a null scope, and another scope takes the
// API's type and id.
func TestSsoGroupRolesValueWithoutConfiguredRoles(t *testing.T) {
	grants := ssoGrants(t, `[`+orgGrant+`,`+groupGrant+`,`+groupGrant+`]`)

	set, diags := ssoGroupRolesValue(context.Background(), nil, grants)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	got := ssoRolesOf(t, set)
	if len(got) != 2 {
		t.Fatalf("two grants with the same role and scope should give one element, got %v", got)
	}
	if diags.WarningsCount() != 1 || diags[0].Summary() != "Duplicate SSO Group Role" {
		t.Errorf("two grants with the same role and scope should give a warning, got %v", diags)
	}
	if got[0].Role.ValueString() != "admin" || !got[0].Scope.IsNull() {
		t.Errorf("the organization grant should have a null scope, got %v", got[0])
	}
	if !got[1].Scope.Equal(scopeObject(t, "usage_group", types.StringValue("ug-1"))) {
		t.Errorf("the usage group grant should take the API's scope, got %v", got[1])
	}
}

func TestSsoGroupRolesValueRejectsAnUnknownScopeType(t *testing.T) {
	grants := ssoGrants(t, `[{"id":"g-1","role":"viewer","entity":{"type":"team","id":"t-1","display_name":"Team"},"create_time":"x"}]`)
	if _, diags := ssoGroupRolesValue(context.Background(), nil, grants); !diags.HasError() {
		t.Error("a grant on a scope type the provider does not know should be an error")
	}
}

// Two elements that SELECT holds as one grant are rejected at plan time.
func TestValidateSsoGroupConfig(t *testing.T) {
	ctx := context.Background()
	usageGroup := func(id types.String) types.Object { return scopeObject(t, "usage_group", id) }

	cases := []struct {
		name    string
		roles   []ssoGroupRoleModel
		wantErr bool
	}{
		{"distinct roles", []ssoGroupRoleModel{ssoRole("admin", nullScope()), ssoRole("viewer", usageGroup(types.StringValue("ug-1")))}, false},
		{"same role, scope ids differ only in case", []ssoGroupRoleModel{ssoRole("viewer", usageGroup(types.StringValue("ug-1"))), ssoRole("viewer", usageGroup(types.StringValue("UG-1")))}, true},
		{"same role, null and explicit organization", []ssoGroupRoleModel{ssoRole("admin", nullScope()), ssoRole("admin", scopeObject(t, "organization", types.StringNull()))}, true},
		{"same scope, different roles", []ssoGroupRoleModel{ssoRole("admin", nullScope()), ssoRole("editor", nullScope())}, false},
		{"unknown scope ids are not compared", []ssoGroupRoleModel{ssoRole("viewer", usageGroup(types.StringUnknown())), ssoRole("viewer", usageGroup(types.StringUnknown()))}, false},
	}
	for _, c := range cases {
		config := ssoGroupModel{Roles: ssoRolesSet(t, c.roles...)}
		diags := validateSsoGroupConfig(ctx, &config)
		if diags.HasError() != c.wantErr {
			t.Errorf("%s: error = %v, want %v (%v)", c.name, diags.HasError(), c.wantErr, diags)
			continue
		}
		if c.wantErr {
			if d, ok := diags[0].(interface{ Path() path.Path }); !ok || !d.Path().Equal(path.Root("roles")) {
				t.Errorf("%s: the error should point at roles, got %v", c.name, diags[0])
			}
		}
	}

	// An unknown set, or an unknown element, is not checked.
	unknown := ssoGroupModel{Roles: types.SetUnknown(ssoGroupRoleObjectType())}
	if diags := validateSsoGroupConfig(ctx, &unknown); diags.HasError() {
		t.Errorf("an unknown set should not be checked: %v", diags)
	}
	withUnknownElement, _ := types.SetValue(ssoGroupRoleObjectType(), []attr.Value{
		types.ObjectUnknown(ssoGroupRoleObjectType().AttrTypes),
	})
	partial := ssoGroupModel{Roles: withUnknownElement}
	if diags := validateSsoGroupConfig(ctx, &partial); diags.HasError() {
		t.Errorf("an unknown element should not be checked: %v", diags)
	}
}

// The usage_group → viewer rule is stated only for team role grants, so an
// SSO group accepts another role on a usage group at plan time.
func TestValidateSsoGroupConfigDoesNotApplyTheTeamRoleRule(t *testing.T) {
	config := ssoGroupModel{Roles: ssoRolesSet(t, ssoRole("editor", scopeObject(t, "usage_group", types.StringValue("ug-1"))))}
	if diags := validateSsoGroupConfig(context.Background(), &config); diags.HasError() {
		t.Errorf("the spec states the viewer rule only for team grants: %v", diags)
	}
}

// The group name is the id, and it can hold a "/" or a space. Each one stays
// inside its path segment.
func TestSsoGroupEndpointsEscapeTheName(t *testing.T) {
	if got := ssoGroupEndpoint("eng/data team"); got != "/v2/sso-groups/eng%2Fdata%20team" {
		t.Errorf("ssoGroupEndpoint = %q", got)
	}
	if got := ssoGroupRolesEndpoint("eng/data team"); got != "/v2/sso-groups/eng%2Fdata%20team/roles" {
		t.Errorf("ssoGroupRolesEndpoint = %q", got)
	}
	if got := ssoGroupRoleEndpoint("eng/data team", "g/1"); got != "/v2/sso-groups/eng%2Fdata%20team/roles/g%2F1" {
		t.Errorf("ssoGroupRoleEndpoint = %q", got)
	}
}
