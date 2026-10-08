// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

// Pagination and lookup for the v2 API's list endpoints. Every list route
// takes the same two query parameters, `page_token` and `max_results`, and
// answers with the same envelope, ListResponse[T]. These helpers own that
// contract, so a resource or a data source that lists something does not
// restate it.

// v2ListPageSize is the max_results value sent on every list request. The
// API's default is 50. A larger page means fewer requests for a long list.
const v2ListPageSize = 100

// v2ListResponse mirrors ListResponse[T]. row_count is not decoded: it is a
// best-effort total, and nothing here needs it.
type v2ListResponse[T any] struct {
	Items []T `json:"items"`
	// PageToken is the cursor for the next page. It is null or empty on the
	// last page.
	PageToken *string `json:"page_token"`
}

// v2ListURL adds the paging parameters to a list endpoint. query holds the
// endpoint's own filters and can be nil. The function does not change query.
func v2ListURL(endpoint string, query url.Values, pageToken string) string {
	params := url.Values{}
	for key, values := range query {
		params[key] = append([]string(nil), values...)
	}
	params.Set("max_results", fmt.Sprint(v2ListPageSize))
	if pageToken != "" {
		params.Set("page_token", pageToken)
	}
	return endpoint + "?" + params.Encode()
}

// v2ListPages requests the pages of a list endpoint in order and gives the
// items of each page to visit. When visit returns false, no more pages are
// requested.
//
// An API failure on any page comes back as an *apiError, with no diagnostics,
// the same as doRequest. A list endpoint that gives a page token it already
// gave would cause an infinite loop, so that is a diagnostic.
func v2ListPages[T any](ctx context.Context, client *APIClient, endpoint string, query url.Values, visit func(items []T) bool) (*apiError, diag.Diagnostics) {
	pageToken := ""
	seen := map[string]bool{}
	for {
		var page v2ListResponse[T]
		apiErr, diags := client.doRequest(ctx, http.MethodGet, v2ListURL(endpoint, query, pageToken), nil, &page, requestOptions{})
		if diags.HasError() || apiErr != nil {
			return apiErr, diags
		}

		if !visit(page.Items) {
			return nil, nil
		}

		if page.PageToken == nil || *page.PageToken == "" {
			return nil, nil
		}
		seen[pageToken] = true
		if seen[*page.PageToken] {
			return nil, diag.Diagnostics{diag.NewErrorDiagnostic(
				"Unexpected List Response",
				fmt.Sprintf("SELECT returned a page token it had already returned when listing %s. "+
					"Please report this issue to the provider developers.", endpoint),
			)}
		}
		pageToken = *page.PageToken
	}
}

// v2ListAll returns every item of a list endpoint, from all of its pages.
func v2ListAll[T any](ctx context.Context, client *APIClient, endpoint string, query url.Values) ([]T, *apiError, diag.Diagnostics) {
	items := []T{}
	apiErr, diags := v2ListPages(ctx, client, endpoint, query, func(page []T) bool {
		items = append(items, page...)
		return true
	})
	if diags.HasError() || apiErr != nil {
		return nil, apiErr, diags
	}
	return items, nil, nil
}

// v2FindInList returns the first item of a list endpoint for which match
// returns true. It stops at the page that holds that item. It returns a nil
// item, and no error, when no page holds a match.
func v2FindInList[T any](ctx context.Context, client *APIClient, endpoint string, query url.Values, match func(item *T) bool) (*T, *apiError, diag.Diagnostics) {
	var found *T
	apiErr, diags := v2ListPages(ctx, client, endpoint, query, func(page []T) bool {
		for i := range page {
			if match(&page[i]) {
				found = &page[i]
				return false
			}
		}
		return true
	})
	if diags.HasError() || apiErr != nil {
		return nil, apiErr, diags
	}
	return found, nil, nil
}

// v2ListAndFind builds a v2Resource fetch hook for a child resource that the
// API can list but has no GET-by-id route for, such as a team member or a role
// grant. The hook lists collection(model) and returns the item whose
// responseId equals the model's id.
//
// When no page holds the item, the hook answers with a 404 apiError. A GET
// on a deleted item gives the same 404, so v2Resource.Read removes the
// resource from state and Terraform plans to create it again.
func v2ListAndFind[TModel, TResponse any](
	collection func(model *TModel) string,
	id func(model *TModel) string,
	responseId func(response *TResponse) string,
) func(ctx context.Context, client *APIClient, model *TModel) (*TResponse, *apiError, diag.Diagnostics) {
	return func(ctx context.Context, client *APIClient, model *TModel) (*TResponse, *apiError, diag.Diagnostics) {
		want := id(model)
		endpoint := collection(model)
		found, apiErr, diags := v2FindInList(ctx, client, endpoint, nil, func(item *TResponse) bool {
			return responseId(item) == want
		})
		if diags.HasError() || apiErr != nil {
			return nil, apiErr, diags
		}
		if found == nil {
			return nil, &apiError{
				StatusCode: http.StatusNotFound,
				Detail:     fmt.Sprintf("no item with id %q in %s", want, endpoint),
			}, nil
		}
		return found, nil, nil
	}
}
