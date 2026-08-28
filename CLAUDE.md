# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is the Terraform Provider for SELECT, a **mostly auto-generated** provider built from SELECT's public OpenAPI specification. The provider enables Infrastructure as Code management of SELECT platform resources.

**Critical Branding Note**: The company name "SELECT" must ALWAYS be capitalized as "SELECT", never "Select" or "select".

## Development Commands

### Essential Commands
- `make reset` - Full regeneration: clean, fetch OpenAPI specs, generate code, build, and install
- `make codegen` - Download both OpenAPI specs (`https://api.select.dev/public_openapi` and `https://api.select.dev/v2/openapi.json`) and generate provider code
- `make codegen-go` - Generate from specs already on disk, skipping the download
- `make build` - Build the provider binary
- `make install` - Install provider locally (includes build)
- `make setup-dev-overrides` - Configure Terraform to use local provider build

### Testing
- `make test-go` - Run the Go unit tests (no API access needed)
- `make test` - Run the Go unit tests and the Terraform provider tests
- `cd tests && terraform test provider.tftest.hcl -filter=test_name` - Run specific test case
- `make test-snowflake` - Snowflake account tests; needs working Snowflake credentials, so it is excluded from `make test`
- `make test-databricks` - Databricks connection tests; needs a working Databricks service principal, so it is excluded from `make test`
- `make test-clean` - Clean up test state files

**Test Requirements**: Tests require environment variables:
```bash
export TF_VAR_select_api_key="your-api-key"
export TF_VAR_select_organization_id="your-org-id"
```

### Documentation
- `make docs` - Generate provider documentation from schema (requires build + dev overrides)

## Architecture

### Code Generation Pipeline

The provider is generated from **two** OpenAPI documents. SELECT's v2 API is a separate FastAPI application mounted at `/v2` with its own document, so each version has its own generator config and its own code spec; `tfplugingen-framework` adds packages rather than replacing its output directory, so the two runs coexist.

1. **Fetch OpenAPI Specs**: `curl https://api.select.dev/public_openapi` and `curl https://api.select.dev/v2/openapi.json`
2. **Generate Schema**: `tfplugingen-openapi` converts each spec → Terraform schema (`internal/provider/provider_code_spec.json` and `provider_code_spec.v2.json`)
3. **Patch Schema (v2 only)**: `go run ./tools/specpatch` fills in what `tfplugingen-openapi` cannot produce — see below
4. **Generate Code**: `tfplugingen-framework` creates final Go code in `internal/provider/`

**Important**: The `internal/provider/` directory is git-ignored and regenerated on every `make codegen` run.

### The limits of `tfplugingen-openapi`

Two limitations shape everything about the generator configs, and are easy to trip over:

- **An attribute override only supports `description`.** The upstream `Override` struct has a single `Description` field, and the config is parsed with a plain `yaml.Unmarshal`, so any other key is dropped silently. `computed_optional_required` entries in `generator_config.yml` are therefore no-ops. There is no config path to `sensitive`, defaults, or plan modifiers.
- **A nullable property loses its description.** `anyOf: [T, null]` maps correctly to `T`, but the description on the outer schema is dropped. Most of the v2 API's properties are written this way.

`tools/specpatch` closes both gaps by patching the generated code spec before `tfplugingen-framework` runs. It restores descriptions from the OpenAPI schemas named in `generator_overrides.v2.yml`, marks every attribute whose property carries `x-terraform-sensitive`, and applies per-attribute plan modifiers. It fails the build if a write-only property is not marked sensitive, or if an override matches no generated attribute.

### Repository Structure

```
internal/
├── provider/               # Generated code (git-ignored, regenerated each build)
│   ├── resource_databricks_connection/
│   ├── resource_snowflake_account/
│   ├── resource_usage_group/
│   └── resource_usage_group_set/
├── provider.go            # Hand-written provider configuration and setup
├── api.go                 # HTTP client, API utilities, type conversion
├── usage_group_resource.go        # Custom resource implementation (connects generated types to API)
├── usage_group_set_resource.go    # Custom resource implementation
├── v2_api.go                      # Conventions every v2 resource shares: If-Match, problem+json, validation reports
├── snowflake_account_resource.go  # Snowflake account resource (v2 API): CRUD and config validation
├── snowflake_account_api.go       # Its request payloads, response mapping, and error formatting
├── databricks_connection_resource.go # Databricks connection resource (v2 API)
└── databricks_connection_api.go      # Its request payloads, response mapping, and error formatting
```

### How Resources Work

Each resource has two parts:
1. **Generated types** in `internal/provider/resource_*/` - Auto-generated from OpenAPI spec, contains schema and model definitions
2. **Resource implementation** in `internal/*_resource.go` - Hand-written glue code that connects generated types to the API client and implements CRUD operations

The resource files (e.g., `usage_group_resource.go`) handle:
- Resource lifecycle (Create, Read, Update, Delete)
- API endpoint construction
- Version management (special SELECT API requirement)
- Error handling

### API Client Architecture

The `api.go` file provides:
- **HTTPClient**: Handles HTTP communication with connection pooling (12 concurrent connections)
- **APIClient**: Higher-level JSON request/response handling with diagnostics
- **Type Conversion**: Bidirectional conversion between Terraform framework types (`types.String`, etc.) and Go primitives for JSON marshaling
- **Version Management**: `GetOrCreateVersion()` ensures all resources in a single apply share the same version (SELECT API requirement)

Key functions:
- `convertTerraformToAPI()` - Converts Terraform types to JSON-serializable Go types
- `updateTerraformFromAPI()` - Updates Terraform model from API response
- `doJSONRequest()` - Handles all HTTP+JSON interaction with proper error handling

## Configuration

### generator_config.yml

Controls code generation behavior:
- **Resource mappings**: Maps API endpoints to Terraform resources
- **Schema overrides**: Customizes field descriptions and validation
- **Attribute aliases**: Renames fields (e.g., `usage_group_set_id` → `id`)
- **Ignored fields**: Excludes API fields that don't map to Terraform (e.g., `filter_expression` due to complex type unions)

Example customization:
```yaml
resources:
  usage_group:
    schema:
      ignores:
        - filter_expression  # Not supported due to complex type unions
      attributes:
        overrides:
          name:
            computed_optional_required: 'required'
```

## Workflow Patterns

### Making Changes to Resources

1. **If changing resource behavior**: Modify `generator_config.yml` (v1) or `generator_config.v2.yml` / `generator_overrides.v2.yml` (v2), then run `make reset`
2. **If modifying API interaction**: Edit `internal/*_resource.go` or `internal/api.go`, then run `make build install`
3. **Never edit files in `internal/provider/`** - they are regenerated and git-ignored

### Adding a New Resource

1. Update `generator_config.yml` with new resource configuration (paths, methods, schema)
2. Run `make reset` to generate types
3. Create `internal/new_resource_resource.go` with CRUD implementation
4. Register in `internal/provider.go`'s `Resources()` method
5. Add tests in `tests/`

### Debugging Provider Issues

When the provider fails:
1. Check if OpenAPI spec fetch is working: `curl -s https://api.select.dev/public_openapi`
2. Verify dev overrides: `cat ~/.terraform.d/.terraformrc`
3. Rebuild completely: `make clean && make reset`
4. Check API client behavior in `internal/api.go` - all HTTP communication goes through `doJSONRequest()`

## Special Considerations

### Version Management

The SELECT API requires that all changes to usage groups within a usage group set in a single Terraform apply must share the same version ID. The `APIClient.GetOrCreateVersion()` method handles this using `sync.Once` to ensure version creation happens exactly once per apply operation.

### Type Conversion

Terraform Plugin Framework uses special types (`types.String`, `types.Int64`, etc.) that must be converted to/from Go primitives for JSON marshaling. The conversion functions in `api.go` handle:
- Null/Unknown state preservation
- JSON normalization (for `filter_expression_json`)
- Reflection-based struct traversal
- Bidirectional mapping using `tfsdk`/`json` tags

### Connection Pooling

The HTTP client is configured with `MaxConnsPerHost: 12` to handle Terraform's default parallelism of 10 concurrent operations, preventing connection exhaustion during large applies.

### v2 API conventions

The v2 surface differs from v1 in ways the client has to honour:

- **Tenancy is a header.** Requests are scoped by `x-tenant-id` rather than an organization ID in the path. `makeRequest` sets it on every request; v1 ignores it.
- **Writes require `If-Match`.** A configurable resource's `etag` must be echoed on update and delete: without it the API answers `428`, and with a stale value `412`. This is why `etag` is a computed attribute persisted in state.
- **Errors are `application/problem+json`** (RFC 9457) carrying `detail` and a stable `code`. `newAPIError` reads them so diagnostics quote the API's own explanation rather than a raw body.
- **Updates are JSON Merge Patch.** An omitted field is left unchanged and `null` clears it. What that implies for a payload depends on the resource: `snowflakeAccountUpdatePayload` sends every clearable field on every update, because removing one from a configuration has to reach the API as the `null` that clears it. `databricksConnectionUpdatePayload` sends only what changed, because nothing on that resource can be cleared and SELECT re-validates against Databricks whenever the access-related fields are merely *present*. Read the API's update schema before deciding which shape a new resource needs.
- **`doRequest` returns the status.** `doJSONRequest` flattens everything into diagnostics (and reports a 404 as a warning) for the v1 resources; callers that need to act on a status, such as removing a deleted resource from state, use `doRequest` directly.

## Testing Notes

Tests use Terraform's native testing framework (`terraform test`). Each test case:
1. Creates resources
2. Validates state
3. Tests updates
4. Cleans up resources

Tests run against the live SELECT API and create real resources. Always run `make test-clean` after test failures to prevent orphaned resources.
