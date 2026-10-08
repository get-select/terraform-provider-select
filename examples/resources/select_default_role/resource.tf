# Every member of the organization can view everything.
resource "select_default_role" "everyone_views" {
  role = "viewer"
}

# Every member of the organization can edit monitors.
resource "select_default_role" "everyone_edits_monitors" {
  role = "monitor_editor"
}
