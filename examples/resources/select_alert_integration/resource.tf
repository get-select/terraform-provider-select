# Destroying this resource disables email alerts for the full organization.
resource "select_alert_integration" "email" {
  integration_type = "email"
}
