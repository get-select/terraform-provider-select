// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func teams(names ...string) []teamResponse {
	out := make([]teamResponse, len(names))
	for i, name := range names {
		out[i] = teamResponse{Id: "t-" + name, Name: name}
	}
	return out
}

// name__ilike compares without case and treats "_" as a wildcard, so the
// list can hold teams other than the one wanted. Only the exact name counts.
func TestMatchTeamByNameKeepsOnlyTheExactName(t *testing.T) {
	team, notFound := matchTeamByName("data_eng", teams("DATA_ENG", "data-eng", "data_eng", "dataXeng"))
	if notFound != nil {
		t.Fatalf("unexpected diagnostic: %s", notFound.Detail())
	}
	if team.Id != "t-data_eng" {
		t.Errorf("expected the exact match, got %q", team.Name)
	}
}

func TestMatchTeamByNameNamesCaseOnlyMatchesWhenNothingMatchesExactly(t *testing.T) {
	_, notFound := matchTeamByName("All Users", teams("all users", "ALL USERS", "All Users Extra"))
	if notFound == nil {
		t.Fatal("a name that matches only without case should not be found")
	}
	detail := notFound.Detail()
	if !strings.Contains(detail, `"all users"`) || !strings.Contains(detail, `"ALL USERS"`) {
		t.Errorf("the diagnostic should name the case-only matches, got: %s", detail)
	}
	if strings.Contains(detail, "Extra") {
		t.Errorf("a team with a different name is not a near match, got: %s", detail)
	}
}

func TestMatchTeamByNameWithNoCandidates(t *testing.T) {
	_, notFound := matchTeamByName("Ghost", nil)
	if notFound == nil || !strings.Contains(notFound.Detail(), `no team named "Ghost"`) {
		t.Errorf("expected a not-found diagnostic, got %v", notFound)
	}
}

func TestMatchTeamByNameRejectsDuplicates(t *testing.T) {
	candidates := []teamResponse{{Id: "t-1", Name: "Ops"}, {Id: "t-2", Name: "Ops"}}
	_, notFound := matchTeamByName("Ops", candidates)
	if notFound == nil {
		t.Fatal("two teams with the same name should not silently pick one")
	}
	if !strings.Contains(notFound.Detail(), "t-1") || !strings.Contains(notFound.Detail(), "t-2") {
		t.Errorf("the diagnostic should name both ids, got: %s", notFound.Detail())
	}
}

// The lookup sends the name as the name__ilike filter and reads every page
// of the result.
func TestReadTeamByNameFiltersAndPages(t *testing.T) {
	var filters []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/teams" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		filters = append(filters, r.URL.Query().Get("name__ilike"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page_token") == "" {
			_, _ = w.Write([]byte(`{"items":[{"id":"t-1","etag":"e","name":"ALL USERS","is_all_users":false,"default_member_role":"viewer","create_time":"a","update_time":"b"}],"page_token":"p2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"t-2","etag":"e","name":"All Users","is_all_users":true,"default_member_role":"viewer","create_time":"c","update_time":"d"}]}`))
	}))
	defer server.Close()

	model := teamDataSourceModel{Name: types.StringValue("All Users")}
	diags := readTeamByName(context.Background(), NewAPIClient("key", "org", server.URL), &model)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if model.Id.ValueString() != "t-2" || !model.IsAllUsers.ValueBool() || model.UpdateTime.ValueString() != "d" {
		t.Errorf("the exact match on page two should be returned, got %+v", model)
	}
	if len(filters) != 2 || filters[0] != "All Users" || filters[1] != "All Users" {
		t.Errorf("every page should carry name__ilike, got %v", filters)
	}
}

func TestReadTeamByNameReportsAnAPIFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"Insufficient scope.","code":"forbidden"}`))
	}))
	defer server.Close()

	model := teamDataSourceModel{Name: types.StringValue("Ops")}
	diags := readTeamByName(context.Background(), NewAPIClient("key", "org", server.URL), &model)
	if !diags.HasError() || !strings.Contains(diags[0].Detail(), "Insufficient scope.") {
		t.Errorf("a 403 should be reported with the API's detail, got %v", diags)
	}
}

func TestTeamNameFilter(t *testing.T) {
	if got := teamNameFilter("data_eng 100%"); got.Get("name__ilike") != "data_eng 100%" {
		t.Errorf("a name with only wildcard characters should still be sent as the filter, got %v", got)
	}
	if got := teamNameFilter(`ops\infra`); got != nil {
		t.Errorf("a name with a backslash should send no filter, got %v", got)
	}
}

// A backslash may be an escape character in the API's LIKE pattern, so the
// lookup sends no filter and finds the team in the full list.
func TestReadTeamByNameSkipsTheFilterForABackslash(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Query().Has("name__ilike") {
			t.Errorf("no name__ilike filter should be sent, got %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[` +
			`{"id":"t-1","etag":"e","name":"ops","is_all_users":false,"default_member_role":"editor","create_time":"a","update_time":"b"},` +
			`{"id":"t-2","etag":"e","name":"ops\\infra","is_all_users":false,"default_member_role":"editor","create_time":"a","update_time":"b"}]}`))
	}))
	defer server.Close()

	model := teamDataSourceModel{Name: types.StringValue(`ops\infra`)}
	diags := readTeamByName(context.Background(), NewAPIClient("key", "org", server.URL), &model)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if model.Id.ValueString() != "t-2" {
		t.Errorf("the exact match in the full list should be returned, got %+v", model)
	}
	if len(queries) != 1 {
		t.Errorf("expected one list request, got %d", len(queries))
	}
}
