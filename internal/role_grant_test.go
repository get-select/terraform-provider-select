// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// v2State is a state for schema s. With a nil model it holds no resource,
// which is what Terraform gives ImportState.
func v2State[TModel any](t *testing.T, s schema.Schema, model *TModel) tfsdk.State {
	t.Helper()
	ctx := context.Background()
	state := tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if model != nil {
		if diags := state.Set(ctx, model); diags.HasError() {
			t.Fatalf("setting state: %v", diags)
		}
	}
	return state
}

// tfPlan is a plan for schema s that holds model.
func tfPlan[TModel any](t *testing.T, s schema.Schema, model *TModel) tfsdk.Plan {
	t.Helper()
	ctx := context.Background()
	plan := tfsdk.Plan{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if diags := plan.Set(ctx, model); diags.HasError() {
		t.Fatalf("setting plan: %v", diags)
	}
	return plan
}

// readV2 runs r.Read against serverURL with model as the prior state.
func readV2[TModel, TResponse any](t *testing.T, r *v2Resource[TModel, TResponse], serverURL string, model TModel) resource.ReadResponse {
	t.Helper()
	r.client = NewAPIClient("key", "org", serverURL)
	s := r.schema(context.Background())
	resp := resource.ReadResponse{State: v2State(t, s, &model)}
	r.Read(context.Background(), resource.ReadRequest{State: v2State(t, s, &model)}, &resp)
	return resp
}

// deleteV2 runs r.Delete against serverURL with model as the prior state.
func deleteV2[TModel, TResponse any](t *testing.T, r *v2Resource[TModel, TResponse], serverURL string, model TModel) resource.DeleteResponse {
	t.Helper()
	r.client = NewAPIClient("key", "org", serverURL)
	s := r.schema(context.Background())
	resp := resource.DeleteResponse{State: v2State(t, s, &model)}
	r.Delete(context.Background(), resource.DeleteRequest{State: v2State(t, s, &model)}, &resp)
	return resp
}

// importV2 runs r.ImportState with the given address.
func importV2[TModel, TResponse any](t *testing.T, r *v2Resource[TModel, TResponse], id string) resource.ImportStateResponse {
	t.Helper()
	s := r.schema(context.Background())
	resp := resource.ImportStateResponse{State: v2State[TModel](t, s, nil)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
	return resp
}

// recordedRequest is one request a recordingServer received.
type recordedRequest struct {
	Method, Path, EscapedPath, IfMatch, Body string
	HasIfMatch                               bool
}

// recordingServer answers every request with status and body, and records
// what it received.
type recordingServer struct {
	*httptest.Server
	requests []recordedRequest
}

func newRecordingServer(t *testing.T, status int, body string) *recordingServer {
	t.Helper()
	s := &recordingServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_, hasIfMatch := r.Header["If-Match"]
		s.requests = append(s.requests, recordedRequest{
			Method: r.Method, Path: r.URL.Path, EscapedPath: r.URL.EscapedPath(),
			IfMatch: r.Header.Get("If-Match"), HasIfMatch: hasIfMatch, Body: string(raw),
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

// objectRequiresReplace reports whether an object attribute carries a plan
// modifier that destroys and recreates the resource.
func objectRequiresReplace(attribute schema.SingleNestedAttribute) bool {
	for _, modifier := range attribute.PlanModifiers {
		if strings.Contains(modifier.Description(context.Background()), "destroy and recreate") {
			return true
		}
	}
	return false
}

func TestBuildRoleGrantCreate(t *testing.T) {
	ctx := context.Background()

	payload, diags := buildRoleGrantCreate(ctx, types.StringValue("admin"), nullScope())
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	body := marshal(t, payload)
	if len(body) != 1 || body["role"] != "admin" {
		t.Errorf("a null scope should omit the scope key, which means the organization, got %v", body)
	}

	payload, diags = buildRoleGrantCreate(ctx, types.StringValue("viewer"), scopeObject(t, "usage_group", types.StringValue("ug-1")))
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	body = marshal(t, payload)
	scope, _ := body["scope"].(map[string]any)
	if body["role"] != "viewer" || scope["type"] != "usage_group" || scope["id"] != "ug-1" {
		t.Errorf("role and scope should be sent as configured, got %v", body)
	}

	payload, _ = buildRoleGrantCreate(ctx, types.StringValue("admin"), scopeObject(t, "organization", types.StringNull()))
	body = marshal(t, payload)
	scope, _ = body["scope"].(map[string]any)
	if scope["type"] != "organization" {
		t.Errorf("an explicit organization scope should be sent, got %v", body)
	}
	if _, present := scope["id"]; present {
		t.Errorf("an organization scope has no id, got %v", scope)
	}
}

// The API accepts only viewer on a usage_group scope. Every other scope, and
// an omitted one, accepts every role.
func TestValidateRoleGrantRole(t *testing.T) {
	ctx := context.Background()
	at := path.Root("role")
	usageGroup := scopeObject(t, "usage_group", types.StringValue("ug-1"))

	cases := []struct {
		name    string
		role    types.String
		scope   types.Object
		wantErr bool
	}{
		{"viewer on a usage group", types.StringValue("viewer"), usageGroup, false},
		{"editor on a usage group", types.StringValue("editor"), usageGroup, true},
		{"admin on a usage group", types.StringValue("admin"), usageGroup, true},
		{"admin on the organization", types.StringValue("admin"), nullScope(), false},
		{"admin on an explicit organization", types.StringValue("admin"), scopeObject(t, "organization", types.StringNull()), false},
		{"editor on a Snowflake account", types.StringValue("editor"), scopeObject(t, "snowflake_account", types.StringValue("u-1")), false},
		{"unknown role", types.StringUnknown(), usageGroup, false},
		{"unknown scope", types.StringValue("editor"), types.ObjectUnknown(roleGrantScopeAttrTypes()), false},
		{"unknown scope type", types.StringValue("editor"), func() types.Object {
			v, _ := types.ObjectValue(roleGrantScopeAttrTypes(), map[string]attr.Value{
				"type": types.StringUnknown(), "id": types.StringValue("ug-1"),
			})
			return v
		}(), false},
	}
	for _, c := range cases {
		diags := validateRoleGrantRole(ctx, at, c.role, c.scope)
		if diags.HasError() != c.wantErr {
			t.Errorf("%s: error = %v, want %v (%v)", c.name, diags.HasError(), c.wantErr, diags)
			continue
		}
		if c.wantErr {
			d, ok := diags[0].(interface{ Path() path.Path })
			if !ok || !d.Path().Equal(at) {
				t.Errorf("%s: the error should point at the role attribute, got %v", c.name, diags[0])
			}
		}
	}
}

// A 409 on create gets the import hint. A 409 on delete, and any other
// status on create, keep the shared wording.
func TestRoleGrantConflict(t *testing.T) {
	f := teamRoleErrors
	conflict := roleGrantConflict(f, "select_team_role", "<team_id>/<role_id>")
	apiErr := &apiError{StatusCode: http.StatusConflict, Detail: "Role grant already exists"}

	d := f.diagnostic("add "+f.Object, apiErr, conflict)
	if d.Summary() != "Team Role Conflict" {
		t.Errorf("summary = %q", d.Summary())
	}
	for _, want := range []string{"Role grant already exists", "terraform import select_team_role.<name> <team_id>/<role_id>"} {
		if !strings.Contains(d.Detail(), want) {
			t.Errorf("the detail should contain %q, got:\n%s", want, d.Detail())
		}
	}

	if d := conflict("delete "+f.Object, apiErr); d != nil {
		t.Errorf("a 409 on delete is not a duplicate grant, got %v", d)
	}
	if d := conflict("add "+f.Object, &apiError{StatusCode: http.StatusUnprocessableEntity}); d != nil {
		t.Errorf("only a 409 gets the import hint, got %v", d)
	}
}

// Every role grant resource gets the same scope attribute, and the API cannot
// change a grant's scope.
func TestRoleGrantSchemasReplaceOnScope(t *testing.T) {
	ctx := context.Background()
	for name, s := range map[string]schema.Schema{
		"select_team_role":    teamRoleResourceSchema(ctx),
		"select_user_role":    userRoleResourceSchema(ctx),
		"select_default_role": defaultRoleResourceSchema(ctx),
	} {
		scope, ok := s.Attributes["scope"].(schema.SingleNestedAttribute)
		if !ok {
			t.Errorf("%s: scope should be a single nested attribute, got %T", name, s.Attributes["scope"])
			continue
		}
		if !scope.Optional || !objectRequiresReplace(scope) {
			t.Errorf("%s: scope should be optional and force a new grant", name)
		}
		for _, ignored := range []string{"entity", "usage_group_id", "snowflake_account_uuid", "snowflake_organization_name", "is_default", "granted_from_team_name"} {
			if _, present := s.Attributes[ignored]; present {
				t.Errorf("%s: %s is a response detail that scope replaces, and should not be an attribute", name, ignored)
			}
		}
	}
}

func TestV2NoUpdateFailsWithoutARequest(t *testing.T) {
	payload, diags := v2NoUpdate[teamRoleModel]("team role grant")(context.Background(), nil, nil)
	if payload != nil || !diags.HasError() {
		t.Errorf("an update of a resource the API cannot update should fail, got %v %v", payload, diags)
	}
}
