# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is the Terraform Provider for SELECT, a **mostly auto-generated** provider built from SELECT's public OpenAPI specification. The provider enables Infrastructure as Code management of SELECT platform resources.

**Critical Branding Note**: The company name "SELECT" must ALWAYS be capitalized as "SELECT", never "Select" or "select".

## Development Commands

### Essential Commands
- `make reset` - Full regeneration: clean, fetch OpenAPI specs, generate code, build, and install
- `make codegen` - Download the OpenAPI spec (`https://api.select.dev/v2/openapi.json`) and generate provider code
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
- `make test-bigquery` - BigQuery connection tests; needs a working GCP project and service account, so it is excluded from `make test`
- `make test-aws` - AWS connection tests; needs an AWS payer account with a working CUR delivery, so it is excluded from `make test`
- `make test-connections` - All four connection suites
- `make test-budget` - Budget tests; creates no real connection, so it needs only the same API key every suite already uses
- `make test-team` - Team, team member and team data source tests; like budget, needs only the API key
- `make test-role` - Team role, user role and default role grant tests; like budget, needs only the API key
- `make test-sso-group` - SSO group tests (inline roles, rename, a team member of type `sso_group`); like budget, needs only the API key
- `make test-sweep` - Delete connections, budgets, usage group sets, teams, SSO groups and role grants a failed run left attached to the organization
- `make test-clean` - Clean up test state files

**Test Requirements**: Tests require environment variables:
```bash
export TF_VAR_select_api_key="your-api-key"
export TF_VAR_select_organization_id="your-org-id"
# Optional; defaults to a backend on http://localhost:8000
export TF_VAR_select_api_url="https://api.select.dev"
```

The connection suites need credentials for the system being connected as well.
See `tests/README.md` for the full list and for how CI supplies them.

### Documentation
- `make docs` - Generate provider documentation from schema (requires build + dev overrides)

## Architecture

### Code Generation Pipeline

Every resource is generated from SELECT's v2 API document.

1. **Fetch OpenAPI Spec**: `curl https://api.select.dev/v2/openapi.json`
2. **Generate Schema**: `tfplugingen-openapi` converts it → Terraform schema (`internal/provider/provider_code_spec.v2.json`)
3. **Patch Schema**: `go run ./tools/specpatch` fills in what `tfplugingen-openapi` cannot produce — see below
4. **Generate Code**: `tfplugingen-framework` creates final Go code in `internal/provider/`

The legacy v1 public API is gone. Usage groups were its last consumer; they moved to v2 in AUT-15, taking `generator_config.yml`, the `openapi.public.json` fetch and the second codegen pass with them.

**Important**: The `internal/provider/` directory is git-ignored and regenerated on every `make codegen` run.

### The limits of `tfplugingen-openapi`

Two limitations shape everything about the generator configs, and are easy to trip over:

- **An attribute override only supports `description`.** The upstream `Override` struct has a single `Description` field, and the config is parsed with a plain `yaml.Unmarshal`, so any other key is dropped silently. A `computed_optional_required` entry in `generator_config.v2.yml` is therefore a no-op — it belongs in `generator_overrides.v2.yml`. There is no config path to `sensitive`, defaults, or plan modifiers.
- **A nullable property loses its description.** `anyOf: [T, null]` maps correctly to `T`, but the description on the outer schema is dropped. Most of the v2 API's properties are written this way.

`tools/specpatch` closes both gaps by patching the generated code spec before `tfplugingen-framework` runs. It restores descriptions from the OpenAPI schemas named in `generator_overrides.v2.yml`, marks every attribute whose property carries `x-terraform-sensitive`, and applies per-attribute plan modifiers. It fails the build if a write-only property is not marked sensitive, or if an override matches no generated attribute.

### Repository Structure

```
internal/
├── provider/               # Generated code (git-ignored, regenerated each build)
│   ├── resource_aws_connection/
│   ├── resource_bigquery_connection/
│   ├── resource_budget/
│   ├── resource_databricks_connection/
│   ├── resource_default_role/
│   ├── resource_snowflake_account/
│   ├── resource_sso_group/
│   ├── resource_team/
│   ├── resource_team_member/
│   ├── resource_team_role/
│   ├── resource_usage_group/
│   ├── resource_usage_group_set/
│   └── resource_user_role/
├── provider.go            # Hand-written provider configuration and setup
├── api.go                 # HTTP client, API utilities, type conversion
├── usage_group_resource.go        # Custom resource implementation (connects generated types to API)
├── usage_group_set_resource.go    # Custom resource implementation
├── v2_api.go                      # Conventions every v2 resource shares: If-Match, problem+json, validation reports
├── v2_resource.go                  # Generic v2 CRUD (Create/Read/Update/Delete) shared by every v2 resource
├── v2_convert.go                   # Field-conversion and drift-avoidance helpers the v2 resources' payload builders share
├── snowflake_account_resource.go  # Snowflake account resource (v2 API): CRUD and config validation
├── snowflake_account_api.go       # Its request payloads, response mapping, and error formatting
├── databricks_connection_resource.go # Databricks connection resource (v2 API)
├── databricks_connection_api.go      # Its request payloads, response mapping, and error formatting
├── bigquery_connection_resource.go   # BigQuery connection resource (v2 API)
├── bigquery_connection_api.go        # Its request payloads, response mapping, and error formatting
├── aws_connection_resource.go        # AWS connection resource (v2 API)
├── aws_connection_api.go             # Its request payloads, response mapping, and error formatting
├── budget_resource.go                # Budget resource (v2 API): hand-written period attribute, CRUD and config validation
├── budget_api.go                     # Its request payloads, response mapping, and error formatting
├── v2_list.go                        # List pagination (page_token/max_results) and list-and-find reads for v2 list endpoints
├── v2_data_source.go                 # Generic v2 data source (Configure/Read) shared by every data source
├── role_grant_scope.go               # The shared role grant `scope {type, id}` attribute and its request/response mapping
├── team_resource.go / team_api.go    # Team resource (v2 API)
├── team_member_resource.go / team_member_api.go # Team member resource: no GET-by-id, so Read lists and finds
├── role_grant.go                     # What every role grant shares: create payload, response fields, role-on-scope check, 409 hint
├── team_role_resource.go / team_role_api.go       # Team role grant: no GET-by-id, no update, no ETag
├── user_role_resource.go / user_role_api.go       # User role grant: direct grants only; a default or team-inherited grant reads as gone
├── default_role_resource.go / default_role_api.go # Default role grant: no GET-by-id, no update
├── sso_group_resource.go / sso_group_api.go       # SSO group with an inline roles set; hand-written CRUD, not a v2Resource
└── team_data_source.go               # select_team data source: look up a team by exact name
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
- **Version Management**: `EnsureVersion()` records one version per usage group set per apply, before that apply changes any of the set's groups

Key functions:
- `doRequest()` - Handles all HTTP+JSON interaction, returning the API's status as an `*apiError` so a caller can branch on it
- `EnsureVersion()` / `VersionRecorded()` - Usage group set versioning, once per set per apply

Request and response bodies are plain Go structs built by each resource's own `build*`/`apply*` functions in `internal/*_api.go`. The reflection layer that converted Terraform framework types in and out of JSON went with the v1 resources that needed it.

## Configuration

### generator_config.v2.yml

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
            description: The group's display name. Must be unique within its set.
```

Anything beyond a description — `computed_optional_required`, `requires_replace`, `use_state_for_unknown` — goes in `generator_overrides.v2.yml`, which `specpatch` applies. Note that `requires_replace` and `use_state_for_unknown` are matched against the literal `true`; any other value is silently ignored.

## Workflow Patterns

### Making Changes to Resources

1. **If changing resource behavior**: Modify `generator_config.v2.yml` / `generator_overrides.v2.yml`, then run `make reset`
2. **If modifying API interaction**: Edit `internal/*_resource.go` or `internal/api.go`, then run `make build install`
3. **Never edit files in `internal/provider/`** - they are regenerated and git-ignored

### Adding a New Resource

1. Update `generator_config.v2.yml` with new resource configuration (paths, methods, schema), and `generator_overrides.v2.yml` with anything beyond descriptions
2. Run `make reset` to generate types
3. Create `internal/new_resource_api.go` with payload/response structs and their builders
4. Create `internal/new_resource_resource.go`, populating a `v2Resource` — see `usage_group_resource.go` for a resource nested under a parent, and `budget_resource.go` for one needing hand-written schema
5. Register in `internal/provider.go`'s `Resources()` method (a data source goes in `DataSources()`)
6. Add tests in `internal/` (unit) and `tests/` (acceptance)

### Debugging Provider Issues

When the provider fails:
1. Check if OpenAPI spec fetch is working: `curl -s https://api.select.dev/v2/openapi.json`
2. Verify dev overrides: `cat ~/.terraform.d/.terraformrc`
3. Rebuild completely: `make clean && make reset`
4. Check API client behavior in `internal/api.go` - all HTTP communication goes through `doJSONRequest()`

## Special Considerations

### Version Management

A usage group set version is a frozen copy of the set's groups. The newest one holds the live groups; the rest are restorable checkpoints. Usage group writes change the newest version in place and never add one, so without intervention an apply would overwrite what the previous apply left behind with nothing kept.

`APIClient.EnsureVersion()` records a version through `POST /v2/usage-group-sets/{id}/versions` before an apply's first change to a given set's groups, so each apply leaves exactly one checkpoint of the state it started from. Terraform gives a provider no apply-level hook, so this has to happen inside the first write that needs it; the `APIClient` lives for one apply, so per-set state on it is per-apply state.

Two details are easy to get wrong:

- **Versions are tracked per set, not per client.** An apply touching several sets records one version for each. The v1 implementation held a single `sync.Once` on the client, so only the first set ever got one.
- **Recording a version rotates the set's ETag.** A later write in the same apply then carries an ETag this provider itself invalidated, which the API answers with a 412. That is not the user's to fix, so `v2Resource.write` retries once against a freshly read ETag — but only when the resource's `selfInflicted412` hook says this apply recorded a version for the set in question. A 412 from a genuine outside change still reaches the user, which is the whole point of sending `If-Match`.

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
- **Updates are JSON Merge Patch.** An omitted field is left unchanged and `null` clears it. What that implies for a payload depends on the resource: `snowflakeAccountUpdatePayload` sends every clearable field on every update, because removing one from a configuration has to reach the API as the `null` that clears it. `databricksConnectionUpdatePayload` sends only what changed, because nothing on that resource can be cleared and SELECT re-validates against Databricks whenever the access-related fields are merely *present*. `budgetUpdatePayload` also sends only what changed — its API 422s on an explicit null for `name`, `amount`, `period` and `started_at` — but unlike Databricks it does have two clearable fields, `team_id` and `filter_expression_json`, so those two use `*nullableString` for the three-state omit/null/value a plain pointer cannot express. Read the API's update schema before deciding which shape a new resource needs.
- **Not every property survives codegen.** A `oneOf` discriminated union (budget's `period`) makes `tfplugingen-openapi` drop the whole resource with "schema composition is currently not supported," and a recursive `anyOf` (budget's `filter_expression`) is both ungeneratable and, on this API, dangerous to emit at all — sending both `filter_expression` and `filter_expression_json` 422s, and an explicit null counts as present. Both go in the resource's `ignores` list in `generator_config.v2.yml`; `period` is then hand-written as a `types.Object` attribute injected into the generated schema, and `filter_expression_json`, a plain JSON-encoded string, is the resource's whole filter surface.
- **Some child resources have no GET-by-id.** Team members and role grants can only be listed. Set the `v2Resource.fetch` hook to `v2ListAndFind(...)`: Read pages through the list and answers a missing item with a 404, so the resource leaves state like any deleted resource. `tfplugingen-openapi` still requires a `read` operation, so `generator_config.v2.yml` points `read` at the create operation, which has the same response schema and path parameters.
- **Some resources have no update and no ETag.** A team role grant and a default role grant cannot change: make every attribute force replacement and set `updatePayload: v2NoUpdate[...]()`. A team role grant also has no ETag; its `identity` returns a null `Etag`, and `ifMatchHeader` then sends no `If-Match`.
- **An SSO group carries its roles inline.** `POST /sso-groups` needs at least one role and the API refuses to revoke the last one, so `select_sso_group` has a `roles` set and there is no SSO group role resource. It is hand-written, not a `v2Resource`: Read is two requests (get the group, list its roles), the group's id is its name so a rename changes the URL, and an update is many requests. Update renames first (PATCH with `If-Match`) and writes the new id to state at once, then grants the added roles, then revokes the removed roles, so the group never has zero roles, then gets the group for its new ETag and lists the roles. State then takes the plan; a role list that differs from the plan gives a warning, not state that differs from the plan, which Terraform would report as a provider bug. A failed step writes what SELECT holds after the steps before it. A rename that fails with a 5xx or no readable response can still have happened, so Update moves state to the new name only when the old name answers 404 and the new name holds a group. A set element holds only `role` and `scope`, with no grant id: a computed value in an element is unknown at plan time and makes every plan replace the element. Roles are compared by role, scope type and scope id without case, so a case-only change sends nothing, and Read pairs each listed grant with its configured element to keep the configured form.
- **Lists are paginated.** Use `v2ListAll` / `v2FindInList` from `v2_list.go`; they send `max_results` and follow `page_token` until it is empty.
- **Data sources are hand-written.** The generator builds a data source only from a GET-by-id route, and a lookup by name has none. `v2DataSource` supplies Configure/Metadata/Schema/Read; a data source gives it a schema and a lookup function.
- **`doRequest` returns the status.** `doJSONRequest` flattens everything into diagnostics (and reports a 404 as a warning) for the v1 resources; callers that need to act on a status, such as removing a deleted resource from state, use `doRequest` directly.

## Testing Notes

Tests use Terraform's native testing framework (`terraform test`). Each test case:
1. Creates resources
2. Validates state
3. Tests updates
4. Cleans up resources

Tests run against the live SELECT API and create real resources. Always run `make test-clean` after test failures to prevent orphaned resources.

The connection suites go further: each one drives a full create → update → delete cycle, with the delete written as a run block that flips the suite's `enable_*` variable to `false`. Terraform's own teardown would destroy the resource anyway, but it asserts nothing and swallows what it cannot remove, so a delete the API refuses has to fail the test rather than pass quietly. When one of these fails partway it leaves a connection attached to the organization, and the name is then in use for good — `make test-sweep` clears it.

CI runs Databricks, BigQuery, AWS, Budget, Usage Group, Team, Role and SSO Group from `.github/workflows/e2e.yaml` against the deployed API with a dedicated test organization, taking credentials from GitHub secrets. Because one organization backs every run, the workflow serializes on a `concurrency` group and names each resource after the run id. Snowflake is excluded from CI for now: SELECT claims a Snowflake organization globally, not per SELECT org, and every test account currently available is already claimed elsewhere. `make test-snowflake` still works locally against a dedicated, unclaimed fixture. Budget needs no credentials of its own — creating one makes no call to an external system — so it joins the matrix without adding any secrets.
