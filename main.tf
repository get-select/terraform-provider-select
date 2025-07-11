terraform {
  required_providers {
    select = {
      source = "hashicorp.com/edu/select"
    }
  }
}

provider "select" {
  api_key         = "sl_3by2dPGN0DDsPCttSHZmcHXRgMZkvgJB"
  organization_id = "org_4e6AWNdsLYS96DkH"
  select_api_url  = "http://localhost:8000"
}

resource "select_usage_group_set" "test_set" {
  name                   = "new-test-name-1"
  order                  = 1
  snowflake_account_uuid = "scwxhob-ad38017"
}

resource "select_usage_group" "test_group" {
  name               = "usage-group-test-1"
  order              = 1
  budget             = 1000.0
  usage_group_set_id = select_usage_group_set.test_set.id
  filter_expression_json = jsonencode({
    "operator" : "or",
    "filters" : [
      {
        "field" : "warehouse_name",
        "values" : ["SELECT_BACKEND", "SELECT_BACKEND_LARGE"],
        "operator" : "in",
      }
    ],
  })
}
