// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"
)

// A trailing slash on select_api_url used to double up with an endpoint's own
// leading slash and build a path such as "//v2/usage-group-sets". The API
// answers a doubled slash with a plain 404, which Read cannot tell apart from
// the resource itself being gone, so NewHTTPClient trims it instead of
// forwarding it into every request.
func TestNewHTTPClientTrimsATrailingSlash(t *testing.T) {
	tests := map[string]string{
		"http://localhost:8000":   "http://localhost:8000",
		"http://localhost:8000/":  "http://localhost:8000",
		"http://localhost:8000//": "http://localhost:8000",
		"https://api.select.dev":  "https://api.select.dev",
		"https://api.select.dev/": "https://api.select.dev",
	}

	for input, want := range tests {
		client := NewHTTPClient("key", "org", input)
		if client.baseURL != want {
			t.Errorf("NewHTTPClient(%q).baseURL = %q, want %q", input, client.baseURL, want)
		}
	}
}

func TestBuildURLDoesNotDoubleTheSlash(t *testing.T) {
	client := NewHTTPClient("key", "org", "http://localhost:8000/")

	got := client.buildURL("/v2/usage-group-sets")
	want := "http://localhost:8000/v2/usage-group-sets"
	if got != want {
		t.Errorf("buildURL(%q) = %q, want %q", "/v2/usage-group-sets", got, want)
	}
}
