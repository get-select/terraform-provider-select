# SPDX-License-Identifier: MPL-2.0

# Main configuration file for Terraform provider testing

terraform {
  required_providers {
    select = {
      source = "get-select/select"
    }
  }
}

# Input variables that will be set by tests or environment
variable "select_api_key" {
  description = "API key for the Select provider"
  type        = string
  sensitive   = true
}

variable "select_organization_id" {
  description = "Organization ID for the Select provider"
  type        = string
}

# Which SELECT API the tests apply against. A local run defaults to a backend on
# localhost; CI points this at the deployed API. Switching between them is an
# environment variable rather than an edit here.
variable "select_api_url" {
  description = "Base URL of the SELECT API the tests run against"
  type        = string
  default     = "http://localhost:8000"
}

# Whether to manage the usage group set/group resources below. Default true so
# provider.tftest.hcl and `make test`/`test-all` are unaffected; each connection
# suite sets this false in its own file, since it shares this root module but
# has nothing to do with usage groups.
variable "enable_usage_group_tests" {
  description = "Whether to manage the usage group set/group test resources"
  type        = bool
  default     = true
}

# Test-specific variables with defaults
variable "usage_group_set_name" {
  description = "Name for the usage group set"
  type        = string
  default     = "test-usage-group-set"
}

variable "usage_group_set_order" {
  description = "Order for the usage group set"
  type        = number
  default     = 1
}

variable "test_team_id" {
  description = "Test team UUID"
  type        = string
  default     = "2f0899e2-2746-4300-887c-524e64b5a138"
}

variable "usage_group_name" {
  description = "Name for the usage group"
  type        = string
  default     = "test-usage-group"
}

variable "usage_group_order" {
  description = "Order for the usage group"
  type        = number
  default     = 1
}

variable "usage_group_budget" {
  description = "Budget for the usage group"
  type        = number
  default     = null
}

# Filter expression variables (as strings to avoid function call issues)
variable "simple_filter_expression_json" {
  description = "Simple filter expression in JSON format"
  type        = string
  default     = "{\"filters\":[{\"field\":\"warehouse_name\",\"operator\":\"in\",\"values\":[\"SELECT_BACKEND\"]}],\"operator\":\"or\"}"
}

variable "complex_filter_expression_json" {
  description = "Complex filter expression in JSON format"
  type        = string
  default     = "{\"filters\":[{\"field\":\"warehouse_name\",\"operator\":\"in\",\"values\":[\"SELECT_BACKEND\"]},{\"field\":\"role_name\",\"operator\":\"in\",\"values\":[\"SELECT_BACKEND\",\"SELECT_CI\"]}],\"operator\":\"or\"}"
}

# Snowflake account test variables.
#
# Creating a Snowflake account makes SELECT validate the configuration against
# Snowflake for real, so these tests need credentials that work and are off by
# default. Set enable_snowflake_account_tests and the rest, then run
# `make test-snowflake`.
variable "enable_snowflake_account_tests" {
  description = "Whether to manage a real Snowflake account. Creating one requires working Snowflake credentials."
  type        = bool
  default     = false
}

variable "snowflake_account_id" {
  description = "Snowflake account identifier in ORGANIZATION-ACCOUNT form"
  type        = string
  default     = "terraform-test-org-terraform-test-account"
}

variable "snowflake_account_name" {
  description = "Display name for the Snowflake account in SELECT"
  type        = string
  default     = "terraform-test-account"
}

# The rename tests need a second name. A run block cannot build one — Terraform
# does not expose var.* inside a run's variables block — so the suffix is its own
# variable and the name is composed below.
variable "snowflake_account_name_suffix" {
  description = "Appended to the Snowflake account name, so a run block can rename without restating it"
  type        = string
  default     = ""
}

variable "snowflake_username" {
  description = "Snowflake user SELECT connects as"
  type        = string
  default     = "SELECT_USER"
}

variable "snowflake_private_key" {
  description = "PEM-encoded private key for the Snowflake user"
  type        = string
  sensitive   = true
  default     = ""
}

variable "snowflake_role" {
  description = "Snowflake role SELECT assumes"
  type        = string
  default     = "SELECT_ROLE"
}

variable "snowflake_warehouse" {
  description = "Snowflake warehouse SELECT runs its queries on"
  type        = string
  default     = "SELECT_WH"
}

variable "snowflake_sync_enabled" {
  description = "Whether SELECT syncs data for the test account"
  type        = bool
  default     = true
}

variable "snowflake_export_storage_integration_name" {
  description = "Snowflake storage integration used to export data to SELECT"
  type        = string
  default     = "SELECT_STORAGE_INTEGRATION"
}

# Databricks connection test variables.
#
# Creating a Databricks connection makes SELECT validate the configuration
# against Databricks for real, so these tests need a service principal that works
# and are off by default. Set enable_databricks_connection_tests and the rest,
# then run `make test-databricks`.
variable "enable_databricks_connection_tests" {
  description = "Whether to manage a real Databricks connection. Creating one requires working Databricks credentials."
  type        = bool
  default     = false
}

variable "databricks_connection_name" {
  description = "Display name for the Databricks connection in SELECT"
  type        = string
  default     = "terraform-test-databricks-connection"
}

# The rename tests need a second name. A run block cannot build one — Terraform
# does not expose var.* inside a run's variables block — so the suffix is its own
# variable and the name is composed below.
variable "databricks_connection_name_suffix" {
  description = "Appended to the Databricks connection name, so a run block can rename without restating it"
  type        = string
  default     = ""
}

variable "databricks_account_id" {
  description = "Databricks account SELECT reads usage and spend for"
  type        = string
  default     = "00000000-0000-0000-0000-000000000000"
}

variable "databricks_workspace_url" {
  description = "URL of the workspace SELECT connects through, including the scheme"
  type        = string
  default     = "https://terraform-test.cloud.databricks.com"
}

variable "databricks_warehouse_id" {
  description = "SQL warehouse in that workspace SELECT runs its queries on"
  type        = string
  default     = "0000000000000000"
}

variable "databricks_client_id" {
  description = "Client ID of the service principal SELECT authenticates as"
  type        = string
  default     = ""
}

variable "databricks_client_secret" {
  description = "OAuth secret generated for that service principal"
  type        = string
  sensitive   = true
  default     = ""
}

variable "databricks_sync_enabled" {
  description = "Whether SELECT syncs data for the test connection"
  type        = bool
  default     = true
}

# BigQuery connection test variables.
#
# Creating a BigQuery connection makes SELECT validate the configuration
# against BigQuery for real, so these tests need a GCP project and service
# account that work and are off by default. Set enable_bigquery_connection_tests
# and the rest, then run `make test-bigquery`.
variable "enable_bigquery_connection_tests" {
  description = "Whether to manage a real BigQuery connection. Creating one requires a working GCP project and service account."
  type        = bool
  default     = false
}

variable "bigquery_connection_name" {
  description = "Display name for the BigQuery connection in SELECT"
  type        = string
  default     = "terraform-test-bigquery-connection"
}

# The rename tests need a second name. A run block cannot build one — Terraform
# does not expose var.* inside a run's variables block — so the suffix is its own
# variable and the name is composed below.
variable "bigquery_connection_name_suffix" {
  description = "Appended to the BigQuery connection name, so a run block can rename without restating it"
  type        = string
  default     = ""
}

variable "bigquery_gcp_project_id" {
  description = "GCP project SELECT reads BigQuery usage and spend from"
  type        = string
  default     = "terraform-test-project"
}

variable "bigquery_dataset_id" {
  description = "BigQuery dataset holding the project's billing and pricing exports"
  type        = string
  default     = "billing_export"
}

variable "bigquery_billing_account_id" {
  description = "Cloud Billing account the exports belong to, in XXXXXX-XXXXXX-XXXXXX form"
  type        = string
  default     = "000000-000000-000000"
}

variable "bigquery_service_account" {
  description = "GCP service account SELECT impersonates to reach the project"
  type        = string
  default     = "select@terraform-test-project.iam.gserviceaccount.com"
}

variable "bigquery_sync_enabled" {
  description = "Whether SELECT syncs data for the test connection"
  type        = bool
  default     = true
}

# AWS connection test variables.
#
# Creating an AWS connection makes SELECT read the Cost and Usage Report out of
# S3 for real, so these tests need an AWS payer account with a working CUR
# delivery and are off by default. Set enable_aws_connection_tests and the rest,
# then run `make test-aws`.
variable "enable_aws_connection_tests" {
  description = "Whether to manage a real AWS connection. Creating one requires an AWS payer account with a working CUR delivery."
  type        = bool
  default     = false
}

variable "aws_connection_name" {
  description = "Display name for the AWS connection in SELECT"
  type        = string
  default     = "terraform-test-aws-connection"
}

# The rename tests need a second name. A run block cannot build one — Terraform
# does not expose var.* inside a run's variables block — so the suffix is its own
# variable and the name is composed below.
variable "aws_connection_name_suffix" {
  description = "Appended to the AWS connection name, so a run block can rename without restating it"
  type        = string
  default     = ""
}

variable "aws_payer_account_id" {
  description = "12-digit AWS payer account SELECT reads spend for"
  type        = string
  default     = "000000000000"
}

variable "aws_s3_bucket" {
  description = "S3 bucket AWS delivers the payer account's Cost and Usage Report to"
  type        = string
  default     = "terraform-test-cur"
}

variable "aws_s3_prefix" {
  description = "Path within the bucket the report is delivered under. Null means the root of the bucket."
  type        = string
  default     = "reports"
}

variable "aws_region" {
  description = "AWS region the bucket is in"
  type        = string
  default     = "us-east-1"
}

variable "aws_access_key_id" {
  description = "Access key id of the IAM user SELECT authenticates as"
  type        = string
  default     = ""
}

variable "aws_secret_access_key" {
  description = "Secret access key paired with aws_access_key_id"
  type        = string
  sensitive   = true
  default     = ""
}

variable "aws_sync_enabled" {
  description = "Whether SELECT syncs data for the test connection"
  type        = bool
  default     = true
}

# Provider configuration
provider "select" {
  api_key         = var.select_api_key
  organization_id = var.select_organization_id
  select_api_url  = var.select_api_url
}

# count keeps these six out of the way of the connection suites: each one
# manages a real connection and shares this root module with provider.tftest.hcl,
# so an apply triggered by any run — in any file — creates every unconditional
# resource here regardless of which file asked for it. A connection suite's API
# key is scoped only to its own resource type, so an ungated usage group set
# 403s on "Insufficient scope"; even a fully-scoped key would then hit
# test_team's hardcoded team_id, which exists in whatever org this was
# originally built against but not in a fresh one. Neither is a real dependency
# these suites have, so enable_usage_group_tests just turns them off instead of
# working around it.

# Usage group set with SELECT organization scope
resource "select_usage_group_set" "test_org" {
  count = var.enable_usage_group_tests ? 1 : 0

  name  = var.usage_group_set_name
  order = var.usage_group_set_order
}

# Usage group set with team scope
resource "select_usage_group_set" "test_team" {
  count = var.enable_usage_group_tests ? 1 : 0

  name    = "${var.usage_group_set_name}-team"
  order   = 2
  team_id = var.test_team_id
}

# Usage group set with SELECT organization scope (no scope fields)
resource "select_usage_group_set" "test_select_org" {
  count = var.enable_usage_group_tests ? 1 : 0

  name  = "${var.usage_group_set_name}-select-org"
  order = 3
  # No scope fields = SELECT organization scope
}

# Basic usage group
resource "select_usage_group" "test_basic" {
  count = var.enable_usage_group_tests ? 1 : 0

  name                   = var.usage_group_name
  order                  = var.usage_group_order
  budget                 = var.usage_group_budget
  usage_group_set_id     = select_usage_group_set.test_org[0].id
  filter_expression_json = var.simple_filter_expression_json
}

# Usage group with budget
resource "select_usage_group" "test_with_budget" {
  count = var.enable_usage_group_tests ? 1 : 0

  name                   = "${var.usage_group_name}-with-budget"
  order                  = var.usage_group_order + 1
  budget                 = 100.0
  usage_group_set_id     = select_usage_group_set.test_org[0].id
  filter_expression_json = var.simple_filter_expression_json
}

# Usage group with complex filter
resource "select_usage_group" "test_complex_filter" {
  count = var.enable_usage_group_tests ? 1 : 0

  name                   = "${var.usage_group_name}-complex"
  order                  = var.usage_group_order + 2
  budget                 = null
  usage_group_set_id     = select_usage_group_set.test_org[0].id
  filter_expression_json = var.complex_filter_expression_json
}

# Snowflake account. count keeps it out of the way of every other test: with
# enable_snowflake_account_tests unset there is no resource and no API call, so
# provider.tftest.hcl and `terraform validate` run without Snowflake credentials.
resource "select_snowflake_account" "test" {
  count = var.enable_snowflake_account_tests ? 1 : 0

  id   = var.snowflake_account_id
  name = "${var.snowflake_account_name}${var.snowflake_account_name_suffix}"

  credentials = {
    authentication_method = "key_pair"
    username              = var.snowflake_username
    private_key           = var.snowflake_private_key
  }

  role                            = var.snowflake_role
  warehouse                       = var.snowflake_warehouse
  export_storage_integration_name = var.snowflake_export_storage_integration_name
  sync_enabled                    = var.snowflake_sync_enabled
}

# Databricks connection. count keeps it out of the way of every other test: with
# enable_databricks_connection_tests unset there is no resource and no API call,
# so provider.tftest.hcl and `terraform validate` run without Databricks
# credentials.
resource "select_databricks_connection" "test" {
  count = var.enable_databricks_connection_tests ? 1 : 0

  name = "${var.databricks_connection_name}${var.databricks_connection_name_suffix}"

  databricks_account_id = var.databricks_account_id
  primary_workspace_url = var.databricks_workspace_url
  warehouse_id          = var.databricks_warehouse_id

  credentials = {
    client_id     = var.databricks_client_id
    client_secret = var.databricks_client_secret
  }

  sync_enabled = var.databricks_sync_enabled
}

# BigQuery connection. count keeps it out of the way of every other test: with
# enable_bigquery_connection_tests unset there is no resource and no API call,
# so provider.tftest.hcl and `terraform validate` run without GCP credentials.
resource "select_bigquery_connection" "test" {
  count = var.enable_bigquery_connection_tests ? 1 : 0

  name = "${var.bigquery_connection_name}${var.bigquery_connection_name_suffix}"

  gcp_project_id      = var.bigquery_gcp_project_id
  bigquery_dataset_id = var.bigquery_dataset_id
  billing_account_id  = var.bigquery_billing_account_id
  service_account     = var.bigquery_service_account

  sync_enabled = var.bigquery_sync_enabled
}

# AWS connection. count keeps it out of the way of every other test: with
# enable_aws_connection_tests unset there is no resource and no API call, so
# provider.tftest.hcl and `terraform validate` run without AWS credentials.
resource "select_aws_connection" "test" {
  count = var.enable_aws_connection_tests ? 1 : 0

  name = "${var.aws_connection_name}${var.aws_connection_name_suffix}"

  payer_account_id = var.aws_payer_account_id
  s3_bucket        = var.aws_s3_bucket
  s3_prefix        = var.aws_s3_prefix
  region           = var.aws_region

  credentials = {
    access_key_id     = var.aws_access_key_id
    secret_access_key = var.aws_secret_access_key
  }

  sync_enabled = var.aws_sync_enabled
}

# Outputs for verification. one() rather than [0]: these are unconditional, so
# they're still evaluated — and would error on an out-of-range index — when a
# connection suite runs with enable_usage_group_tests left at its default false.
output "usage_group_set_id" {
  value = one(select_usage_group_set.test_org[*].id)
}

output "usage_group_set_name" {
  value = one(select_usage_group_set.test_org[*].name)
}

output "basic_usage_group_id" {
  value = one(select_usage_group.test_basic[*].id)
}

output "basic_usage_group_name" {
  value = one(select_usage_group.test_basic[*].name)
}

output "usage_group_with_budget_id" {
  value = one(select_usage_group.test_with_budget[*].id)
}

output "usage_group_complex_filter_id" {
  value = one(select_usage_group.test_complex_filter[*].id)
}

output "team_usage_group_set_id" {
  value = one(select_usage_group_set.test_team[*].id)
}

output "select_org_usage_group_set_id" {
  value = one(select_usage_group_set.test_select_org[*].id)
}

# The ids SELECT assigned, so a later run block can assert an update kept the
# same resource. one() returns null rather than failing when the corresponding
# enable_* variable is off and the resource has no instances. Snowflake needs no
# equivalent: its id is the account identifier the configuration supplies, so the
# test compares against that directly.
output "databricks_connection_id" {
  value = one(select_databricks_connection.test[*].id)
}

output "bigquery_connection_id" {
  value = one(select_bigquery_connection.test[*].id)
}

output "aws_connection_id" {
  value = one(select_aws_connection.test[*].id)
}
