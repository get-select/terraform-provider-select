# SPDX-License-Identifier: MPL-2.0

# Snowflake account tests.
#
# These are separate from provider.tftest.hcl because creating an account makes
# SELECT validate the configuration against Snowflake for real: the credentials
# have to work, and the account is added to and removed from the target
# organization. Run with `make test-snowflake` once the TF_VAR_snowflake_*
# variables are set, or let CI run it from the credentials it holds as secrets.
#
# The credential and field-combination rules are checked before anything reaches
# the API and are covered by the Go tests in internal/, so they are not repeated
# here. What only a live run can show is that the account round-trips: that the
# API's own values land in state, that an update is an update rather than a
# replacement, and that removing the account reaches the API rather than only
# leaving state.

variables {
  enable_snowflake_account_tests = true
  # This suite shares tests/main.tf's root module with provider.tftest.hcl, so
  # every apply here would otherwise also create the usage group set/group
  # resources that file needs — resources this suite's API key isn't scoped for
  # and has nothing to do with anyway.
  enable_usage_group_tests = false
}

# The account is added with the identity SELECT resolved from Snowflake, and with
# the ETag every subsequent write depends on.
run "create_snowflake_account" {
  command = apply

  assert {
    condition     = select_snowflake_account.test[0].id == var.snowflake_account_id
    error_message = "Snowflake account ID should match the configured identifier"
  }

  assert {
    condition     = select_snowflake_account.test[0].name == var.snowflake_account_name
    error_message = "Snowflake account name should match expected value"
  }

  assert {
    condition     = select_snowflake_account.test[0].etag != ""
    error_message = "ETag should be set after creation; updates and deletes require it"
  }

  assert {
    condition     = select_snowflake_account.test[0].snowflake_organization_name != ""
    error_message = "Snowflake organization name should be resolved from the account"
  }

  assert {
    condition     = select_snowflake_account.test[0].create_time != ""
    error_message = "Create time should be set after creation"
  }

  # The schema declares the API's own defaults, so an unset value must resolve at
  # plan time rather than staying unknown until after apply.
  assert {
    condition     = select_snowflake_account.test[0].query_sanitization_enabled == false
    error_message = "query_sanitization_enabled should default to false"
  }

  # Never returned by the API, so state can only hold what the configuration says.
  assert {
    condition     = select_snowflake_account.test[0].credentials.username == var.snowflake_username
    error_message = "Configured credentials should be preserved in state"
  }
}

# A rename and a field the API can clear both go through one merge patch, and the
# ETag moves with them.
run "update_snowflake_account" {
  command = apply

  variables {
    snowflake_account_name_suffix = "-renamed"
  }

  assert {
    condition     = select_snowflake_account.test[0].name == "${var.snowflake_account_name}-renamed"
    error_message = "Snowflake account name should reflect the update"
  }

  assert {
    condition     = select_snowflake_account.test[0].id == var.snowflake_account_id
    error_message = "Updating fields must not replace the account"
  }

  assert {
    condition     = select_snowflake_account.test[0].update_time != select_snowflake_account.test[0].create_time
    error_message = "Update time should move past create time after an update"
  }
}

# sync_enabled is one of the fields the API refuses to clear, so it has to be sent
# as a value on every patch rather than omitted.
run "disable_sync" {
  command = apply

  variables {
    snowflake_account_name_suffix = "-renamed"
    snowflake_sync_enabled        = false
  }

  assert {
    condition     = select_snowflake_account.test[0].sync_enabled == false
    error_message = "sync_enabled should reflect the update"
  }
}

# Taking the account out of the configuration removes it from the organization.
# Terraform tears down whatever is left at the end of the file either way, but
# that teardown asserts nothing and swallows what it cannot remove; doing it as a
# run block means a delete the API refuses fails the test. It matters more here
# than elsewhere: an account left attached blocks the next run, since the same
# identifier cannot be added to the organization twice.
run "delete_snowflake_account" {
  command = apply

  variables {
    snowflake_account_name_suffix  = "-renamed"
    snowflake_sync_enabled         = false
    enable_snowflake_account_tests = false
  }

  assert {
    condition     = length(select_snowflake_account.test) == 0
    error_message = "The account should have been removed from the organization"
  }
}
