data "google_project" "this" {}

# Its only right: starting the jobs below.
resource "google_service_account" "scheduler" {
  account_id   = "${var.name}-scheduler"
  display_name = "Sport Challenge scheduler"
}

resource "google_cloud_run_v2_job_iam_member" "scheduler" {
  for_each = var.jobs
  name     = each.value.name
  location = var.region
  role     = "roles/run.invoker"
  member   = google_service_account.scheduler.member
}

# Starts a job through the Cloud Run API. The tasks are idempotent: a retry or
# an overlapping run changes nothing.
resource "google_cloud_scheduler_job" "worker" {
  for_each         = var.jobs
  name             = "${var.name}-${each.key}"
  region           = var.region
  schedule         = each.value.schedule
  time_zone        = "Etc/UTC"
  attempt_deadline = "180s"

  retry_config {
    retry_count = 1
  }

  http_target {
    http_method = "POST"
    uri         = "https://run.googleapis.com/v2/projects/${data.google_project.this.project_id}/locations/${var.region}/jobs/${each.value.name}:run"

    oauth_token {
      service_account_email = google_service_account.scheduler.email
    }
  }

  depends_on = [google_cloud_run_v2_job_iam_member.scheduler]
}
