## About

This script generates Terraform resources that match your existing usage groups in SELECT. 

**Use cases:**
- **Migration**: Convert existing usage groups to Terraform management
- **Initial setup**: Configure usage groups in the SELECT UI, then export them to Terraform

For a step-by-step guide, see the [migration documentation](https://select.dev/docs/reference/usage-guide/migrating-usage-groups-to-terraform).

## Output

The script writes one `.tf` file per usage group set, holding that set and the
usage groups in it, plus a `main.tf` with the provider configuration and an
`import.sh` that brings the existing resources into state.

Run `terraform fmt` over the output before committing it. The generated files
are valid HCL but not canonically laid out.

## Notes

- Requires an API key with the `usage_groups:read` scope.
- Reads the SELECT v2 API. Usage group sets no longer carry
  `snowflake_account_uuid` or `snowflake_organization_name`, so the output is a
  flat directory rather than one module per Snowflake account.
