# SPDX-License-Identifier: MPL-2.0

# Budget tests.
#
# These are separate from provider.tftest.hcl for the same reason the
# connection suites are: they share tests/main.tf's root module but have
# nothing to do with usage groups. Unlike those four suites, creating a budget
# makes no call to an external system — SELECT stores the definition directly
# — so this one needs nothing beyond the same API key and organization every
# other test already uses, and it joins the provider's e2e CI matrix rather
# than needing its own credentials. Run with `make test-budget`.

variables {
  enable_budget_tests = true
  # This suite shares tests/main.tf's root module with provider.tftest.hcl, so
  # every apply here would otherwise also create the usage group set/group
  # resources that file needs — resources this suite's API key isn't scoped
  # for and has nothing to do with anyway.
  enable_usage_group_tests = false
}

# The budget is created with the period SELECT stored and the current period it
# derived from it, and with the ETag every subsequent write depends on.
run "create_budget" {
  command = apply

  assert {
    condition     = select_budget.test[0].name == var.budget_name
    error_message = "Budget name should match expected value"
  }

  assert {
    condition     = select_budget.test[0].id != ""
    error_message = "SELECT should assign an ID when the budget is created"
  }

  assert {
    condition     = select_budget.test[0].etag != ""
    error_message = "ETag should be set after creation; updates and deletes require it"
  }

  assert {
    condition     = select_budget.test[0].amount == 1000
    error_message = "The configured amount should be recorded"
  }

  assert {
    condition     = select_budget.test[0].period.schedule_type == "monthly"
    error_message = "The configured period should be recorded"
  }

  assert {
    condition     = select_budget.test[0].current_period_start != ""
    error_message = "current_period_start should be derived from period on read"
  }

  assert {
    condition     = select_budget.test[0].create_time != ""
    error_message = "Create time should be set after creation"
  }
}

# A rename is an in-place update: id stays the same, and update_time moves off
# create_time even though nothing about the period or amount changed.
run "rename_budget" {
  command = apply

  variables {
    budget_name_suffix = "-renamed"
  }

  assert {
    condition     = select_budget.test[0].name == "${var.budget_name}-renamed"
    error_message = "The budget should have been renamed in place"
  }

  assert {
    condition     = select_budget.test[0].id == run.create_budget.budget_id
    error_message = "A rename should not replace the budget"
  }

  assert {
    condition     = select_budget.test[0].update_time != select_budget.test[0].create_time
    error_message = "A rename should move update_time off create_time"
  }
}

# Taking the budget out of the configuration deletes it. Terraform tears down
# whatever is left at the end of the file either way, but that teardown
# asserts nothing and swallows what it cannot remove; doing it as a run block
# means a delete the API refuses fails the test.
run "delete_budget" {
  command = apply

  variables {
    budget_name_suffix  = "-renamed"
    enable_budget_tests = false
  }

  assert {
    condition     = length(select_budget.test) == 0
    error_message = "The budget should have been destroyed"
  }
}
