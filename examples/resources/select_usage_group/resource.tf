resource "select_usage_group" "analytics" {
  name               = "Analytics Team"
  order              = 1
  budget             = 5000.0
  usage_group_set_id = select_usage_group_set.production.id
  
  filter_expression_json = jsonencode({
    operator = "and"
    filters = [
      {
        field    = "role_name"
        operator = "in"
        values   = ["ANALYST", "DATA_SCIENTIST"]
      }
    ]
  })
} 