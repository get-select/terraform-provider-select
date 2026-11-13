resource "select_alert_integration" "email" {
  integration_type = "email"
}

resource "select_email_alert_destination" "finance" {
  integration_id = select_alert_integration.email.id
  email_address  = "finance@example.com"
}
