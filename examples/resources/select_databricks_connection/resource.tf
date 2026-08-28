# SELECT reads a Databricks account's usage through one workspace per region.
# The service principal is an OAuth machine-to-machine identity with access to
# the workspace's SQL warehouse and read access to the account's system tables.
resource "select_databricks_connection" "production_us_east" {
  name = "Acme Production (us-east-1)"

  databricks_account_id = "11111111-2222-3333-4444-555555555555"
  primary_workspace_url = "https://acme-prod.cloud.databricks.com"
  warehouse_id          = "abc123def4567890"

  credentials = {
    client_id     = "88888888-9999-aaaa-bbbb-cccccccccccc"
    client_secret = var.databricks_client_secret
  }
}

# One account can have several connections, one per region, each reading through
# a workspace in its own region. SELECT allows one connection per account per
# region, so this is how an account spanning regions is covered.
resource "select_databricks_connection" "production_eu_west" {
  name = "Acme Production (eu-west-1)"

  databricks_account_id = "11111111-2222-3333-4444-555555555555"
  primary_workspace_url = "https://acme-prod-eu.cloud.databricks.com"
  warehouse_id          = "fedcba0987654321"

  credentials = {
    client_id     = "88888888-9999-aaaa-bbbb-cccccccccccc"
    client_secret = var.databricks_client_secret
  }

  # Sanitize query text before SELECT stores it, and hold off syncing until the
  # connection has been reviewed.
  query_sanitization_enabled = true
  sync_enabled               = false
}
