# SPDX-License-Identifier: MPL-2.0

# Monitor tests: select_alert_integration (resource and data source), the
# email and MS Teams alert destinations, and a budget_threshold monitor that
# sends to both destinations. Slack is not tested: its integration needs a
# browser sign-in. Run with `make test-monitor`.
#
# The alert integrations are organization-wide. The suite manages the MS Teams
# integration and reads the email integration, which the test organization
# must have enabled: other destinations use it, so SELECT refuses to disable
# it. Two suites that ran at the same time could disable the MS Teams
# integration under each other.

variables {
  enable_monitor_tests = true
  # This suite shares tests/main.tf's root module with provider.tftest.hcl.
  enable_usage_group_tests = false
}

run "create_monitor" {
  command = apply

  assert {
    condition     = select_monitor.test[0].id != "" && select_monitor.test[0].etag != ""
    error_message = "SELECT should assign an id and an ETag when the monitor is created"
  }

  assert {
    condition     = select_monitor.test[0].type == "budget_threshold"
    error_message = "type should come from trigger_config.type"
  }

  assert {
    condition     = length(select_monitor.test[0].destination_ids) == 2
    error_message = "The monitor should send to both destinations"
  }

  assert {
    condition     = select_monitor.test[0].trigger_config.thresholds[0].value == 80
    error_message = "The configured threshold should be recorded"
  }

  assert {
    condition     = data.select_alert_integration.email[0].id != ""
    error_message = "The data source should find the organization's email integration"
  }

  assert {
    condition     = select_email_alert_destination.test[0].status != ""
    error_message = "The email destination should have a status"
  }

  assert {
    condition     = select_ms_teams_alert_destination.test[0].webhook_url_update_time != null
    error_message = "The Teams destination should record when its webhook URL was set"
  }
}

# A rename, a new threshold and a new Teams channel are in-place updates: the
# monitor is a PUT and the destination a PATCH, and neither id changes.
run "update_monitor" {
  command = apply

  variables {
    monitor_name_suffix          = "-renamed"
    monitor_threshold            = 90
    monitor_teams_channel_suffix = "-renamed"
  }

  assert {
    condition     = select_monitor.test[0].name == "${var.monitor_name}-renamed"
    error_message = "The monitor should have been renamed in place"
  }

  assert {
    condition     = select_monitor.test[0].id == run.create_monitor.monitor_id
    error_message = "An update should not replace the monitor"
  }

  assert {
    condition     = select_monitor.test[0].trigger_config.thresholds[0].value == 90
    error_message = "The new threshold should be recorded"
  }

  assert {
    condition     = select_ms_teams_alert_destination.test[0].id == run.create_monitor.ms_teams_alert_destination_id
    error_message = "A channel change should not replace the destination"
  }
}

# Taking everything out of the configuration deletes it. Terraform deletes the
# monitor before the destinations and the destinations before the
# integrations, so SELECT blocks none of the deletes.
run "delete_monitor" {
  command = apply

  variables {
    monitor_name_suffix          = "-renamed"
    monitor_teams_channel_suffix = "-renamed"
    enable_monitor_tests         = false
  }

  assert {
    condition     = length(select_monitor.test) == 0 && length(select_email_alert_destination.test) == 0
    error_message = "The monitor and the destinations should have been destroyed"
  }
}
