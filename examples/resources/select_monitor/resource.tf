# A budget monitor: it emails when actual spend passes 80% and 100% of the
# budget.
resource "select_monitor" "budget" {
  name        = "Acme Monthly budget"
  data_source = "budget"
  state       = "enabled"
  schedule    = "daily"
  budget_id   = select_budget.monthly_org_wide.id

  destination_ids = [select_email_alert_destination.finance.id]

  notification_frequency_config = {
    frequency_type = "on_trigger"
  }

  trigger_config = {
    type = "budget_threshold"
    thresholds = [
      { metric = "actual_spend", unit = "percent", value = 80 },
      { metric = "actual_spend", unit = "percent", value = 100 },
    ]
  }

  message_config_json = jsonencode({
    sections = [
      { section_type = "title" },
    ]
  })
}

# A condition monitor: it posts to Slack when daily spend is more than 5000.
# GET /v2/monitors/query-classes lists the query classes and their columns.
resource "select_monitor" "daily_spend" {
  name        = "Daily spend above 5000"
  data_source = "organization_spend"
  state       = "enabled"
  schedule    = "daily"

  destination_ids = [select_slack_alert_destination.alerts.id]

  notification_frequency_config = {
    frequency_type     = "limit_x_days"
    frequency_interval = 1
  }

  trigger_config = {
    type      = "condition"
    operator  = ">"
    threshold = 5000
    trigger   = "on_condition_met"
    query_json = jsonencode({
      query_class  = "OrgDailySpendQuery"
      query_object = { measures = ["spend"] }
      period       = { period = 1, grain = "day" }
    })
  }

  message_config_json = jsonencode({
    sections = [
      { section_type = "title" },
      {
        section_type = "chart"
        query = {
          query_class  = "OrgDailySpendQuery"
          query_object = { measures = ["spend"] }
          period       = { period = 30, grain = "day" }
        }
      },
    ]
  })
}
