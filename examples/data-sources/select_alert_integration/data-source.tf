# Find the Slack integration for one workspace. Connect Slack in the SELECT UI
# first.
data "select_alert_integration" "slack" {
  integration_type     = "slack"
  slack_workspace_name = "Acme"
}

resource "select_slack_alert_destination" "alerts" {
  integration_id = data.select_alert_integration.slack.id
  channel        = "cost-alerts"
}
