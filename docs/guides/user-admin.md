---
page_title: "Manage users and roles"
description: |-
  Give people access to SELECT with SSO groups, teams, direct role grants and default roles.
---

<!-- /docs/ is auto generated from the provider schema, and the templates in /templates do not edit files in /docs directly. -->

# Manage users and roles

This guide shows how to give people access to SELECT with Terraform. It ends with a full example.

## Users come from your identity provider

SELECT gets its users from Auth0 and from your SSO identity provider. A person becomes a SELECT user when they sign in. The provider has no user resource: each resource identifies a user by email address.

A role grant works for an email address before that person signs in. SELECT keeps the grant against the email address, and the grant takes effect when the person first signs in. SELECT accepts a team member and a direct role grant for an email address that has not signed in.

## Where a user's roles come from

A user holds a role from one of four sources. Each source has its own resource.

| Source | Who holds the role | Resource |
| --- | --- | --- |
| Direct | One user, by email address | [`select_user_role`](https://registry.terraform.io/providers/get-select/select/latest/docs/resources/user_role) |
| Team | Every member of the team | [`select_team_role`](https://registry.terraform.io/providers/get-select/select/latest/docs/resources/team_role) |
| Default | Every member of the organization | [`select_default_role`](https://registry.terraform.io/providers/get-select/select/latest/docs/resources/default_role) |
| SSO group | Every member of the identity provider group | [`select_sso_group`](https://registry.terraform.io/providers/get-select/select/latest/docs/resources/sso_group) |

Use [`select_team`](https://registry.terraform.io/providers/get-select/select/latest/docs/resources/team) to make a team, and [`select_team_member`](https://registry.terraform.io/providers/get-select/select/latest/docs/resources/team_member) to add one user or one SSO group to it. Use `for_each` over a set of email addresses to add many users. Then adding or removing an email address changes only that member.

### SSO groups

[`select_sso_group`](https://registry.terraform.io/providers/get-select/select/latest/docs/resources/sso_group) maps a group from your identity provider to SELECT roles. Set `name` to the group name exactly as your identity provider sends it. The roles are part of the resource, in its `roles` set. A role change applies on each member's next request. A change to who is in the group, or a rename, applies at each member's next login.

An SSO group can also be a member of a team. Then the members of the group also hold the roles of the team.

### Grants that Terraform does not manage

Each `select_user_role`, `select_team_role`, `select_default_role` and `select_team_member` manages one grant or one membership. Terraform does not change grants and memberships that it does not manage. The `roles` set of `select_sso_group` is different: it holds all roles of the group, and a role granted outside Terraform shows as a change in the next plan.

## Check that an email address is a user

The [`select_users`](https://registry.terraform.io/providers/get-select/select/latest/docs/data-sources/users) data source lists the users of the organization. Set `emails` to get only the users with those email addresses. An email address that belongs to no user does not cause an error. Compare `emails` with the email addresses in `users` to find the missing ones. The example below uses a `check` block, which gives a warning. To stop the apply instead, use a `precondition`, as the data source page shows.

## What the provider does not manage

- **Invitations.** The provider does not invite people. If a person cannot sign in through your identity provider, invite them in the SELECT app.
- **User removal.** The provider does not remove a user from the organization. When you destroy a grant or a membership, SELECT revokes only that grant or membership. When you delete a user in SELECT, SELECT also deletes every direct grant and team membership of their email address, including those that Terraform manages. The next apply then makes them again. When you delete a user, also remove their email address from your configuration.
- **Batch endpoints.** The provider does not use `POST /v2/users/actions/batch-update-roles` or `POST /v2/teams/actions/batch-update-memberships`. Each batch endpoint replaces all of a user's direct grants or team memberships. That would conflict with the resources above, which manage one grant or one membership each. Do not use the batch endpoints for users that Terraform manages.

## Full example

The example makes:

1. An SSO group with the `team_creator` role. The role gives no access to data.
2. A team with the SSO group and a set of email addresses as members. Each member has the `viewer` role in the team.
3. A usage group set that belongs to the team. The team gets `viewer` on each usage group in the set. This is the only access to data that the SSO group and the analysts get.
4. A direct `admin` grant for each admin email address, and a `check` block that warns when an admin email address is not a user yet.
5. One default role for every member of the organization: `team_creator`.

An organization-wide role other than `team_creator` gives access to all data. Then a grant on one usage group adds nothing. Give the SSO group and the default role a narrow role or a scope, as this example does.

```terraform
terraform {
  required_providers {
    select = {
      source  = "get-select/select"
      version = "~> 0.1"
    }
  }
}

variable "select_api_key" {
  type      = string
  sensitive = true
}

variable "select_organization_id" {
  type = string
}

# The group name, exactly as your identity provider sends it.
variable "sso_group_name" {
  type    = string
  default = "data-analysts"
}

# Users to add to the team one by one, in addition to the SSO group.
variable "analyst_emails" {
  type    = set(string)
  default = ["alice@example.com", "bob@example.com"]
}

# Users who get the admin role directly.
variable "admin_emails" {
  type    = set(string)
  default = ["carol@example.com"]
}

provider "select" {
  api_key         = var.select_api_key
  organization_id = var.select_organization_id
}

# 1. SSO group. A role change applies on each member's next request. SELECT
# requires at least one role. team_creator gives no access to data: the
# members get their access to data from the team below.
resource "select_sso_group" "analysts" {
  name = var.sso_group_name

  roles = [
    { role = "team_creator" },
  ]
}

# 2. Team. Its members are the SSO group and each email in analyst_emails.
resource "select_team" "analysts" {
  name = "Analysts"
}

resource "select_team_member" "analysts_group" {
  team_id    = select_team.analysts.id
  type       = "sso_group"
  identifier = select_sso_group.analysts.name
  role       = "viewer"
}

# One member per email, so adding or removing an email changes only that member.
resource "select_team_member" "analysts" {
  for_each = var.analyst_emails

  team_id    = select_team.analysts.id
  type       = "user"
  identifier = each.value
  role       = "viewer"
}

# 3. Usage group set that belongs to the team, and its usage groups.
resource "select_usage_group_set" "analytics" {
  name    = "Analytics"
  order   = 1
  team_id = select_team.analysts.id
}

locals {
  # Each usage group selects the spend of some Snowflake roles.
  usage_groups = {
    Marketing = { order = 1, snowflake_roles = ["MARKETING"] }
    Finance   = { order = 2, snowflake_roles = ["FINANCE"] }
  }
}

resource "select_usage_group" "analytics" {
  for_each = local.usage_groups

  name               = each.key
  order              = each.value.order
  usage_group_set_id = select_usage_group_set.analytics.id

  filter_expression_json = jsonencode({
    operator = "and"
    filters = [
      {
        field    = "role_name"
        operator = "in"
        values   = each.value.snowflake_roles
      }
    ]
  })
}

# The team can view each usage group in the set. A usage_group scope accepts
# only the viewer role.
resource "select_team_role" "analysts_view_usage_groups" {
  for_each = select_usage_group.analytics

  team_id = select_team.analysts.id
  role    = "viewer"
  scope = {
    type = "usage_group"
    id   = each.value.id
  }
}

# 4. Direct grants. A grant works for an email before that person signs in.
resource "select_user_role" "admins" {
  for_each = var.admin_emails

  email = each.value
  role  = "admin"
}

# Warns, and does not fail the apply, when an admin email is not a SELECT
# user yet. That person receives the grant when they first sign in.
check "admins_are_users" {
  data "select_users" "admins" {
    emails = var.admin_emails
  }

  assert {
    condition = length(setsubtract(
      [for email in var.admin_emails : lower(email)],
      [for user in data.select_users.admins.users : lower(user.email)],
    )) == 0
    error_message = "These admin emails are not SELECT users yet: ${join(", ", setsubtract(
      [for email in var.admin_emails : lower(email)],
      [for user in data.select_users.admins.users : lower(user.email)],
    ))}. Their admin role takes effect when they first sign in."
  }
}

# 5. Default role. Every member of the organization can create teams. Any other
# role without a scope gives every member access to all data.
resource "select_default_role" "everyone_creates_teams" {
  role = "team_creator"
}
```
