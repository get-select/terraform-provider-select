# Terraform Provider Tests

Integration tests for the SELECT Terraform provider using Terraform's built-in
testing framework.

There are two kinds. `provider.tftest.hcl` covers usage groups and needs only a
SELECT API key. The four `*_connection.tftest.hcl` / `snowflake_account.tftest.hcl`
suites manage **real connections**: creating one makes SELECT validate the
configuration against Snowflake, Databricks, BigQuery or S3 for real, so they
need working credentials for the system being connected and are kept out of
`make test`.

## Setup

1. **Set environment variables**:
   ```bash
   export TF_VAR_select_api_key="your-api-key"
   export TF_VAR_select_organization_id="your-org-id"
   # Optional; defaults to a backend on http://localhost:8000
   export TF_VAR_select_api_url="https://api.select.dev"
   ```

2. **Install the provider**:
   ```bash
   make install
   make setup-dev-overrides
   ```

## Running tests

```bash
make test              # Go unit tests + the usage group suite
make test-snowflake    # one connection suite at a time
make test-databricks
make test-bigquery
make test-aws
make test-connections  # all four
make test-clean        # remove local state files
make test-sweep        # delete connections a failed run left behind
```

Individual cases:

```bash
cd tests
terraform test provider.tftest.hcl -filter=create_usage_group_set
```

## What the connection suites cover

Each one walks a full create → update → delete cycle against the live API:

- **create** — the resource lands in state with what SELECT resolved from the
  system being connected, including the ETag every later write depends on, and
  the API's own defaults resolve at plan time rather than staying unknown.
- **update** — a rename, and whichever field that resource can clear or toggle,
  are in-place updates rather than a replacement.
- **delete** — setting the suite's `enable_*` variable to `false` takes the
  resource out of the configuration, which issues a real `DELETE`. Terraform
  destroys whatever is left at the end of a file anyway, but that teardown
  asserts nothing and swallows what it cannot remove, so the delete is a run
  block of its own.

What the suites deliberately do *not* cover is anything settled before a request
is sent — credential rules, field combinations, which fields a patch omits. Those
are in the Go tests under `internal/`, where they cost nothing to run.

One thing to know before adding a run block: Terraform exposes `var.*` to an
assertion's condition but **not** inside a run's own `variables` block, so a run
can only assign literals. That is why renaming goes through a `*_name_suffix`
variable rather than building the new name inline — the value is composed in
`main.tf`, and the run block just supplies the suffix.

## Credentials

Each suite is gated by an `enable_*` variable, so with none of them set there are
no connection resources in the configuration and nothing calls out. Set the
variables for the one you want to run.

| Suite | Variables |
|---|---|
| Snowflake | `snowflake_account_id`, `snowflake_account_name`, `snowflake_username`, `snowflake_private_key`, `snowflake_role`, `snowflake_warehouse`, `snowflake_export_storage_integration_name` |
| Databricks | `databricks_connection_name`, `databricks_account_id`, `databricks_workspace_url`, `databricks_warehouse_id`, `databricks_client_id`, `databricks_client_secret` |
| BigQuery | `bigquery_connection_name`, `bigquery_gcp_project_id`, `bigquery_dataset_id`, `bigquery_billing_account_id`, `bigquery_service_account` |
| AWS | `aws_connection_name`, `aws_payer_account_id`, `aws_s3_bucket`, `aws_s3_prefix`, `aws_region`, `aws_access_key_id`, `aws_secret_access_key` |

One of these is not obvious: **`bigquery_service_account`** is not a credential
this test holds. Access comes from the SELECT backend impersonating that service
account, so the grant lives in the target GCP project's IAM, not here.

## In CI

`.github/workflows/e2e.yaml` runs all four suites as a matrix against the
deployed API using a dedicated test organization. Credentials come from GitHub
secrets mapped to the `TF_VAR_` names above — the same mechanism the select
repo's `test-e2e.yaml` uses. The `E2E_CREATE_*` secrets are shared with that
workflow; the `TF_E2E_*` ones belong to this repo.

Two things keep runs from tripping over each other:

- Every connection is named `terraform-test-<run id>-<platform>`, and the
  workflow takes a `concurrency` lock. SELECT refuses a second connection with a
  name already in use, and the same Snowflake account identifier cannot be added
  to an organization twice, so runs have to queue rather than overlap.
- `scripts/ci-cleanup-connections.sh` sweeps before and after. A run cancelled
  mid-apply leaves a connection attached, and its name is then taken for good.
  `make test-sweep` runs the same script locally.

## Troubleshooting

### `Error: Unauthorized`
Verify your environment variables. The connection suites need an API key with the
read and write scopes for the resource in question — `snowflake_accounts:*`,
`databricks_connections:*`, `bigquery_connections:*`, `aws_accounts:*`.

### `Error: Could not find required provider`
Run `make install && make setup-dev-overrides`.

### A name is already in use
A previous run left a connection behind. `make test-sweep` removes anything named
`terraform-test*` from the organization.

### `Error: ... has changed since Terraform last read it`
The resource was modified outside Terraform between the read that recorded the
ETag and the write. Usually means two runs overlapped.
