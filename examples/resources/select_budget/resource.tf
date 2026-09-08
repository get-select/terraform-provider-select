# A recurring, organization-wide budget. With no filter_expression_json, every
# dollar of spend the organization records counts toward it.
resource "select_budget" "monthly_org_wide" {
  name   = "Acme Monthly"
  amount = 50000

  period = {
    schedule_type = "monthly"
    start_date    = "2026-01-01"
  }
}

# A budget scoped to a team and narrowed to one warehouse's spend, expiring
# after four quarters.
#
# The filter's root must be a group — {"operator": "and"|"or", "filters":
# [...]} — never a bare leaf filter. A leaf at the root plans clean, because
# Terraform cannot see inside an opaque JSON string, and then 422s on apply:
# the API's structured filter only accepts a group at that position.
resource "select_budget" "analytics_team_warehouse" {
  name    = "Analytics Team - SELECT_BACKEND"
  amount  = 5000
  team_id = "2f0899e2-2746-4300-887c-524e64b5a138"

  period = {
    schedule_type             = "quarterly"
    start_date                = "2026-01-01"
    expires_after_occurrences = 4
  }

  filter_expression_json = jsonencode({
    operator = "or"
    filters = [
      {
        field    = "warehouse_name"
        operator = "in"
        values   = ["SELECT_BACKEND"]
      }
    ]
  })
}
