resource "select_team" "analysts" {
  name = "Analysts"
}

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

# Every member of the team can view the Marketing usage group. A usage_group
# scope accepts only the viewer role.
resource "select_team_role" "analysts_marketing" {
  team_id = select_team.analysts.id
  role    = "viewer"
  scope = {
    type = "usage_group"
    id   = select_usage_group.marketing.id
  }
}

# Without a scope, the role applies to the whole organization.
resource "select_team_role" "analysts_monitors" {
  team_id = select_team.analysts.id
  role    = "monitor_editor"
}
