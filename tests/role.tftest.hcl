# SPDX-License-Identifier: MPL-2.0

# Role grant tests: select_team_role, select_user_role and select_default_role.
#
# These are separate from provider.tftest.hcl for the same reason the team
# suite is: they share tests/main.tf's root module but have nothing to do with
# the usage group suite. A role grant makes no call to an external system, so
# this suite needs nothing beyond the same API key and organization every other
# test already uses, and it joins the provider's e2e CI matrix. Run with
# `make test-role`.

variables {
  enable_role_tests = true
  # This suite shares tests/main.tf's root module with provider.tftest.hcl, so
  # every apply here would otherwise also create the usage group resources
  # that file needs. The suite makes its own usage group instead.
  enable_usage_group_tests = false
}

# Each grant is created, and its scope reads back as configured: the usage
# group for the team and default grants, and null (the organization) for the
# user grant.
run "create_roles" {
  command = apply

  assert {
    condition     = select_team_role.test[0].id != "" && select_team_role.test[0].team_id == select_team.role[0].id
    error_message = "SELECT should assign the team role grant an ID, on the test team"
  }

  assert {
    condition     = select_team_role.test[0].scope.type == "usage_group" && select_team_role.test[0].scope.id == select_usage_group.role[0].id
    error_message = "The team role grant's scope should read back as the test usage group"
  }

  assert {
    condition     = select_user_role.test[0].id != "" && select_user_role.test[0].etag != ""
    error_message = "SELECT should assign the user role grant an ID and an ETag"
  }

  assert {
    condition     = select_user_role.test[0].role == "viewer" && select_user_role.test[0].scope == null
    error_message = "The user role grant should hold viewer on the whole organization"
  }

  assert {
    condition     = select_default_role.test[0].id != "" && select_default_role.test[0].etag != ""
    error_message = "SELECT should assign the default role grant an ID and an ETag"
  }

  assert {
    condition     = select_default_role.test[0].scope.type == "usage_group" && select_default_role.test[0].scope.id == select_usage_group.role[0].id
    error_message = "The default role grant's scope should read back as the test usage group"
  }
}

# A user role changes in place with PATCH. A team role grant cannot change, so
# a new scope replaces it.
run "update_roles" {
  command = apply

  variables {
    user_role_role           = "editor"
    team_role_on_usage_group = false
  }

  assert {
    condition     = select_user_role.test[0].id == run.create_roles.user_role_id
    error_message = "A role change should not replace the user role grant"
  }

  assert {
    condition     = select_user_role.test[0].role == "editor"
    error_message = "The user role grant's role should change in place"
  }

  assert {
    condition     = select_team_role.test[0].id != run.create_roles.team_role_id
    error_message = "A new scope should replace the team role grant"
  }

  assert {
    condition     = select_team_role.test[0].scope == null
    error_message = "A team role grant with no scope should apply to the whole organization"
  }

  assert {
    condition     = select_default_role.test[0].id == run.create_roles.default_role_id
    error_message = "The default role grant did not change and should not be replaced"
  }
}

# Taking the grants out of the configuration revokes them. Terraform tears
# down whatever is left at the end of the file either way, but that teardown
# asserts nothing and swallows what it cannot remove.
run "delete_roles" {
  command = apply

  variables {
    user_role_role           = "editor"
    team_role_on_usage_group = false
    enable_role_tests        = false
  }

  assert {
    condition     = length(select_team_role.test) == 0 && length(select_user_role.test) == 0 && length(select_default_role.test) == 0
    error_message = "The role grants should have been revoked"
  }
}
