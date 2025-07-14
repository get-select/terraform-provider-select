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

variable "test_snowflake_account_uuid" {
  description = "Test Snowflake account UUID"
  type        = string
  default     = "12345678-1234-1234-1234-123456789012"
}

variable "test_snowflake_org_name" {
  description = "Test Snowflake organization name"
  type        = string
  default     = "test-snowflake-org"
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

# Provider configuration
provider "select" {
  api_key         = var.select_api_key
  organization_id = var.select_organization_id
  # You could move this to a variable if you wanted to run tests against staging or something
  select_api_url = "http://localhost:8000"
}

# Usage group set with Snowflake account
resource "select_usage_group_set" "test_account" {
  name                   = var.usage_group_set_name
  order                  = var.usage_group_set_order
  snowflake_account_uuid = var.test_snowflake_account_uuid
}

# Basic usage group
resource "select_usage_group" "test_basic" {
  name                   = var.usage_group_name
  order                  = var.usage_group_order
  budget                 = var.usage_group_budget
  usage_group_set_id     = select_usage_group_set.test_account.id
  filter_expression_json = var.simple_filter_expression_json
}

# Usage group with budget
resource "select_usage_group" "test_with_budget" {
  name                   = "${var.usage_group_name}-with-budget"
  order                  = var.usage_group_order + 1
  budget                 = 100.0
  usage_group_set_id     = select_usage_group_set.test_account.id
  filter_expression_json = var.simple_filter_expression_json
}

# Usage group with complex filter
resource "select_usage_group" "test_complex_filter" {
  name                   = "${var.usage_group_name}-complex"
  order                  = var.usage_group_order + 2
  budget                 = null
  usage_group_set_id     = select_usage_group_set.test_account.id
  filter_expression_json = var.complex_filter_expression_json
}

# Outputs for verification
output "usage_group_set_id" {
  value = select_usage_group_set.test_account.id
}

output "usage_group_set_name" {
  value = select_usage_group_set.test_account.name
}

output "basic_usage_group_id" {
  value = select_usage_group.test_basic.id
}

output "basic_usage_group_name" {
  value = select_usage_group.test_basic.name
}

output "usage_group_with_budget_id" {
  value = select_usage_group.test_with_budget.id
}

output "usage_group_complex_filter_id" {
  value = select_usage_group.test_complex_filter.id
}
