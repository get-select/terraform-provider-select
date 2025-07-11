# Usage Group Set for Production Workloads
resource "select_usage_group_set" "production_workloads" {
  name                    = "Production Workload Management"
  snowflake_account_uuid = "12345678-1234-1234-1234-123456789012"
  account_uuid           = "87654321-4321-4321-4321-210987654321"
  organization_name      = "my-snowflake-org"
  version_uuid = "12345678-1234-1234-1234-123456789012"
  usage_groups = [
    {
      name = "ETL Processes"
      budget = 50.00
      filter_expression = {
        operator = "and"
      }
    }
  ]
}

# Complex usage group - ETL processes with multiple conditions
resource "select_usage_group" "etl_processes" {
  usage_group_set_id     = select_usage_group_set.production_workloads.id
  budget                 = 50.00
  snowflake_account_uuid = "12345678-1234-1234-1234-123456789012"
  order = 0
  # Complex filter: ETL warehouses AND ETL users AND specific resource types
  filter_expression = {
    operator = "and"
    filters = [
      # Must be ETL warehouse
      {
        field    = "warehouse_name"
        operator = "in"
        values   = ["ETL_WAREHOUSE_1", "ETL_WAREHOUSE_2", "ETL_BATCH_WH"]
      },
      # ... other stuff if needed, can be complex
    ]
  }
}
# Complex usage group - ETL processes with multiple conditions
resource "select_usage_group_set_version" "etl_processes" {
  usage_group_set_id = select_usage_group_set.production_workloads.id
}
# Another complex usage group - Analytics team with budget constraints
resource "select_usage_group" "analytics_team" {
  usage_group_set_id     = select_usage_group_set.production_workloads.id
  name                   = "Analytics Team Usage"
  budget                 = 3000.00
  snowflake_account_uuid = "12345678-1234-1234-1234-123456789012"
  order = select_usage_group.etl_processes.order + 1
  # Complex filter: Analytics users AND analytics databases AND specific roles
  filter_expression = {
    operator = "and"
    filters = [
      # Analytics team roles
      {
        operator = "or"
        filters = [
          {
            field    = "role_name"
            operator = "in"
            values   = ["ANALYST", "DATA_SCIENTIST", "ANALYTICS_VIEWER"]
          },
          {
            field    = "role_name"
            operator = "like"
            value    = "ANALYTICS_%"
          }
        ]
      },
      # Analytics databases only
      {
        field    = "database_schema_name"
        operator = "like"
        value    = "ANALYTICS%"
      },
      # Must have analytics tags
      {
        field    = "database_tags"
        operator = "array_contains"
        values   = ["analytics", "reporting", "dashboard"]
      },
      # Exclude system/admin operations
      {
        field    = "user_name"
        operator = "not in"
        values   = ["SYSTEM", "ADMIN", "SERVICE_ACCOUNT"]
        not      = true
      }
    ]
  }
}
