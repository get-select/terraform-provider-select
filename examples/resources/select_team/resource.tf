# A team whose new members are viewers unless the member says otherwise.
resource "select_team" "data_engineering" {
  name                = "Data Engineering"
  default_member_role = "viewer"
}
