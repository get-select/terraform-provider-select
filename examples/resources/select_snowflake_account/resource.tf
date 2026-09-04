# Key pair authentication, which is what most automated setups use: Snowflake
# rejects password authentication for service users on accounts that enforce MFA.
resource "select_snowflake_account" "production" {
  id   = "acme-us-east-1"
  name = "Acme Production"

  credentials = {
    authentication_method = "key_pair"
    username              = "SELECT_USER"
    private_key           = file("${path.module}/select_user.p8")
  }

  role      = "SELECT_ROLE"
  warehouse = "SELECT_WH"

  # Exactly one export destination is required.
  export_storage_integration_name = "SELECT_STORAGE_INTEGRATION"
}

# Workload identity federation needs no secret at all: Snowflake resolves the
# user from its own workload identity mapping.
resource "select_snowflake_account" "analytics" {
  id   = "acme-analytics"
  name = "Acme Analytics"

  credentials = {
    authentication_method = "workload_identity_federation"
  }

  role              = "SELECT_ROLE"
  warehouse         = "SELECT_WH"
  export_stage_name = "SELECT_DB.PUBLIC.SELECT_EXPORT_STAGE"
  sync_enabled      = true

  # Attribute activity from these users to Fivetran rather than to the users
  # themselves.
  fivetran_users = ["FIVETRAN_USER"]
}
