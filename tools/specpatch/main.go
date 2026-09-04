// SPDX-License-Identifier: MPL-2.0

// Command specpatch repairs and completes a generated provider code spec with
// the schema details tfplugingen-openapi cannot produce.
//
// tfplugingen-openapi's generator config can only override an attribute's
// description: its Override struct has a single Description field, and unknown
// YAML keys are dropped without complaint. Sensitivity, plan modifiers and the
// rest of the Provider Code Specification are therefore out of reach from
// generator_config*.yml, and have to be applied to the generated code spec
// before tfplugingen-framework turns it into Go.
//
// Three passes, all driven by the OpenAPI document wherever possible so the
// generated schema tracks the API rather than a copy of it kept here:
//
//   - Descriptions. The generator drops the description of any property written
//     as a nullable union (anyOf: [T, null]), which on this API is most of them.
//     specpatch restores each one from the schemas listed in the resource's
//     description_sources, in order, following $refs into nested objects.
//   - Sensitivity. A property carrying x-terraform-sensitive marks every
//     attribute of that name sensitive, so a secret is masked because the API
//     says it is one and not because someone remembered to list it here.
//   - Overrides. Anything left comes from the overrides file, keyed by dotted
//     attribute path.
//
// Two invariants fail the build rather than shipping quietly:
//
//   - A write-only property must also be marked x-terraform-sensitive.
//     Write-only means the API never returns the value, which on this API always
//     means a secret; without this check a newly added secret would generate as
//     an ordinary attribute and show up in plan output.
//   - An override must match an attribute that exists, which catches typos and
//     paths left behind when the API changes.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	specPath := flag.String("spec", "", "path to the source OpenAPI document (JSON)")
	codeSpecPath := flag.String("code-spec", "", "path to the generated provider code spec to patch in place")
	overridesPath := flag.String("overrides", "", "path to the overrides file (YAML, optional)")
	flag.Parse()

	if *specPath == "" || *codeSpecPath == "" {
		fmt.Fprintln(os.Stderr, "specpatch: -spec and -code-spec are required")
		os.Exit(2)
	}

	if err := run(*specPath, *codeSpecPath, *overridesPath); err != nil {
		fmt.Fprintf(os.Stderr, "specpatch: %v\n", err)
		os.Exit(1)
	}
}

func run(specPath, codeSpecPath, overridesPath string) error {
	spec, err := loadSpec(specPath)
	if err != nil {
		return err
	}
	if err := spec.checkSecretsMarked(); err != nil {
		return err
	}

	config, err := loadOverrides(overridesPath)
	if err != nil {
		return err
	}

	raw, err := os.ReadFile(codeSpecPath)
	if err != nil {
		return fmt.Errorf("reading code spec: %w", err)
	}
	var codeSpec map[string]any
	if err := json.Unmarshal(raw, &codeSpec); err != nil {
		return fmt.Errorf("parsing code spec %s: %w", codeSpecPath, err)
	}

	p := &patcher{spec: spec, config: config}
	if err := p.patchAll(codeSpec); err != nil {
		return err
	}
	if err := p.checkOverridesApplied(); err != nil {
		return err
	}

	out, err := json.MarshalIndent(codeSpec, "", "\t")
	if err != nil {
		return fmt.Errorf("serializing code spec: %w", err)
	}
	if err := os.WriteFile(codeSpecPath, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing code spec: %w", err)
	}

	fmt.Printf("specpatch: %s — restored %d description(s), marked %d attribute(s) sensitive, applied %d override(s)\n",
		codeSpecPath, p.restoredDescriptions, p.markedSensitive, p.appliedOverrides)
	if len(p.withoutDescription) > 0 {
		sort.Strings(p.withoutDescription)
		fmt.Printf("specpatch: no description found for: %s\n", strings.Join(p.withoutDescription, ", "))
	}
	return nil
}

// ---------------------------------------------------------------------------
// OpenAPI document
// ---------------------------------------------------------------------------

type spec struct {
	schemas map[string]any
	// sensitiveNames are the property names marked x-terraform-sensitive anywhere
	// in the document.
	sensitiveNames map[string]bool
	// writeOnlyUnmarked are the schema.property locations that are write-only
	// without being marked sensitive.
	writeOnlyUnmarked []string
}

func loadSpec(path string) (*spec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading OpenAPI document: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parsing OpenAPI document %s: %w", path, err)
	}

	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	if schemas == nil {
		return nil, fmt.Errorf("%s has no components.schemas", path)
	}

	s := &spec{schemas: schemas, sensitiveNames: map[string]bool{}}
	for schemaName, schema := range schemas {
		properties, _ := asMap(schema)["properties"].(map[string]any)
		for propName, property := range properties {
			node := asMap(property)
			sensitive := node["x-terraform-sensitive"] == true
			if sensitive {
				s.sensitiveNames[propName] = true
			}
			if node["writeOnly"] == true && !sensitive {
				s.writeOnlyUnmarked = append(s.writeOnlyUnmarked, schemaName+"."+propName)
			}
		}
	}
	return s, nil
}

func (s *spec) checkSecretsMarked() error {
	if len(s.writeOnlyUnmarked) == 0 {
		return nil
	}
	sort.Strings(s.writeOnlyUnmarked)
	return fmt.Errorf(
		"these properties are write-only but not marked x-terraform-sensitive, so they would generate "+
			"as ordinary attributes and appear in plan output: %s.\n"+
			"Mark them in the API's own schema (see entities/api/v2/utils.py) rather than working around it here",
		strings.Join(s.writeOnlyUnmarked, ", "))
}

// resolve unwraps $ref and nullable unions until it reaches a concrete schema
// node. A union member of {"type": "null"} is skipped, matching how the
// generator maps anyOf: [T, null] to T.
func (s *spec) resolve(node any) map[string]any {
	current := asMap(node)
	for range 10 { // bounded: guards against a cyclic $ref
		if ref, ok := current["$ref"].(string); ok {
			name := strings.TrimPrefix(ref, "#/components/schemas/")
			next := asMap(s.schemas[name])
			if next == nil {
				return current
			}
			current = next
			continue
		}
		if members := unionMembers(current); members != nil {
			for _, member := range members {
				candidate := asMap(member)
				if candidate["type"] == "null" {
					continue
				}
				current = candidate
				break
			}
			continue
		}
		return current
	}
	return current
}

// property returns the schema node for a named property of the given schema,
// resolving the parent first so a $ref'd or nullable parent still works.
func (s *spec) property(parent any, name string) (map[string]any, bool) {
	properties, _ := s.resolve(parent)["properties"].(map[string]any)
	if properties == nil {
		return nil, false
	}
	node, ok := properties[name]
	if !ok {
		return nil, false
	}
	return asMap(node), true
}

// description returns a property's description, preferring the outer node (where
// a nullable union carries it) over the resolved member.
func (s *spec) description(node map[string]any) string {
	if text, ok := node["description"].(string); ok && text != "" {
		return text
	}
	if text, ok := s.resolve(node)["description"].(string); ok {
		return text
	}
	return ""
}

func unionMembers(node map[string]any) []any {
	for _, key := range []string{"anyOf", "oneOf"} {
		if members, ok := node[key].([]any); ok && len(members) > 0 {
			return members
		}
	}
	return nil
}

func asMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

// ---------------------------------------------------------------------------
// Overrides file
// ---------------------------------------------------------------------------

type overrides struct {
	Resources map[string]resourceOverrides `yaml:"resources"`
}

type resourceOverrides struct {
	// Description is the resource's own description. The generator does not carry
	// one over from the OpenAPI schema, which leaves the heading of the generated
	// documentation page empty.
	Description string `yaml:"description"`
	// DescriptionSources are OpenAPI schema names searched in order for a
	// description the generator dropped.
	DescriptionSources []string                  `yaml:"description_sources"`
	Attributes         map[string]map[string]any `yaml:"attributes"`
}

func loadOverrides(path string) (overrides, error) {
	if path == "" {
		return overrides{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return overrides{}, fmt.Errorf("reading overrides: %w", err)
	}
	var result overrides
	if err := yaml.Unmarshal(raw, &result); err != nil {
		return overrides{}, fmt.Errorf("parsing overrides %s: %w", path, err)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Patching
// ---------------------------------------------------------------------------

type patcher struct {
	spec   *spec
	config overrides

	restoredDescriptions int
	markedSensitive      int
	appliedOverrides     int
	withoutDescription   []string

	// seen records the dotted attribute paths encountered per resource, so an
	// override that never matched can be reported.
	seen map[string]map[string]bool
}

func (p *patcher) patchAll(codeSpec map[string]any) error {
	p.seen = map[string]map[string]bool{}

	resources, _ := codeSpec["resources"].([]any)
	for _, entry := range resources {
		resource := asMap(entry)
		name, _ := resource["name"].(string)
		schema := asMap(resource["schema"])
		if schema == nil {
			continue
		}
		p.seen[name] = map[string]bool{}

		if description := p.config.Resources[name].Description; description != "" {
			schema["description"] = description
		}

		// Each source schema is a starting point for the description search;
		// recursion narrows them in step with the attribute path.
		sources := make([]any, 0, len(p.config.Resources[name].DescriptionSources))
		for _, schemaName := range p.config.Resources[name].DescriptionSources {
			source, ok := p.spec.schemas[schemaName]
			if !ok {
				return fmt.Errorf("resource %q lists description source %q, which is not a schema in the OpenAPI document",
					name, schemaName)
			}
			sources = append(sources, source)
		}

		if err := p.patchAttributes(name, "", schema["attributes"], sources); err != nil {
			return err
		}
	}
	return nil
}

// patchAttributes walks a code spec attribute list, recursing into nested
// attributes so a path like credentials.password is reachable. sources are the
// OpenAPI schema nodes corresponding to the current level.
func (p *patcher) patchAttributes(resource, prefix string, attributes any, sources []any) error {
	list, _ := attributes.([]any)
	for _, entry := range list {
		attribute := asMap(entry)
		name, _ := attribute["name"].(string)
		if name == "" {
			continue
		}
		typeKey, body := attributeType(attribute)
		if body == nil {
			continue
		}

		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		p.seen[resource][path] = true

		childSources := p.restoreDescription(resource, path, name, body, sources)

		if p.spec.sensitiveNames[name] && body["sensitive"] != true {
			body["sensitive"] = true
			p.markedSensitive++
		}
		if err := p.applyOverride(resource, path, typeKey, body); err != nil {
			return err
		}

		if err := p.patchAttributes(resource, path, nestedAttributes(body), childSources); err != nil {
			return err
		}
	}
	return nil
}

// restoreDescription fills in a missing description from the first source schema
// that has one, and returns the source nodes for the attribute's children.
func (p *patcher) restoreDescription(resource, path, name string, body map[string]any, sources []any) []any {
	var childSources []any
	description := ""
	for _, source := range sources {
		node, ok := p.spec.property(source, name)
		if !ok {
			continue
		}
		childSources = append(childSources, node)
		if description == "" {
			description = p.spec.description(node)
		}
	}

	if existing, ok := body["description"].(string); ok && existing != "" {
		return childSources
	}
	if description == "" {
		if nestedAttributes(body) == nil {
			p.withoutDescription = append(p.withoutDescription, resource+"."+path)
		}
		return childSources
	}
	body["description"] = description
	p.restoredDescriptions++
	return childSources
}

func (p *patcher) applyOverride(resource, path, typeKey string, body map[string]any) error {
	override, ok := p.config.Resources[resource].Attributes[path]
	if !ok {
		return nil
	}
	for key, value := range override {
		switch key {
		case "requires_replace":
			if value == true {
				modifier, err := planModifier(typeKey, "RequiresReplace()")
				if err != nil {
					return fmt.Errorf("%s.%s: %w", resource, path, err)
				}
				body["plan_modifiers"] = append(existingModifiers(body), modifier)
			}
		case "use_state_for_unknown":
			if value == true {
				modifier, err := planModifier(typeKey, "UseStateForUnknown()")
				if err != nil {
					return fmt.Errorf("%s.%s: %w", resource, path, err)
				}
				body["plan_modifiers"] = append(existingModifiers(body), modifier)
			}
		default:
			// Anything else is set verbatim, so a future need does not require a
			// change here — the Provider Code Specification is the contract.
			body[key] = value
		}
	}
	p.appliedOverrides++
	return nil
}

// checkOverridesApplied reports overrides that matched no attribute, which
// almost always means a typo or a path the API no longer has.
func (p *patcher) checkOverridesApplied() error {
	var missing []string
	for resource, config := range p.config.Resources {
		seen, known := p.seen[resource]
		if !known {
			missing = append(missing, fmt.Sprintf("resource %q (not in this code spec)", resource))
			continue
		}
		for path := range config.Attributes {
			if !seen[path] {
				missing = append(missing, resource+"."+path)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("these overrides matched no generated attribute: %s", strings.Join(missing, ", "))
	}
	return nil
}

// attributeType returns an attribute's type key ("string", "single_nested", …)
// and its body. A code spec attribute is {"name": …, "<type>": {…}}.
func attributeType(attribute map[string]any) (string, map[string]any) {
	for key, value := range attribute {
		if key == "name" {
			continue
		}
		if body, ok := value.(map[string]any); ok {
			return key, body
		}
	}
	return "", nil
}

// nestedAttributes returns a container attribute's child attributes. single_nested
// holds them directly; the list/set/map variants hold them under nested_object.
func nestedAttributes(body map[string]any) any {
	if attributes, ok := body["attributes"]; ok {
		return attributes
	}
	if nested, ok := body["nested_object"].(map[string]any); ok {
		return nested["attributes"]
	}
	return nil
}

// planModifierPackages maps a code spec type key to the framework plan modifier
// package for that type.
var planModifierPackages = map[string]string{
	"bool":          "boolplanmodifier",
	"float64":       "float64planmodifier",
	"int32":         "int32planmodifier",
	"int64":         "int64planmodifier",
	"list":          "listplanmodifier",
	"list_nested":   "listplanmodifier",
	"map":           "mapplanmodifier",
	"map_nested":    "mapplanmodifier",
	"number":        "numberplanmodifier",
	"object":        "objectplanmodifier",
	"set":           "setplanmodifier",
	"set_nested":    "setplanmodifier",
	"single_nested": "objectplanmodifier",
	"string":        "stringplanmodifier",
}

func planModifier(typeKey, call string) (map[string]any, error) {
	pkg, ok := planModifierPackages[typeKey]
	if !ok {
		return nil, fmt.Errorf("no plan modifier package known for type %q", typeKey)
	}
	return map[string]any{
		"custom": map[string]any{
			"imports": []any{
				map[string]any{"path": "github.com/hashicorp/terraform-plugin-framework/resource/schema/" + pkg},
			},
			"schema_definition": pkg + "." + call,
		},
	}, nil
}

func existingModifiers(body map[string]any) []any {
	existing, _ := body["plan_modifiers"].([]any)
	return existing
}
