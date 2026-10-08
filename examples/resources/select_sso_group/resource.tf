resource "select_usage_group_set" "departments" {
  name  = "Departments"
  order = 1
}

resource "select_usage_group" "marketing" {
  name               = "Marketing"
  order              = 1
  usage_group_set_id = select_usage_group_set.departments.id

  filter_expression_json = jsonencode({
    operator = "and"
    filters = [
      {
        field    = "role_name"
        operator = "in"
        values   = ["MARKETING"]
      }
    ]
  })
}

# Members of the "data-analysts" group in your identity provider receive these
# roles. The name must match the group name that your identity provider sends.
resource "select_sso_group" "data_analysts" {
  name = "data-analysts"

  roles = [
    # Without a scope, the role applies to the whole organization.
    { role = "monitor_editor" },
    {
      role = "viewer"
      scope = {
        type = "usage_group"
        id   = select_usage_group.marketing.id
      }
    },
  ]
}

# A team can hold the SSO group as a member. Members of the group then also
# hold the team's roles.
resource "select_team" "analysts" {
  name = "Analysts"
}

resource "select_team_member" "data_analysts" {
  team_id    = select_team.analysts.id
  type       = "sso_group"
  identifier = select_sso_group.data_analysts.name
  role       = "viewer"
}
