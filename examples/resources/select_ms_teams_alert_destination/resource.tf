resource "select_alert_integration" "ms_teams" {
  integration_type = "ms_teams_webhook"
}

variable "teams_webhook_url" {
  type      = string
  sensitive = true
}

resource "select_ms_teams_alert_destination" "finops" {
  integration_id = select_alert_integration.ms_teams.id
  channel        = "FinOps"
  webhook_url    = var.teams_webhook_url
}
