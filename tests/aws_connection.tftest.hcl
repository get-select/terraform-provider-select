# SPDX-License-Identifier: MPL-2.0

# AWS connection tests.
#
# These are separate from provider.tftest.hcl because creating a connection makes
# SELECT read the Cost and Usage Report out of S3 for real: the bucket, prefix
# and IAM user have to work, and the connection is added to and removed from the
# target organization. Run with `make test-aws` once the TF_VAR_aws_* variables
# are set, or let CI run it from the credentials it holds as secrets.
#
# The payload-shaping rules — which fields a patch omits, and that clearing
# s3_prefix is the one explicit null the API accepts — are settled before
# anything reaches the API and are covered by the Go tests in internal/, so they
# are not repeated here. What only a live run can show is that the connection
# round-trips: that the values SELECT records land in state, that a rename is an
# update rather than a replacement, and that removing the connection reaches the
# API rather than only leaving state.

variables {
  enable_aws_connection_tests = true
  # This suite shares tests/main.tf's root module with provider.tftest.hcl, so
  # every apply here would otherwise also create the usage group set/group
  # resources that file needs — resources this suite's API key isn't scoped for
  # and has nothing to do with anyway.
  enable_usage_group_tests = false
}

# The connection is added with the S3 location SELECT validated, and with the
# ETag every subsequent write depends on.
run "create_aws_connection" {
  command = apply

  assert {
    condition     = select_aws_connection.test[0].name == var.aws_connection_name
    error_message = "Connection name should match expected value"
  }

  assert {
    condition     = select_aws_connection.test[0].id != ""
    error_message = "SELECT should assign an ID when the connection is added"
  }

  assert {
    condition     = select_aws_connection.test[0].etag != ""
    error_message = "ETag should be set after creation; updates and deletes require it"
  }

  assert {
    condition     = select_aws_connection.test[0].payer_account_id == var.aws_payer_account_id
    error_message = "The configured payer account should be recorded"
  }

  assert {
    condition     = select_aws_connection.test[0].s3_bucket == var.aws_s3_bucket
    error_message = "The configured bucket should be recorded"
  }

  assert {
    condition     = select_aws_connection.test[0].create_time != ""
    error_message = "Create time should be set after creation"
  }

  # The schema declares the API's own default, so an unset value must resolve at
  # plan time rather than staying unknown until after apply.
  assert {
    condition     = select_aws_connection.test[0].sync_enabled == true
    error_message = "sync_enabled should default to true"
  }
}

# A rename is an in-place update that makes no call to S3, since name is not one
# of the access-related fields that triggers revalidation.
run "rename_aws_connection" {
  command = apply

  variables {
    aws_connection_name_suffix = "-renamed"
  }

  assert {
    condition     = select_aws_connection.test[0].name == "${var.aws_connection_name}-renamed"
    error_message = "The connection should have been renamed in place"
  }

  assert {
    condition     = select_aws_connection.test[0].id == run.create_aws_connection.aws_connection_id
    error_message = "A rename should not replace the connection"
  }
}

# Clearing s3_prefix is the one removal this API accepts, and it moves SELECT to
# reading the root of the bucket. It revalidates against S3, so the report has to
# be readable from the root for this to pass.
run "clear_s3_prefix" {
  command = apply

  variables {
    aws_connection_name_suffix = "-renamed"
    aws_s3_prefix              = null
  }

  assert {
    condition     = select_aws_connection.test[0].s3_prefix == null
    error_message = "Removing s3_prefix from the configuration should clear it"
  }
}

# Toggling sync touches only the parent connection record, so it too is an
# in-place update that makes no call to S3.
run "disable_sync" {
  command = apply

  variables {
    aws_connection_name_suffix = "-renamed"
    aws_s3_prefix              = null
    aws_sync_enabled           = false
  }

  assert {
    condition     = select_aws_connection.test[0].sync_enabled == false
    error_message = "sync_enabled should follow the configuration"
  }
}

# Taking the connection out of the configuration destroys it. Terraform tears
# down whatever is left at the end of the file either way, but that teardown
# asserts nothing and swallows what it cannot remove; doing it as a run block
# means a delete the API refuses fails the test.
run "delete_aws_connection" {
  command = apply

  variables {
    aws_connection_name_suffix  = "-renamed"
    aws_s3_prefix               = null
    aws_sync_enabled            = false
    enable_aws_connection_tests = false
  }

  assert {
    condition     = length(select_aws_connection.test) == 0
    error_message = "The connection should have been destroyed"
  }
}
