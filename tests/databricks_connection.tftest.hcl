# SPDX-License-Identifier: MPL-2.0

# Databricks connection tests.
#
# These are separate from provider.tftest.hcl because creating a connection makes
# SELECT validate the configuration against Databricks for real: the service
# principal has to work, and the connection is added to and removed from the
# target organization. Run with `make test-databricks` once the
# TF_VAR_databricks_* variables are set, or let CI run it from the credentials it
# holds as secrets.
#
# The workspace URL rule and the payload-shaping rules are checked before
# anything reaches the API and are covered by the Go tests in internal/, so they
# are not repeated here. What only a live run can show is that the connection
# round-trips: that the values SELECT discovers land in state, that an update is
# an update rather than a replacement, and that removing the connection reaches
# the API rather than only leaving state.

variables {
  enable_databricks_connection_tests = true
  # This suite shares tests/main.tf's root module with provider.tftest.hcl, so
  # every apply here would otherwise also create the usage group set/group
  # resources that file needs — resources this suite's API key isn't scoped for
  # and has nothing to do with anyway.
  enable_usage_group_tests = false
}

# The connection is added with everything SELECT resolved from Databricks, and
# with the ETag every subsequent write depends on.
run "create_databricks_connection" {
  command = apply

  assert {
    condition     = select_databricks_connection.test[0].name == var.databricks_connection_name
    error_message = "Connection name should match expected value"
  }

  assert {
    condition     = select_databricks_connection.test[0].id != ""
    error_message = "SELECT should assign an ID when the connection is added"
  }

  assert {
    condition     = select_databricks_connection.test[0].etag != ""
    error_message = "ETag should be set after creation; updates and deletes require it"
  }

  # Create only succeeds once the metastore check passes, so both of these are
  # resolved by the time the resource exists.
  assert {
    condition     = select_databricks_connection.test[0].metastore != null
    error_message = "Metastore should be resolved by the validation that gates creation"
  }

  assert {
    condition     = length(select_databricks_connection.test[0].workspace_ids) > 0
    error_message = "Workspace discovery should have found at least one workspace"
  }

  assert {
    condition     = select_databricks_connection.test[0].connection_id != ""
    error_message = "The parent connection ID should be set"
  }

  assert {
    condition     = select_databricks_connection.test[0].create_time != ""
    error_message = "Create time should be set after creation"
  }

  # The schema declares the API's own default, so an unset value must resolve at
  # plan time rather than staying unknown until after apply.
  assert {
    condition     = select_databricks_connection.test[0].query_sanitization_enabled == false
    error_message = "query_sanitization_enabled should default to false"
  }
}

# A rename is an in-place update, and it moves the ETag because the name lives on
# the underlying connection record.
run "rename_databricks_connection" {
  command = apply

  variables {
    databricks_connection_name_suffix = "-renamed"
  }

  assert {
    condition     = select_databricks_connection.test[0].name == "${var.databricks_connection_name}-renamed"
    error_message = "The connection should have been renamed in place"
  }

  assert {
    condition     = select_databricks_connection.test[0].id == run.create_databricks_connection.databricks_connection_id
    error_message = "A rename should not replace the connection"
  }
}

# Toggling sync touches only the parent connection record, so it too is an
# in-place update that makes no call to Databricks.
run "disable_sync" {
  command = apply

  variables {
    databricks_connection_name_suffix = "-renamed"
    databricks_sync_enabled           = false
  }

  assert {
    condition     = select_databricks_connection.test[0].sync_enabled == false
    error_message = "sync_enabled should follow the configuration"
  }
}

# Taking the connection out of the configuration destroys it. Terraform tears
# down whatever is left at the end of the file either way, but that teardown
# asserts nothing and swallows what it cannot remove; doing it as a run block
# means a delete the API refuses fails the test.
run "delete_databricks_connection" {
  command = apply

  variables {
    databricks_connection_name_suffix  = "-renamed"
    databricks_sync_enabled            = false
    enable_databricks_connection_tests = false
  }

  assert {
    condition     = length(select_databricks_connection.test) == 0
    error_message = "The connection should have been destroyed"
  }
}
