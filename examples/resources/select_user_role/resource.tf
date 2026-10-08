# The user does not have to have signed in yet. The grant takes effect when
# they first sign in through your identity provider.
resource "select_user_role" "alice_admin" {
  email = "alice@example.com"
  role  = "admin"
}

# One role on one Snowflake account.
resource "select_user_role" "bob_account_editor" {
  email = "bob@example.com"
  role  = "editor"
  scope = {
    type = "snowflake_account"
    id   = "2c3f5d9e-8a1b-4c6d-9e0f-1a2b3c4d5e6f"
  }
}
