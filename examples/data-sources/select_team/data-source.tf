# A team made in the SELECT UI, looked up by its exact name.
data "select_team" "finance" {
  name = "Finance"
}

resource "select_team_member" "analyst" {
  team_id    = data.select_team.finance.id
  type       = "user"
  identifier = "dana@example.com"
  role       = "viewer"
}
