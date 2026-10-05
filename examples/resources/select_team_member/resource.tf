resource "select_team" "data_engineering" {
  name = "Data Engineering"
}

# One select_team_member per user, so adding or removing an email changes only
# that member.
locals {
  data_engineers = toset([
    "alice@example.com",
    "bob@example.com",
  ])
}

resource "select_team_member" "data_engineers" {
  for_each = local.data_engineers

  team_id    = select_team.data_engineering.id
  type       = "user"
  identifier = each.value
}

# A team lead with the admin role on the team.
resource "select_team_member" "lead" {
  team_id    = select_team.data_engineering.id
  type       = "user"
  identifier = "carol@example.com"
  role       = "admin"
}

# Everyone in an SSO group from your identity provider.
resource "select_team_member" "platform_group" {
  team_id    = select_team.data_engineering.id
  type       = "sso_group"
  identifier = "platform-engineers"
  role       = "viewer"
}
