// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// usersServer serves GET /v2/users in pages. pages maps a page token to a
// body; the first page's token is "". It fails the test on any other path.
func usersServer(t *testing.T, pages map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var tokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/users" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		token := r.URL.Query().Get("page_token")
		tokens = append(tokens, token)
		body, ok := pages[token]
		if !ok {
			t.Errorf("unexpected page token %q", token)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, &tokens
}

func userJSON(id, email string) string {
	return `{"id":"` + id + `","name":"N ` + id + `","email_address":"` + email + `","identity_provider":"okta",` +
		`"picture":null,"last_login_sso_groups":["g1","g2"],"num_pages_visited_last_2_months":3,` +
		`"last_login_time":"2026-06-05T10:30:00Z","last_activity_time":"2026-06-06T10:30:00Z"}`
}

// readUsersData runs the data source's Read with a configuration that holds
// emails, which can be nil for no filter, and returns the users it set.
func readUsersData(t *testing.T, serverURL string, emails []string) ([]userDataSourceModel, datasource.ReadResponse) {
	t.Helper()
	ctx := context.Background()
	d := NewUsersDataSource().(*v2DataSource[usersDataSourceModel])
	d.client = NewAPIClient("key", "org", serverURL)
	s := d.schema(ctx)

	config := usersDataSourceModel{
		Emails: types.SetNull(types.StringType),
		Users:  types.ListNull(types.ObjectType{AttrTypes: userAttrTypes()}),
	}
	if emails != nil {
		value, diags := types.SetValueFrom(ctx, types.StringType, emails)
		if diags.HasError() {
			t.Fatalf("building emails: %v", diags)
		}
		config.Emails = value
	}
	raw := tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if diags := raw.Set(ctx, &config); diags.HasError() {
		t.Fatalf("building config: %v", diags)
	}

	resp := datasource.ReadResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: s, Raw: raw.Raw}}, &resp)
	if resp.Diagnostics.HasError() {
		return nil, resp
	}

	var out usersDataSourceModel
	if diags := resp.State.Get(ctx, &out); diags.HasError() {
		t.Fatalf("reading state: %v", diags)
	}
	var users []userDataSourceModel
	if diags := out.Users.ElementsAs(ctx, &users, false); diags.HasError() {
		t.Fatalf("reading users: %v", diags)
	}
	return users, resp
}

func userEmails(users []userDataSourceModel) string {
	out := make([]string, len(users))
	for i, user := range users {
		out[i] = user.Email.ValueString()
	}
	return strings.Join(out, ",")
}

// With no filter, every user from every page is returned, sorted by email.
func TestUsersDataSourceListsAllPages(t *testing.T) {
	server, tokens := usersServer(t, map[string]string{
		"":   `{"items":[` + userJSON("u-1", "zed@example.com") + `,` + userJSON("u-2", "amy@example.com") + `],"page_token":"p2"}`,
		"p2": `{"items":[` + userJSON("u-3", "Bob@Example.com") + `],"page_token":null}`,
	})

	users, resp := readUsersData(t, server.URL, nil)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if got := userEmails(users); got != "amy@example.com,Bob@Example.com,zed@example.com" {
		t.Errorf("every user should be returned, sorted by email without case, got %s", got)
	}
	if len(*tokens) != 2 || (*tokens)[1] != "p2" {
		t.Errorf("both pages should be requested, got tokens %v", *tokens)
	}
}

// The filter ignores case. An email that belongs to no user is not an
// error: it is absent from the result.
func TestUsersDataSourceFiltersByEmailWithoutCase(t *testing.T) {
	server, _ := usersServer(t, map[string]string{
		"": `{"items":[` + userJSON("u-1", "Alice@Example.com") + `,` + userJSON("u-2", "bob@example.com") + `,` +
			userJSON("u-3", "carol@example.com") + `]}`,
	})

	users, resp := readUsersData(t, server.URL, []string{"alice@example.COM", "carol@example.com", "nobody@example.com"})
	if resp.Diagnostics.HasError() {
		t.Fatalf("a missing email should not fail the read: %v", resp.Diagnostics)
	}
	if got := userEmails(users); got != "Alice@Example.com,carol@example.com" {
		t.Errorf("only the filtered users should be returned, got %s", got)
	}
}

// An empty filter selects no users. It is not the same as no filter.
func TestUsersDataSourceEmptyFilterSelectsNoUsers(t *testing.T) {
	server, _ := usersServer(t, map[string]string{
		"": `{"items":[` + userJSON("u-1", "alice@example.com") + `]}`,
	})

	users, resp := readUsersData(t, server.URL, []string{})
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if len(users) != 0 {
		t.Errorf("an empty filter should select no users, got %s", userEmails(users))
	}
}

// Every exported field comes from the matching UserV2 field.
func TestUsersDataSourceMapsEveryField(t *testing.T) {
	server, _ := usersServer(t, map[string]string{"": `{"items":[` + userJSON("u-1", "alice@example.com") + `]}`})

	users, resp := readUsersData(t, server.URL, nil)
	if resp.Diagnostics.HasError() || len(users) != 1 {
		t.Fatalf("expected one user, got %d: %v", len(users), resp.Diagnostics)
	}
	user := users[0]
	groups := types.ListValueMust(types.StringType, []attr.Value{types.StringValue("g1"), types.StringValue("g2")})
	if user.Id.ValueString() != "u-1" || user.Email.ValueString() != "alice@example.com" ||
		user.Name.ValueString() != "N u-1" || user.IdentityProvider.ValueString() != "okta" ||
		user.LastLoginTime.ValueString() != "2026-06-05T10:30:00Z" || !user.LastLoginSsoGroups.Equal(groups) {
		t.Errorf("unexpected mapping: %+v", user)
	}
}

// A user who has no name, no login record or no SSO groups gets null values,
// not empty strings or an empty list.
func TestUsersDataSourceKeepsNullFields(t *testing.T) {
	server, _ := usersServer(t, map[string]string{
		"": `{"items":[{"id":"u-1","name":null,"email_address":"new@example.com","identity_provider":"google",` +
			`"picture":null,"last_login_sso_groups":null,"num_pages_visited_last_2_months":null,` +
			`"last_login_time":null,"last_activity_time":null}]}`,
	})

	users, resp := readUsersData(t, server.URL, nil)
	if resp.Diagnostics.HasError() || len(users) != 1 {
		t.Fatalf("expected one user, got %d: %v", len(users), resp.Diagnostics)
	}
	user := users[0]
	if !user.Name.IsNull() || !user.LastLoginTime.IsNull() || !user.LastLoginSsoGroups.IsNull() {
		t.Errorf("null API fields should be null, got %+v", user)
	}
	if user.Email.ValueString() != "new@example.com" || user.IdentityProvider.ValueString() != "google" {
		t.Errorf("required fields should still be set, got %+v", user)
	}
}

// An empty user list is an empty list, not null, so length() works on it.
func TestUsersDataSourceEmptyListIsNotNull(t *testing.T) {
	server, _ := usersServer(t, map[string]string{"": `{"items":[]}`})

	ctx := context.Background()
	value, diags := usersValue(ctx, filterUsers(nil, nil))
	if diags.HasError() || value.IsNull() || len(value.Elements()) != 0 {
		t.Errorf("expected an empty, non-null list, got %v %v", value, diags)
	}

	users, resp := readUsersData(t, server.URL, nil)
	if resp.Diagnostics.HasError() || len(users) != 0 {
		t.Errorf("expected no users, got %d: %v", len(users), resp.Diagnostics)
	}
}

func TestUsersDataSourceReportsAnAPIFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"Insufficient scope.","code":"forbidden"}`))
	}))
	defer server.Close()

	_, resp := readUsersData(t, server.URL, nil)
	if !resp.Diagnostics.HasError() {
		t.Fatal("a 403 should fail the read")
	}
	detail := resp.Diagnostics[0].Detail()
	if !strings.Contains(detail, "Insufficient scope.") || !strings.Contains(detail, "users:read") {
		t.Errorf("the diagnostic should hold the API's detail and the read scope, got: %s", detail)
	}
}
