# SPDX-License-Identifier: MPL-2.0

# BigQuery connection tests.
#
# These are separate from provider.tftest.hcl because creating a connection makes
# SELECT validate the configuration against BigQuery for real: the GCP project
# and service account have to work, and the connection is added to and removed
# from the target organization. Run with `make test-bigquery` once the
# TF_VAR_bigquery_* variables are set.
#
# The is_doit field-combination rule and the payload-shaping rules are checked
# before anything reaches the API and are covered by the Go tests in internal/,
# so they are not repeated here. What only a live run can show is that the
# connection round-trips: that the values SELECT discovers land in state, and
# that a rename is an update rather than a replacement.
#
# Commented out for now: wiring this into CI needs a dedicated GCP project and a
# service account stored as CI secrets, which is being done as a separate
# follow-up. `make test-bigquery` remains available for a manual local run
# against your own test project in the meantime.

/*
variables {
  enable_bigquery_connection_tests = true
}

# The connection is added with the project SELECT validated, and with the ETag
# every subsequent write depends on.
run "create_bigquery_connection" {
  command = apply

  assert {
    condition     = select_bigquery_connection.test[0].name == var.bigquery_connection_name
    error_message = "Connection name should match expected value"
  }

  assert {
    condition     = select_bigquery_connection.test[0].id != ""
    error_message = "SELECT should assign an ID when the connection is added"
  }

  assert {
    condition     = select_bigquery_connection.test[0].etag != ""
    error_message = "ETag should be set after creation; updates and deletes require it"
  }

  assert {
    condition     = select_bigquery_connection.test[0].gcp_project_id == var.bigquery_gcp_project_id
    error_message = "The configured GCP project should be recorded"
  }

  assert {
    condition     = select_bigquery_connection.test[0].create_time != ""
    error_message = "Create time should be set after creation"
  }

  # The schema declares the API's own defaults, so an unset value must resolve at
  # plan time rather than staying unknown until after apply.
  assert {
    condition     = select_bigquery_connection.test[0].is_doit == false
    error_message = "is_doit should default to false"
  }

  assert {
    condition     = select_bigquery_connection.test[0].query_sanitization_enabled == false
    error_message = "query_sanitization_enabled should default to false"
  }
}

# A rename is an in-place update that makes no call to BigQuery, since name is
# not one of the access-related fields that triggers revalidation.
run "rename_bigquery_connection" {
  command = apply

  variables {
    bigquery_connection_name = "terraform-test-bigquery-connection-renamed"
  }

  assert {
    condition     = select_bigquery_connection.test[0].name == "terraform-test-bigquery-connection-renamed"
    error_message = "The connection should have been renamed in place"
  }

  assert {
    condition     = select_bigquery_connection.test[0].id != ""
    error_message = "A rename should not replace the connection"
  }
}

# Toggling sync touches only the parent connection record, so it too is an
# in-place update that makes no call to BigQuery.
run "disable_sync" {
  command = apply

  variables {
    bigquery_connection_name = "terraform-test-bigquery-connection-renamed"
    bigquery_sync_enabled    = false
  }

  assert {
    condition     = select_bigquery_connection.test[0].sync_enabled == false
    error_message = "sync_enabled should follow the configuration"
  }
}
*/
