// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"strings"
	"testing"
)

// A trailing slash is a typo worth absorbing rather than rejecting: it should
// normalize the same way NewHTTPClient's own trim does.
func TestNormalizeAPIURLTrimsATrailingSlash(t *testing.T) {
	tests := map[string]string{
		"http://localhost:8000":   "http://localhost:8000",
		"http://localhost:8000/":  "http://localhost:8000",
		"http://localhost:8000//": "http://localhost:8000",
		"https://api.select.dev/": "https://api.select.dev",
		// A proxy can serve the API under its own prefix; only "/v2" doubles up.
		"https://proxy.example/select/": "https://proxy.example/select",
	}

	for input, want := range tests {
		got, diags := normalizeAPIURL(input)
		if diags.HasError() {
			t.Errorf("normalizeAPIURL(%q) returned diagnostics: %v", input, diags)
			continue
		}
		if got != want {
			t.Errorf("normalizeAPIURL(%q) = %q, want %q", input, got, want)
		}
	}
}

// A scheme-less value, one ending in "/v2", or one with a query or fragment
// fails invisibly if it reaches a request at all. The endpoint constants
// already supply "/v2", so "http://localhost:8000/v2" would double that prefix,
// and a query or fragment would swallow the appended path. Each gets a 404
// indistinguishable from the resource being gone, so each should be rejected at
// configure time with a diagnostic naming the problem.
func TestNormalizeAPIURLRejectsAURLTheProviderCannotUse(t *testing.T) {
	tests := map[string]string{
		"localhost:8000":                  "http://",
		"ftp://localhost:8000":            "http://",
		"http://localhost:8000/v2":        "/v2",
		"http://localhost:8000/v2/":       "/v2",
		"https://proxy.example/select/v2": "/v2",
		"http://localhost:8000?x=1":       "query",
		"http://localhost:8000?":          "query",
		"http://localhost:8000#frag":      "fragment",
		"http://localhost:8000#":          "fragment",
		"http:///no-host":                 "host",
		"not a url at all%%%":             "URL",
	}

	for input, wantSubstring := range tests {
		_, diags := normalizeAPIURL(input)
		if !diags.HasError() {
			t.Errorf("normalizeAPIURL(%q) should have been rejected", input)
			continue
		}
		detail := diags[0].Detail()
		if !strings.Contains(detail, wantSubstring) {
			t.Errorf("normalizeAPIURL(%q) diagnostic %q should mention %q", input, detail, wantSubstring)
		}
	}
}
