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
