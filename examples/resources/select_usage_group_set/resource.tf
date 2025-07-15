resource "select_usage_group_set" "production" {
  name                   = "Production Workloads"
  order                  = 1
  snowflake_account_uuid = "12345678-1234-1234-1234-123456789012"
} 