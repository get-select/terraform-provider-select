# Project to tag mapping - minimal data structure
locals {
  project_tags = {
    "Project A" = "project_a"
    "Project B" = "project_b"
  }
}

data "select_usage_group_set" "team_tags" {
  usage_groups = [
    for project_name, tag_name in local.project_tags : {
      name: project_name,
      filter_expression: {
        operator: "and",
        filters: [
          {
            field: "warehouse_tags",
            operator: "in",
            values: [tag_name]
          }
        ]
      }
    }
  ]
}


# Usage Group Set for Production Workloads
resource "select_usage_group_set" "production_workloads" {
  name                    = "Production Workload Management"
  snowflake_account_uuid = "12345678-1234-1234-1234-123456789012"
  organization_name      = "my-snowflake-org"
}

# Complex usage group - ETL processes with multiple conditions
resource "select_usage_group" "etl_processes" {
  usage_group_set_id     = select_usage_group_set.production_workloads.id
  name                   = each.value.team_name
  snowflake_account_uuid = select_usage_group_set.production_workloads.snowflake_account_uuid
  order = each.index
  filter_expression = each.value.filter_expression
  key = each.value.team_name
}
