# Terraform Provider Tests

Integration tests for the SELECT Terraform provider using Terraform's built-in
testing framework.

There are three kinds. `provider.tftest.hcl` covers usage groups and needs only
a SELECT API key. The four `*_connection.tftest.hcl` / `snowflake_account.tftest.hcl`
suites manage **real connections**: creating one makes SELECT validate the
configuration against Snowflake, Databricks, BigQuery or S3 for real, so they
need working credentials for the system being connected and are kept out of
`make test`. `budget.tftest.hcl` manages a real budget, but creating one makes
no call to an external system — SELECT stores the definition directly — so it
needs nothing beyond the same API key every suite already uses. `team.tftest.hcl`
is the same: it manages a real team, one member and the `select_team` data
source, and calls no external system. So is `role.tftest.hcl`: it grants a team
role, a user role and a default role, on a team, usage group set and usage
group it makes itself, and reads the `select_users` data source. So is `sso_group.tftest.hcl`: it manages an SSO group
with two roles, and a team member that refers to the group.

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
make test-budget       # no credentials of its own; joins CI's e2e matrix
make test-team         # no credentials of its own; joins CI's e2e matrix
make test-role         # no credentials of its own; joins CI's e2e matrix
make test-sso-group    # no credentials of its own; joins CI's e2e matrix
make test-clean        # remove local state files
make test-sweep        # delete connections, budgets, usage group sets, teams, SSO groups and role grants a failed run left behind
```

Individual cases:

```bash
cd tests
terraform test provider.tftest.hcl -filter=create_usage_group_set
```

## What the connection suites cover

Each one walks a full create → update → delete cycle against the live API.
`budget.tftest.hcl`, `team.tftest.hcl`, `role.tftest.hcl` and
`sso_group.tftest.hcl` follow the same shape, minus anything that depends on an
external system. A team role grant and a default role grant cannot change in
place, so the role suite's update step replaces the team role grant instead.
The SSO group suite's update step renames the group, grants one role and
revokes one role in the same apply:

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
| Budget | `budget_name` — nothing else; creating a budget makes no call to an external system |
| Team | `team_name`, and optionally `team_member_email` — the user the suite adds to its team. CI reads it from the `TF_E2E_TEAM_MEMBER_EMAIL` repository variable |
| SSO group | `sso_group_name_prefix` — nothing else. No identity provider group has the test name, so the roles reach nobody |
| Role | `role_name_prefix`, and optionally `role_user_email` — the email the suite grants a user role to. Keep it an address that belongs to no real user. CI sets a new one on every run, `terraform-test-<run id>-role-user@example.com` |

One of these is not obvious: **`bigquery_service_account`** is not a credential
this test holds. Access comes from the SELECT backend impersonating that service
account, so the grant lives in the target GCP project's IAM, not here.

## In CI

`.github/workflows/e2e.yaml` runs the connection suites, budget, usage groups,
teams, role grants and SSO groups as a matrix against the deployed API using a dedicated test
organization — Databricks, BigQuery, AWS, Budget, Usage Group, Team, Role and SSO Group. Credentials come from GitHub secrets mapped to the
`TF_VAR_` names above — the same mechanism the select repo's `test-e2e.yaml`
uses, though every secret here is its own copy rather than shared with it.
select's equivalents are named `E2E_CREATE_*` because it also runs e2e tests
against *pre-existing* Snowflake connections (so `CREATE_` distinguishes the
ones its create-flow test provisions); this repo never has that second kind, so
its names drop the `CREATE_`.

**Snowflake is excluded from the matrix for now.** SELECT enforces one global
claim per Snowflake organization across every SELECT org, not per-account, and
every Snowflake test account currently available is already claimed by a
different org — adding it under ours fails with `409 Unable to add Snowflake
account`, not anything this provider or CI can fix. `make test-snowflake` still
works locally once a dedicated, unclaimed fixture exists, and the matrix entry
is a one-line change at that point.

Two things keep runs from tripping over each other:

- Every resource is named `terraform-test-<run id>-<platform>`, and the
  workflow takes a `concurrency` lock. SELECT refuses a second connection with a
  name already in use, and the same Snowflake account identifier cannot be added
  to an organization twice, so runs have to queue rather than overlap.
- `scripts/ci-cleanup-connections.sh` sweeps before and after, budgets, usage
  group sets, teams and SSO groups included. Deleting an SSO group also
  deletes its roles.
  A run cancelled mid-apply leaves a resource attached, and its name is then
  taken for good. `make test-sweep` runs the same script locally.
- A role grant has no name to carry the run id. A team role grant goes with its
  team. The script deletes a default role grant only when it is scoped to a
  usage group in a set with the prefix, which is how the role suite scopes its
  own. If it cannot delete such a grant, it keeps the set for that run, so a
  later sweep can still find the grant. It deletes the direct grants of each
  email in `CI_SWEEP_USER_EMAILS` that starts with the prefix. CI gives the
  role suite a new email on every run, `terraform-test-<run id>-role-user@example.com`,
  and the sweep after the role leg cleans it. A sweep cannot list the emails
  of earlier runs, so a grant that one of them leaked stays. That is harmless:
  the grant is on an example.com address that is not a user, and no later run
  uses it.

## Troubleshooting

### `Error: Unauthorized`
Verify your environment variables. Each suite needs an API key with the read
and write scopes for the resource in question — `snowflake_accounts:*`,
`databricks_connections:*`, `bigquery_connections:*`, `aws_accounts:*`,
`budgets:*`, `teams:*`. The role suite also makes a usage group, so it needs
the usage group scopes, and it needs `users:*` and `default_roles:*` (assumed
names: the API spec does not name its scopes). The SSO group suite needs
`sso_groups:*` (also assumed), `teams:*` and the usage group scopes.

### `Error: Could not find required provider`
Run `make install && make setup-dev-overrides`.

### A name is already in use
A previous run left a connection behind. `make test-sweep` removes anything named
`terraform-test*` from the organization.

### `Error: ... has changed since Terraform last read it`
The resource was modified outside Terraform between the read that recorded the
ETag and the write. Usually means two runs overlapped.
