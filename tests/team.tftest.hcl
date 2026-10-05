# SPDX-License-Identifier: MPL-2.0

# Team tests: select_team, select_team_member and the select_team data source.
#
# These are separate from provider.tftest.hcl for the same reason the budget
# suite is: they share tests/main.tf's root module but have nothing to do with
# usage groups. A team makes no call to an external system, so this suite
# needs nothing beyond the same API key and organization every other test
# already uses, and it joins the provider's e2e CI matrix. Run with
# `make test-team`.

variables {
  enable_team_tests = true
  # This suite shares tests/main.tf's root module with provider.tftest.hcl, so
  # every apply here would otherwise also create the usage group resources
  # that file needs.
  enable_usage_group_tests = false
}

# The team and its member are created with the ETags every later write needs,
# and the data source finds the team by name.
run "create_team" {
  command = apply

  assert {
    condition     = select_team.test[0].name == var.team_name
    error_message = "Team name should match expected value"
  }

  assert {
    condition     = select_team.test[0].id != "" && select_team.test[0].etag != ""
    error_message = "SELECT should assign an ID and an ETag when the team is created"
  }

  assert {
    condition     = select_team.test[0].default_member_role == "editor" && select_team.test[0].is_all_users == false
    error_message = "default_member_role and is_all_users should hold their defaults"
  }

  assert {
    condition     = select_team_member.test[0].team_id == select_team.test[0].id
    error_message = "The member should belong to the test team"
  }

  assert {
    condition     = select_team_member.test[0].id != "" && select_team_member.test[0].etag != ""
    error_message = "SELECT should assign the member an ID and an ETag"
  }

  assert {
    condition     = select_team_member.test[0].role == "editor"
    error_message = "The member's role should be recorded"
  }

  assert {
    condition     = data.select_team.test[0].id == select_team.test[0].id
    error_message = "The data source should find the team by its name"
  }
}

# A rename, a new default_member_role and a new member role are all in-place
# updates: neither the team nor the member is replaced, and the data source
# finds the team by its new name.
run "update_team_in_place" {
  command = apply

  variables {
    team_name_suffix         = "-renamed"
    team_default_member_role = "viewer"
    team_member_role         = "viewer"
  }

  assert {
    condition     = select_team.test[0].name == "${var.team_name}-renamed"
    error_message = "The team should have been renamed in place"
  }

  assert {
    condition     = select_team.test[0].id == run.create_team.team_id
    error_message = "A rename should not replace the team"
  }

  assert {
    condition     = select_team.test[0].default_member_role == "viewer"
    error_message = "default_member_role should change in place"
  }

  assert {
    condition     = select_team_member.test[0].id == run.create_team.team_member_id
    error_message = "A role change should not replace the member"
  }

  assert {
    condition     = select_team_member.test[0].role == "viewer"
    error_message = "The member's role should change in place"
  }

  assert {
    condition     = data.select_team.test[0].id == select_team.test[0].id
    error_message = "The data source should find the team by its new name"
  }
}

# Taking the team out of the configuration removes the member and deletes the
# team. Terraform tears down whatever is left at the end of the file either
# way, but that teardown asserts nothing and swallows what it cannot remove.
run "delete_team" {
  command = apply

  variables {
    team_name_suffix  = "-renamed"
    enable_team_tests = false
  }

  assert {
    condition     = length(select_team.test) == 0 && length(select_team_member.test) == 0
    error_message = "The team and its member should have been destroyed"
  }
}
