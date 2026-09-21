# SPDX-License-Identifier: MPL-2.0

# Comprehensive Terraform provider tests

variables {
  # Some other variables need to be provided via environment variables
  # and must correlate to an APIKey in the Db of whatever instance you're testing against
  # TF_VAR_select_api_key
  # TF_VAR_select_organization_id
  test_team_id          = "2f0899e2-2746-4300-887c-524e64b5a138"
  usage_group_set_name  = "terraform-test-set"
  usage_group_set_order = 1
  usage_group_name      = "terraform-test-group"
  usage_group_order     = 1
  usage_group_budget    = 100.0
}

# Test 1: Basic usage group set creation
run "create_usage_group_set" {
  command = apply

  assert {
    condition     = select_usage_group_set.test_org[0].name == var.usage_group_set_name
    error_message = "Usage group set name should match expected value"
  }

  assert {
    condition     = select_usage_group_set.test_org[0].order == var.usage_group_set_order
    error_message = "Usage group set order should match expected value"
  }

  assert {
    condition     = select_usage_group_set.test_org[0].id != null
    error_message = "Usage group set ID should be set after creation"
  }
}

# Test 1b: Team-scoped usage group set creation
run "create_team_scoped_usage_group_set" {
  command = apply

  assert {
    condition     = select_usage_group_set.test_team[0].name == "${var.usage_group_set_name}-team"
    error_message = "Team-scoped usage group set name should match expected value"
  }

  assert {
    condition     = select_usage_group_set.test_team[0].team_id == var.test_team_id
    error_message = "Team ID should match expected value"
  }

  assert {
    condition     = select_usage_group_set.test_team[0].id != null
    error_message = "Team-scoped usage group set ID should be set after creation"
  }
}

# Test 1c: SELECT organization-scoped usage group set creation
run "create_select_org_scoped_usage_group_set" {
  command = apply

  assert {
    condition     = select_usage_group_set.test_select_org[0].name == "${var.usage_group_set_name}-select-org"
    error_message = "SELECT org-scoped usage group set name should match expected value"
  }

  assert {
    condition     = select_usage_group_set.test_select_org[0].team_id == null
    error_message = "Team ID should be null for SELECT org-scoped set"
  }

  assert {
    condition     = select_usage_group_set.test_select_org[0].id != null
    error_message = "SELECT org-scoped usage group set ID should be set after creation"
  }
}

# Test 2: Basic usage group creation
run "create_usage_groups" {
  command = apply

  assert {
    condition     = select_usage_group.test_basic[0].name == var.usage_group_name
    error_message = "Basic usage group name should match expected value"
  }

  assert {
    condition     = select_usage_group.test_basic[0].order == var.usage_group_order
    error_message = "Basic usage group order should match expected value"
  }

  assert {
    condition     = select_usage_group.test_basic[0].usage_group_set_id == select_usage_group_set.test_org[0].id
    error_message = "Usage group should belong to the correct usage group set"
  }

  assert {
    condition     = select_usage_group.test_basic[0].budget == 100.0
    error_message = "Basic usage group should have default budget of 100 when not specified"
  }

  assert {
    condition     = select_usage_group.test_basic[0].filter_expression_json != null
    error_message = "Usage group should have a filter expression"
  }
}

# Test 3: Usage group with budget
run "verify_usage_group_with_budget" {
  command = plan

  assert {
    condition     = select_usage_group.test_with_budget[0].budget == var.usage_group_budget
    error_message = "Usage group with budget should have correct budget value"
  }

  assert {
    condition     = select_usage_group.test_with_budget[0].name == "${var.usage_group_name}-with-budget"
    error_message = "Usage group with budget should have correct name"
  }

  assert {
    condition     = select_usage_group.test_with_budget[0].usage_group_set_id == select_usage_group_set.test_org[0].id
    error_message = "Usage group with budget should belong to correct set"
  }
}

# Test 4: Complex filter expression
run "verify_complex_filter" {
  command = plan

  assert {
    condition     = select_usage_group.test_complex_filter[0].filter_expression_json != null
    error_message = "Complex filter usage group should have filter expression"
  }

  assert {
    condition     = select_usage_group.test_complex_filter[0].name == "${var.usage_group_name}-complex"
    error_message = "Complex filter usage group should have correct name"
  }

  # Verify the filter expression is valid JSON (basic check)
  assert {
    condition     = length(select_usage_group.test_complex_filter[0].filter_expression_json) > 10
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
    condition     = select_usage_group_set.test_org[0].name == "terraform-test-set-updated"
    error_message = "Usage group set name should be updated"
  }

  assert {
    condition     = select_usage_group_set.test_org[0].order == 5
    error_message = "Usage group set order should be updated"
  }

  # Ensure ID stability during updates
  assert {
    condition     = select_usage_group_set.test_org[0].id != null
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
    condition     = select_usage_group.test_basic[0].name == "terraform-test-group-updated"
    error_message = "Usage group name should be updated"
  }

  assert {
    condition     = select_usage_group.test_basic[0].order == 3
    error_message = "Usage group order should be updated"
  }

  assert {
    condition     = select_usage_group.test_basic[0].budget == 50.0
    error_message = "Usage group budget should be updated"
  }

  # Ensure ID stability during updates
  assert {
    condition     = select_usage_group.test_basic[0].id != null
    error_message = "Usage group ID should remain stable during updates"
  }
}

# Test 8: The attributes v2 added, and the ones it removed.
#
# organization_id is gone: v1 carried the organization in the path, so the
# resource had an attribute for it, and v2 scopes by the x-tenant-id header the
# provider block already supplies. A configuration that still sets it fails to
# validate, which is the breaking part of this migration.
run "v2_attributes_are_populated" {
  command = apply

  # ETags drive optimistic concurrency on every v2 write. Without one in state
  # an update would have nothing to send as If-Match and the API would refuse it
  # with 428.
  assert {
    condition     = select_usage_group_set.test_org[0].etag != null && select_usage_group_set.test_org[0].etag != ""
    error_message = "A usage group set should carry the ETag its updates send as If-Match"
  }

  assert {
    condition     = select_usage_group.test_basic[0].etag != null && select_usage_group.test_basic[0].etag != ""
    error_message = "A usage group should carry the ETag its updates send as If-Match"
  }

  # Recording a version is what keeps the state an apply started from
  # restorable. Every apply that touches a set's groups records exactly one, so
  # by now the count has moved past the 1 a freshly created set has.
  assert {
    condition     = select_usage_group_set.test_org[0].version > 1
    error_message = "Each apply that changes a set's groups should record one version, so the count should have grown"
  }

  assert {
    condition     = select_usage_group_set.test_org[0].public != null
    error_message = "public should be resolved rather than left unknown"
  }

  assert {
    condition     = select_usage_group_set.test_org[0].insights_sync_pending != null
    error_message = "insights_sync_pending should be resolved rather than left unknown"
  }

  assert {
    condition     = select_usage_group.test_basic[0].create_time != null && select_usage_group.test_basic[0].update_time != null
    error_message = "A usage group should carry the timestamps v2 renamed from created_at/updated_at"
  }

  assert {
    condition     = select_usage_group.test_basic[0].usage_group_set_id == select_usage_group_set.test_org[0].id
    error_message = "A usage group should stay attached to the set it was created in"
  }
}

# Test 9: Clearing a budget.
#
# budget is the one field on a usage group the API lets a caller clear, so
# removing it from the configuration has to reach the API as an explicit null.
# Omitting the key would leave the old budget in place and the apply would fail
# on an inconsistent result.
run "clear_usage_group_budget" {
  command = apply

  variables {
    usage_group_name   = "terraform-test-group-updated"
    usage_group_order  = 3
    usage_group_budget = null
  }

  assert {
    condition     = select_usage_group.test_basic[0].budget == null
    error_message = "Removing budget from the configuration should clear it rather than leave the old value"
  }
}
