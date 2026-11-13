# The address that receives every alert of the environment, the budget's too.
resource "google_monitoring_notification_channel" "email" {
  display_name = "Alerts"
  type         = "email"

  labels = {
    email_address = var.alert_email
  }
}

resource "google_monitoring_alert_policy" "api_errors" {
  display_name          = "API: server errors (5xx)"
  combiner              = "OR"
  notification_channels = [google_monitoring_notification_channel.email.id]

  conditions {
    display_name = "More than ${var.api_5xx_threshold} responses 5xx in 5 minutes"

    condition_threshold {
      filter          = "resource.type = \"cloud_run_revision\" AND resource.labels.service_name = \"${var.api_service_name}\" AND metric.type = \"run.googleapis.com/request_count\" AND metric.labels.response_code_class = \"5xx\""
      comparison      = "COMPARISON_GT"
      threshold_value = var.api_5xx_threshold
      duration        = "0s"

      aggregations {
        alignment_period     = "300s"
        per_series_aligner   = "ALIGN_SUM"
        cross_series_reducer = "REDUCE_SUM"
      }
    }
  }

  alert_strategy {
    auto_close = "1800s"
  }

  documentation {
    mime_type = "text/markdown"
    content   = "The API answers with 5xx errors. Read the logs of the service (severity ERROR, with the request_id) and Error Reporting."
  }
}

# Any Cloud Run job of the project: the worker tasks and the migrations.
resource "google_monitoring_alert_policy" "job_failed" {
  display_name          = "Worker and migrations: job failed"
  combiner              = "OR"
  notification_channels = [google_monitoring_notification_channel.email.id]

  conditions {
    display_name = "A job execution failed"

    condition_threshold {
      filter          = "resource.type = \"cloud_run_job\" AND metric.type = \"run.googleapis.com/job/completed_execution_count\" AND metric.labels.result = \"failed\""
      comparison      = "COMPARISON_GT"
      threshold_value = 0
      duration        = "0s"

      aggregations {
        alignment_period     = "300s"
        per_series_aligner   = "ALIGN_SUM"
        cross_series_reducer = "REDUCE_SUM"
      }
    }
  }

  alert_strategy {
    auto_close = "1800s"
  }

  documentation {
    mime_type = "text/markdown"
    content   = "A Cloud Run job failed. Read the logs of the execution. A worker task can simply be started again (it is idempotent); a failed migration leaves the database dirty: follow docs/migrations.md."
  }
}

# close-weeks runs every hour: no success for three hours means that it fails
# or that the schedule does not start it, and eliminations are late.
resource "google_monitoring_alert_policy" "close_weeks_missing" {
  display_name          = "Worker: close-weeks has not succeeded for 3 hours"
  combiner              = "OR"
  notification_channels = [google_monitoring_notification_channel.email.id]

  conditions {
    display_name = "No successful close-weeks execution for 3 hours"

    condition_absent {
      filter   = "resource.type = \"cloud_run_job\" AND resource.labels.job_name = \"${var.close_weeks_job_name}\" AND metric.type = \"run.googleapis.com/job/completed_execution_count\" AND metric.labels.result = \"succeeded\""
      duration = "10800s"

      aggregations {
        alignment_period   = "3600s"
        per_series_aligner = "ALIGN_SUM"
      }
    }
  }

  alert_strategy {
    auto_close = "1800s"
  }

  documentation {
    mime_type = "text/markdown"
    content   = "close-weeks has not succeeded for 3 hours. Check the Cloud Scheduler job and the executions of the Cloud Run job, then start it again by hand if needed."
  }
}
