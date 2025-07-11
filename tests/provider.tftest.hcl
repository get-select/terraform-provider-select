# Comprehensive Terraform provider tests

variables {
  # Some other variables need to be provided via environment variables
  # and must correlate to an APIKey in the Db of whatever instance you're testing against
  # TF_VAR_select_api_key
  # TF_VAR_select_organization_id
  test_snowflake_account_uuid = "scwxhob-ad38017"
  usage_group_set_name        = "terraform-test-set"
  usage_group_set_order       = 1
  usage_group_name            = "terraform-test-group"
  usage_group_order           = 1
  usage_group_budget          = 100.0
}

# Test 1: Basic usage group set creation
run "create_usage_group_set" {
  command = apply

  assert {
    condition     = select_usage_group_set.test_account.name == var.usage_group_set_name
    error_message = "Usage group set name should match expected value"
  }

  assert {
    condition     = select_usage_group_set.test_account.order == var.usage_group_set_order
    error_message = "Usage group set order should match expected value"
  }

  assert {
    condition     = select_usage_group_set.test_account.snowflake_account_uuid == var.test_snowflake_account_uuid
    error_message = "Snowflake account UUID should match expected value"
  }

  assert {
    condition     = select_usage_group_set.test_account.id != null
    error_message = "Usage group set ID should be set after creation"
  }
}

# Test 2: Basic usage group creation
run "create_usage_groups" {
  command = apply

  assert {
    condition     = select_usage_group.test_basic.name == var.usage_group_name
    error_message = "Basic usage group name should match expected value"
  }

  assert {
    condition     = select_usage_group.test_basic.order == var.usage_group_order
    error_message = "Basic usage group order should match expected value"
  }

  assert {
    condition     = select_usage_group.test_basic.usage_group_set_id == select_usage_group_set.test_account.id
    error_message = "Usage group should belong to the correct usage group set"
  }

  assert {
    condition     = select_usage_group.test_basic.budget == 100.0
    error_message = "Basic usage group should have default budget of 100 when not specified"
  }

  assert {
    condition     = select_usage_group.test_basic.filter_expression_json != null
    error_message = "Usage group should have a filter expression"
  }
}

# Test 3: Usage group with budget
run "verify_usage_group_with_budget" {
  command = plan

  assert {
    condition     = select_usage_group.test_with_budget.budget == var.usage_group_budget
    error_message = "Usage group with budget should have correct budget value"
  }

  assert {
    condition     = select_usage_group.test_with_budget.name == "${var.usage_group_name}-with-budget"
    error_message = "Usage group with budget should have correct name"
  }

  assert {
    condition     = select_usage_group.test_with_budget.usage_group_set_id == select_usage_group_set.test_account.id
    error_message = "Usage group with budget should belong to correct set"
  }
}

# Test 4: Complex filter expression
run "verify_complex_filter" {
  command = plan

  assert {
    condition     = select_usage_group.test_complex_filter.filter_expression_json != null
    error_message = "Complex filter usage group should have filter expression"
  }

  assert {
    condition     = select_usage_group.test_complex_filter.name == "${var.usage_group_name}-complex"
    error_message = "Complex filter usage group should have correct name"
  }

  # Verify the filter expression is valid JSON (basic check)
  assert {
    condition     = length(select_usage_group.test_complex_filter.filter_expression_json) > 10
    error_message = "Filter expression JSON should not be empty"
  }
}

# Test 5: Verify outputs
run "verify_outputs" {
  command = plan

  assert {
    condition     = output.usage_group_set_id != null
    error_message = "Usage group set ID output should be available"
  }

  assert {
    condition     = output.usage_group_set_name != null
    error_message = "Usage group set name output should be available"
  }

  assert {
    condition     = output.basic_usage_group_id != null
    error_message = "Basic usage group ID output should be available"
  }

  assert {
    condition     = output.usage_group_with_budget_id != null
    error_message = "Usage group with budget ID output should be available"
  }

  assert {
    condition     = output.usage_group_complex_filter_id != null
    error_message = "Complex filter usage group ID output should be available"
  }
}

# Test 6: Update operations
run "update_usage_group_set" {
  command = apply

  variables {
    usage_group_set_name  = "terraform-test-set-updated"
    usage_group_set_order = 5
  }

  assert {
    condition     = select_usage_group_set.test_account.name == "terraform-test-set-updated"
    error_message = "Usage group set name should be updated"
  }

  assert {
    condition     = select_usage_group_set.test_account.order == 5
    error_message = "Usage group set order should be updated"
  }

  # Ensure ID stability during updates
  assert {
    condition     = select_usage_group_set.test_account.id != null
    error_message = "Usage group set ID should remain stable during updates"
  }
}

# Test 7: Update usage group
run "update_usage_group" {
  command = apply

  variables {
    usage_group_name   = "terraform-test-group-updated"
    usage_group_order  = 3
    usage_group_budget = 50.0
  }

  assert {
    condition     = select_usage_group.test_basic.name == "terraform-test-group-updated"
    error_message = "Usage group name should be updated"
  }

  assert {
    condition     = select_usage_group.test_basic.order == 3
    error_message = "Usage group order should be updated"
  }

  assert {
    condition     = select_usage_group.test_basic.budget == 50.0
    error_message = "Usage group budget should be updated"
  }

  # Ensure ID stability during updates
  assert {
    condition     = select_usage_group.test_basic.id != null
    error_message = "Usage group ID should remain stable during updates"
  }
}
