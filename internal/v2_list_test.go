// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type listItem struct {
	Id string `json:"id"`
}

// pagedServer serves pages of a list endpoint. pages[i] is the body of the
// page that the token tokens[i] asks for; the first page's token is "". It
// records every query string the client sends.
type pagedServer struct {
	pages   map[string]string
	queries []url.Values
	// status, when set, answers every request whose page token equals
	// failToken with this status.
	failToken string
	status    int
}

func (s *pagedServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("a list should only GET, got %s", r.Method)
		}
		s.queries = append(s.queries, r.URL.Query())
		token := r.URL.Query().Get("page_token")
		if s.status != 0 && token == s.failToken {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(s.status)
			_, _ = fmt.Fprintf(w, `{"detail":"failed on page %q","code":"x"}`, token)
			return
		}
		body, ok := s.pages[token]
		if !ok {
			t.Errorf("unexpected page token %q", token)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
}

func newPagedServer(t *testing.T, pages map[string]string) (*pagedServer, *APIClient) {
	backend := &pagedServer{pages: pages}
	server := httptest.NewServer(backend.handler(t))
	t.Cleanup(server.Close)
	return backend, NewAPIClient("key", "org", server.URL)
}

func ids(items []listItem) string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.Id
	}
	return strings.Join(out, ",")
}

// The endpoint's own filters must survive, and the paging parameters are
// added beside them. The caller's query must not change: a lookup reuses it
// for every page.
func TestV2ListURLAddsPagingAndKeepsFilters(t *testing.T) {
	query := url.Values{"name__ilike": {"Data Eng"}}

	first, err := url.Parse(v2ListURL("/v2/teams", query, ""))
	if err != nil {
		t.Fatal(err)
	}
	if first.Path != "/v2/teams" {
		t.Errorf("path should be unchanged, got %q", first.Path)
	}
	got := first.Query()
	if got.Get("name__ilike") != "Data Eng" || got.Get("max_results") != fmt.Sprint(v2ListPageSize) {
		t.Errorf("first page should carry the filter and max_results, got %v", got)
	}
	if got.Has("page_token") {
		t.Errorf("the first page should carry no page_token, got %q", got.Get("page_token"))
	}

	next, _ := url.Parse(v2ListURL("/v2/teams", query, "abc+/="))
	if next.Query().Get("page_token") != "abc+/=" {
		t.Errorf("an opaque token should be sent unchanged after encoding, got %q", next.Query().Get("page_token"))
	}

	if len(query) != 1 {
		t.Errorf("v2ListURL should not change the caller's query, got %v", query)
	}
}

func TestV2ListAllFollowsPageTokensToTheLastPage(t *testing.T) {
	backend, client := newPagedServer(t, map[string]string{
		"":   `{"items":[{"id":"a"},{"id":"b"}],"page_token":"p2"}`,
		"p2": `{"items":[{"id":"c"}],"page_token":"p3"}`,
		"p3": `{"items":[{"id":"d"}],"page_token":null,"row_count":4}`,
	})

	items, apiErr, diags := v2ListAll[listItem](context.Background(), client, "/v2/things", url.Values{"type": {"user"}})
	if diags.HasError() || apiErr != nil {
		t.Fatalf("unexpected failure: %v %v", diags, apiErr)
	}
	if ids(items) != "a,b,c,d" {
		t.Errorf("every page's items should be returned in order, got %s", ids(items))
	}
	if len(backend.queries) != 3 {
		t.Fatalf("expected one request per page, got %d", len(backend.queries))
	}
	for i, want := range []string{"", "p2", "p3"} {
		if got := backend.queries[i].Get("page_token"); got != want {
			t.Errorf("request %d should carry page_token %q, got %q", i, want, got)
		}
		if backend.queries[i].Get("type") != "user" {
			t.Errorf("request %d lost the endpoint's filter: %v", i, backend.queries[i])
		}
	}
}

func TestV2ListAllReturnsAnEmptyListForNoItems(t *testing.T) {
	backend, client := newPagedServer(t, map[string]string{
		"": `{"items":[]}`,
	})

	items, apiErr, diags := v2ListAll[listItem](context.Background(), client, "/v2/things", nil)
	if diags.HasError() || apiErr != nil {
		t.Fatalf("unexpected failure: %v %v", diags, apiErr)
	}
	if items == nil || len(items) != 0 {
		t.Errorf("an empty list should be a non-nil empty slice, got %#v", items)
	}
	if len(backend.queries) != 1 {
		t.Errorf("an absent page_token means the last page, got %d requests", len(backend.queries))
	}
}

// The API documents the last page's token as "empty/absent", so an empty
// string has to stop the loop the same as null does.
func TestV2ListAllTreatsAnEmptyPageTokenAsTheLastPage(t *testing.T) {
	backend, client := newPagedServer(t, map[string]string{
		"": `{"items":[{"id":"a"}],"page_token":""}`,
	})

	items, _, diags := v2ListAll[listItem](context.Background(), client, "/v2/things", nil)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if ids(items) != "a" || len(backend.queries) != 1 {
		t.Errorf("an empty page_token should end the list, got %s after %d requests", ids(items), len(backend.queries))
	}
}

// A server that gives back the token it was just sent would page forever.
func TestV2ListAllStopsWhenThePageTokenRepeats(t *testing.T) {
	backend, client := newPagedServer(t, map[string]string{
		"":   `{"items":[{"id":"a"}],"page_token":"p2"}`,
		"p2": `{"items":[{"id":"b"}],"page_token":"p2"}`,
	})

	_, _, diags := v2ListAll[listItem](context.Background(), client, "/v2/things", nil)
	if !diags.HasError() {
		t.Fatal("a repeated page token should fail rather than loop")
	}
	if len(backend.queries) != 2 {
		t.Errorf("the loop should stop at the repeat, got %d requests", len(backend.queries))
	}
}

// A cycle longer than one page loops forever too.
func TestV2ListAllStopsWhenThePageTokensCycle(t *testing.T) {
	backend, client := newPagedServer(t, map[string]string{
		"":   `{"items":[{"id":"a"}],"page_token":"p1"}`,
		"p1": `{"items":[{"id":"b"}],"page_token":"p2"}`,
		"p2": `{"items":[{"id":"c"}],"page_token":"p1"}`,
	})

	_, _, diags := v2ListAll[listItem](context.Background(), client, "/v2/things", nil)
	if !diags.HasError() {
		t.Fatal("a page token cycle should fail rather than loop")
	}
	if len(backend.queries) != 3 {
		t.Errorf("the loop should stop when p1 comes back, got %d requests", len(backend.queries))
	}
}

func TestV2ListAllReturnsAnAPIErrorFromALaterPage(t *testing.T) {
	backend, client := newPagedServer(t, map[string]string{
		"": `{"items":[{"id":"a"}],"page_token":"p2"}`,
	})
	backend.failToken, backend.status = "p2", http.StatusForbidden

	items, apiErr, diags := v2ListAll[listItem](context.Background(), client, "/v2/things", nil)
	if diags.HasError() {
		t.Fatalf("an API failure should be an apiError, not diagnostics: %v", diags)
	}
	if apiErr == nil || apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("the second page's 403 should reach the caller, got %v", apiErr)
	}
	if items != nil {
		t.Errorf("a failed list should return no partial items, got %v", items)
	}
}

// A lookup should stop at the page that holds the item rather than read the
// whole list.
func TestV2FindInListStopsAtThePageHoldingTheMatch(t *testing.T) {
	backend, client := newPagedServer(t, map[string]string{
		"":   `{"items":[{"id":"a"}],"page_token":"p2"}`,
		"p2": `{"items":[{"id":"b"},{"id":"c"}],"page_token":"p3"}`,
		"p3": `{"items":[{"id":"d"}]}`,
	})

	found, apiErr, diags := v2FindInList(context.Background(), client, "/v2/things", nil, func(item *listItem) bool {
		return item.Id == "c"
	})
	if diags.HasError() || apiErr != nil {
		t.Fatalf("unexpected failure: %v %v", diags, apiErr)
	}
	if found == nil || found.Id != "c" {
		t.Fatalf("expected item c, got %v", found)
	}
	if len(backend.queries) != 2 {
		t.Errorf("the lookup should stop at page two, got %d requests", len(backend.queries))
	}
}

func TestV2FindInListReturnsNilWhenNoPageHoldsAMatch(t *testing.T) {
	backend, client := newPagedServer(t, map[string]string{
		"":   `{"items":[{"id":"a"}],"page_token":"p2"}`,
		"p2": `{"items":[{"id":"b"}]}`,
	})

	found, apiErr, diags := v2FindInList(context.Background(), client, "/v2/things", nil, func(item *listItem) bool {
		return item.Id == "z"
	})
	if diags.HasError() || apiErr != nil {
		t.Fatalf("a missing item is not a failure: %v %v", diags, apiErr)
	}
	if found != nil {
		t.Errorf("expected no item, got %v", found)
	}
	if len(backend.queries) != 2 {
		t.Errorf("every page should be read before giving up, got %d requests", len(backend.queries))
	}
}

type listModel struct {
	Parent, Id string
}

func TestV2ListAndFindFindsTheItemAcrossPages(t *testing.T) {
	backend, client := newPagedServer(t, map[string]string{
		"":   `{"items":[{"id":"m-1"}],"page_token":"p2"}`,
		"p2": `{"items":[{"id":"m-2"}]}`,
	})
	fetch := v2ListAndFind(
		func(m *listModel) string { return "/v2/parents/" + m.Parent + "/children" },
		func(m *listModel) string { return m.Id },
		func(r *listItem) string { return r.Id },
	)

	found, apiErr, diags := fetch(context.Background(), client, &listModel{Parent: "p", Id: "m-2"})
	if diags.HasError() || apiErr != nil {
		t.Fatalf("unexpected failure: %v %v", diags, apiErr)
	}
	if found == nil || found.Id != "m-2" {
		t.Fatalf("expected m-2, got %v", found)
	}
	if len(backend.queries) != 2 {
		t.Errorf("expected both pages read, got %d", len(backend.queries))
	}
}

// v2Resource.Read removes a resource from state on a 404. A listed item that
// is gone has to answer the same way, or Terraform would keep it in state.
func TestV2ListAndFindAnswers404WhenTheItemIsGone(t *testing.T) {
	_, client := newPagedServer(t, map[string]string{
		"": `{"items":[{"id":"m-1"}]}`,
	})
	fetch := v2ListAndFind(
		func(m *listModel) string { return "/v2/parents/" + m.Parent + "/children" },
		func(m *listModel) string { return m.Id },
		func(r *listItem) string { return r.Id },
	)

	found, apiErr, diags := fetch(context.Background(), client, &listModel{Parent: "p", Id: "m-9"})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if found != nil {
		t.Errorf("expected no item, got %v", found)
	}
	if apiErr == nil || apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("a missing item should answer 404, got %v", apiErr)
	}
}

// A parent that is gone 404s on the list itself, and that 404 has to reach
// Read unchanged.
func TestV2ListAndFindPassesThroughTheListsOwnFailure(t *testing.T) {
	backend, client := newPagedServer(t, map[string]string{})
	backend.failToken, backend.status = "", http.StatusNotFound
	fetch := v2ListAndFind(
		func(m *listModel) string { return "/v2/parents/" + m.Parent + "/children" },
		func(m *listModel) string { return m.Id },
		func(r *listItem) string { return r.Id },
	)

	_, apiErr, _ := fetch(context.Background(), client, &listModel{Parent: "gone", Id: "m-1"})
	if apiErr == nil || apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("the parent's 404 should reach the caller, got %v", apiErr)
	}
	if !strings.Contains(apiErr.Detail, `failed on page ""`) {
		t.Errorf("the list's own problem detail should be kept, got %q", apiErr.Detail)
	}
}
