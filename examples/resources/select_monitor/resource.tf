# A condition monitor: it posts to Slack when yesterday's spend is more than
# 5000. GET /v2/monitors/query-classes lists the query classes, and
# GET /v2/monitors/query-classes/{query_class}/columns lists their columns.
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
      query_object = { measures = ["sum_spend"] }
      period       = { period = 1, grain = "day" }
    })
  }

  message_config_json = jsonencode({
    sections = [
      # 1. A sentence that states yesterday's spend and compares it with the
      #    same day last week.
      {
        section_type               = "paragraph"
        include_period_over_period = true
        body_template = join(" ", [
          "{{ period_analyzed }}, {{ metric_name | lower }} exceeded the threshold",
          "of **{{ threshold }}** at **{{ latest_measure_value }}**,",
          "{{ latest_measure_value_pct_change }} from the",
          "{{ previous_period_measure_value }} the {{ previous_period }}.",
        ])
        query = {
          query_class  = "OrgDailySpendQuery"
          query_object = { measures = ["sum_spend"] }
          period       = { period = 1, grain = "day" }
          compare_with = { period = 7, grain = "day" }
        }
      },
      # 2. Daily spend for the last 30 days, with the threshold drawn as a line.
      {
        section_type         = "chart"
        threshold            = 5000
        highlight_last_point = true
        query = {
          query_class = "OrgDailySpendQuery"
          query_object = {
            measures   = ["sum_spend"]
            dimensions = ["day"]
            sort       = [{ field = "day", direction = "asc" }]
          }
          period = { period = 30, grain = "day", fill_missing_dates = true }
        }
      },
      # 3. The 10 services with the most spend yesterday.
      {
        section_type = "table"
        title        = "Top services by spend"
        columns      = ["Service", "Spend", "% Change"]
        query = {
          query_class = "OrgDailySpendQuery"
          query_object = {
            sort  = [{ field = "sum_spend", direction = "desc" }]
            limit = 10
          }
          period = { period = 1, grain = "day" }
        }
      },
    ]
  })
}

# A budget monitor: it emails when actual spend passes 80% and 100% of the
# budget. It uses the same three sections. A paragraph with no body_template
# gets the budget sentence that SELECT generates.
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
      {
        section_type = "paragraph"
        query = {
          query_class  = "OrgDailySpendQuery"
          query_object = { measures = ["sum_spend"] }
          period       = { period = 1, grain = "day" }
        }
      },
      {
        section_type = "chart"
        query = {
          query_class = "OrgDailySpendQuery"
          query_object = {
            measures   = ["sum_spend"]
            dimensions = ["day"]
            sort       = [{ field = "day", direction = "asc" }]
          }
        }
      },
      {
        section_type = "table"
        title        = "Top services by spend"
        columns      = ["Service", "Spend"]
        query = {
          query_class = "OrgDailySpendQuery"
          query_object = {
            sort  = [{ field = "sum_spend", direction = "desc" }]
            limit = 10
          }
        }
      },
    ]
  })
}
