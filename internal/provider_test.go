// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
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

// Every registered resource and data source must have a name and a schema the
// framework accepts. A schema mistake otherwise shows only when Terraform
// loads the provider.
func TestProviderRegistersValidSchemas(t *testing.T) {
	ctx := context.Background()
	p := &selectProvider{}

	names := map[string]bool{}
	for _, newResource := range p.Resources(ctx) {
		r := newResource()
		var meta resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "select"}, &meta)
		var s resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &s)
		if diags := s.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Errorf("resource %s has an invalid schema: %v", meta.TypeName, diags)
		}
		names["resource "+meta.TypeName] = true
	}
	for _, newDataSource := range p.DataSources(ctx) {
		d := newDataSource()
		var meta datasource.MetadataResponse
		d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "select"}, &meta)
		var s datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &s)
		if diags := s.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Errorf("data source %s has an invalid schema: %v", meta.TypeName, diags)
		}
		names["data source "+meta.TypeName] = true
	}

	for _, want := range []string{"resource select_team", "resource select_team_member", "data source select_team"} {
		if !names[want] {
			t.Errorf("%s should be registered, got %v", want, names)
		}
	}
}
