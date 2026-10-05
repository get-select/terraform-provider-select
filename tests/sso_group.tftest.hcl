# SPDX-License-Identifier: MPL-2.0

# SSO group tests: select_sso_group with its inline roles, and a
# select_team_member of type sso_group that refers to the group.
#
# These are separate from provider.tftest.hcl for the same reason the team and
# role suites are: they share tests/main.tf's root module but have nothing to
# do with the usage group suite. An SSO group makes no call to an external
# system, so this suite needs nothing beyond the same API key and organization
# every other test already uses, and it joins the provider's e2e CI matrix.
# Run with `make test-sso-group`.

variables {
  enable_sso_group_tests = true
  # This suite shares tests/main.tf's root module with provider.tftest.hcl, so
  # every apply here would otherwise also create the usage group resources
  # that file needs. The suite makes its own usage group instead.
  enable_usage_group_tests = false
}

# The group is created with both roles in one request. The organization role
# reads back with a null scope, and the usage group role with its scope.
run "create_sso_group" {
  command = apply

  assert {
    condition     = select_sso_group.test[0].id == select_sso_group.test[0].name && select_sso_group.test[0].etag != ""
    error_message = "The group's id should be its name, and SELECT should assign an ETag"
  }

  assert {
    condition     = length(select_sso_group.test[0].roles) == 2
    error_message = "The group should hold two roles"
  }

  assert {
    condition     = anytrue([for r in select_sso_group.test[0].roles : r.role == "editor" && r.scope == null])
    error_message = "The group should hold editor on the whole organization, with a null scope"
  }

  assert {
    condition     = anytrue([for r in select_sso_group.test[0].roles : r.role == "viewer" && try(r.scope.type, "") == "usage_group" && try(r.scope.id, "") == select_usage_group.sso_group[0].id])
    error_message = "The group should hold viewer on the test usage group"
  }

  assert {
    condition     = select_team_member.sso_group[0].type == "sso_group" && select_team_member.sso_group[0].identifier == select_sso_group.test[0].name
    error_message = "The team should hold the group as a member, by its name"
  }
}

# One apply renames the group, grants monitor_editor and revokes editor. The
# rename changes the member's identifier, which replaces the member.
run "update_sso_group" {
  command = apply

  variables {
    sso_group_name_suffix   = "-renamed"
    sso_group_roles_updated = true
  }

  assert {
    condition     = endswith(select_sso_group.test[0].name, "-renamed") && select_sso_group.test[0].id == select_sso_group.test[0].name
    error_message = "The rename should change the group's name and id"
  }

  assert {
    condition     = length(select_sso_group.test[0].roles) == 2
    error_message = "The group should still hold two roles"
  }

  assert {
    condition     = anytrue([for r in select_sso_group.test[0].roles : r.role == "monitor_editor" && r.scope == null])
    error_message = "The group should hold the new role"
  }

  assert {
    condition     = !anytrue([for r in select_sso_group.test[0].roles : r.role == "editor"])
    error_message = "The group should no longer hold the removed role"
  }

  assert {
    condition     = anytrue([for r in select_sso_group.test[0].roles : r.role == "viewer" && try(r.scope.id, "") == select_usage_group.sso_group[0].id])
    error_message = "The unchanged usage group role should stay"
  }

  assert {
    condition     = select_team_member.sso_group[0].identifier == select_sso_group.test[0].name
    error_message = "The team member should refer to the new name"
  }

  assert {
    condition     = select_team_member.sso_group[0].id != run.create_sso_group.sso_group_team_member_id
    error_message = "A new identifier should replace the team member"
  }
}

# Taking the group out of the configuration deletes it and every role it
# grants. Terraform tears down whatever is left at the end of the file either
# way, but that teardown asserts nothing and swallows what it cannot remove.
run "delete_sso_group" {
  command = apply

  variables {
    sso_group_name_suffix   = "-renamed"
    sso_group_roles_updated = true
    enable_sso_group_tests  = false
  }

  assert {
    condition     = length(select_sso_group.test) == 0 && length(select_team_member.sso_group) == 0
    error_message = "The SSO group and its team member should have been deleted"
  }
}
