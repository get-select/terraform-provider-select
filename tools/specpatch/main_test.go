// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture writes an OpenAPI document, a code spec and an overrides file to a
// temporary directory and returns their paths.
func fixture(t *testing.T, spec, codeSpec, overrides string) (string, string, string) {
	t.Helper()
	dir := t.TempDir()

	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
		return path
	}

	overridesPath := ""
	if overrides != "" {
		overridesPath = write("overrides.yml", overrides)
	}
	return write("openapi.json", spec), write("code_spec.json", codeSpec), overridesPath
}

const specWithNullableAndSecret = `{
  "components": {"schemas": {
    "WidgetCreate": {"properties": {
      "id": {"type": "string", "description": "The widget's identifier."},
      "nickname": {"anyOf": [{"type": "string"}, {"type": "null"}], "description": "What to call it."},
      "auth": {"$ref": "#/components/schemas/Auth", "description": "How to connect."}
    }},
    "Widget": {"properties": {
      "id": {"type": "string", "description": "A read description that loses to the create one."},
      "etag": {"type": "string", "description": "The widget's ETag.", "readOnly": true}
    }},
    "Auth": {"properties": {
      "token": {"anyOf": [{"type": "string"}, {"type": "null"}], "description": "The token.",
                "writeOnly": true, "x-terraform-sensitive": true}
    }}
  }}
}`

const codeSpecWithMissingDescriptions = `{
  "provider": {"name": "select"},
  "resources": [{"name": "widget", "schema": {"attributes": [
    {"name": "id", "string": {"computed_optional_required": "required", "description": "The widget's identifier."}},
    {"name": "nickname", "string": {"computed_optional_required": "computed_optional"}},
    {"name": "auth", "single_nested": {"computed_optional_required": "required", "attributes": [
      {"name": "token", "string": {"computed_optional_required": "computed_optional"}}
    ]}},
    {"name": "etag", "string": {"computed_optional_required": "computed"}}
  ]}}],
  "version": "0.1"
}`

func readCodeSpec(t *testing.T, path string) map[string]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading patched code spec: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parsing patched code spec: %v", err)
	}

	byPath := map[string]map[string]any{}
	var walk func(prefix string, attributes any)
	walk = func(prefix string, attributes any) {
		list, _ := attributes.([]any)
		for _, entry := range list {
			attribute := asMap(entry)
			name, _ := attribute["name"].(string)
			_, body := attributeType(attribute)
			if body == nil {
				continue
			}
			full := prefix + name
			byPath[full] = body
			walk(full+".", nestedAttributes(body))
		}
	}
	resources, _ := parsed["resources"].([]any)
	for _, resource := range resources {
		walk("", asMap(asMap(resource)["schema"])["attributes"])
	}
	return byPath
}

// The generator drops the description of any nullable property, and marks nothing
// sensitive. Both have to come back, including through a $ref into a nested
// object, and the request schema's wording has to win.
func TestRestoresDescriptionsAndMarksSecrets(t *testing.T) {
	specPath, codeSpecPath, overridesPath := fixture(t,
		specWithNullableAndSecret, codeSpecWithMissingDescriptions,
		`resources:
  widget:
    description_sources: [WidgetCreate, Widget]
    attributes:
      id:
        requires_replace: true
`)

	if err := run(specPath, codeSpecPath, overridesPath); err != nil {
		t.Fatalf("run: %v", err)
	}
	attributes := readCodeSpec(t, codeSpecPath)

	if got := attributes["nickname"]["description"]; got != "What to call it." {
		t.Errorf("nickname description = %v, want the one from the OpenAPI document", got)
	}
	if got := attributes["auth.token"]["description"]; got != "The token." {
		t.Errorf("auth.token description = %v, want it resolved through the $ref", got)
	}
	if got := attributes["etag"]["description"]; got != "The widget's ETag." {
		t.Errorf("etag description = %v, want the response schema consulted too", got)
	}
	if got := attributes["id"]["description"]; got != "The widget's identifier." {
		t.Errorf("id description = %v, want the first listed source to win", got)
	}

	if attributes["auth.token"]["sensitive"] != true {
		t.Errorf("auth.token should be sensitive: %v", attributes["auth.token"])
	}
	if _, marked := attributes["nickname"]["sensitive"]; marked {
		t.Errorf("nickname should not be sensitive: %v", attributes["nickname"])
	}

	modifiers, ok := attributes["id"]["plan_modifiers"].([]any)
	if !ok || len(modifiers) != 1 {
		t.Fatalf("id plan_modifiers = %v, want one entry", attributes["id"]["plan_modifiers"])
	}
	definition := asMap(asMap(modifiers[0])["custom"])["schema_definition"]
	if definition != "stringplanmodifier.RequiresReplace()" {
		t.Errorf("plan modifier = %v, want the string variant for a string attribute", definition)
	}
}

// A write-only property is a value the API never returns, which on this API always
// means a secret. Generating it as an ordinary attribute would print it in plan
// output, so an unmarked one has to fail the build.
func TestRejectsUnmarkedWriteOnlyProperty(t *testing.T) {
	spec := strings.Replace(specWithNullableAndSecret, `"writeOnly": true, "x-terraform-sensitive": true`, `"writeOnly": true`, 1)
	specPath, codeSpecPath, _ := fixture(t, spec, codeSpecWithMissingDescriptions, "")

	err := run(specPath, codeSpecPath, "")
	if err == nil {
		t.Fatal("expected an error for a write-only property that is not marked sensitive")
	}
	if !strings.Contains(err.Error(), "Auth.token") {
		t.Errorf("error should name the offending property, got: %v", err)
	}
}

// An override that matches nothing is a typo or a leftover from an API change,
// and silently doing nothing is the worst possible outcome.
func TestRejectsOverrideThatMatchesNothing(t *testing.T) {
	specPath, codeSpecPath, overridesPath := fixture(t,
		specWithNullableAndSecret, codeSpecWithMissingDescriptions,
		`resources:
  widget:
    attributes:
      auth.tokne:
        requires_replace: true
`)

	err := run(specPath, codeSpecPath, overridesPath)
	if err == nil {
		t.Fatal("expected an error for an override that matched no attribute")
	}
	if !strings.Contains(err.Error(), "widget.auth.tokne") {
		t.Errorf("error should name the unmatched path, got: %v", err)
	}
}

// A description source that does not exist would silently leave descriptions
// missing, which only shows up as thin documentation much later.
func TestRejectsUnknownDescriptionSource(t *testing.T) {
	specPath, codeSpecPath, overridesPath := fixture(t,
		specWithNullableAndSecret, codeSpecWithMissingDescriptions,
		`resources:
  widget:
    description_sources: [NoSuchSchema]
`)

	err := run(specPath, codeSpecPath, overridesPath)
	if err == nil || !strings.Contains(err.Error(), "NoSuchSchema") {
		t.Fatalf("expected an error naming the unknown schema, got: %v", err)
	}
}
