# Every member of the organization can view everything.
resource "select_default_role" "everyone_views" {
  role = "viewer"
}

# Every member of the organization can create teams.
resource "select_default_role" "everyone_creates_teams" {
  role = "team_creator"
}
