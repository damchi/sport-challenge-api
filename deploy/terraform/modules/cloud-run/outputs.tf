output "api_url" {
  description = "Public URL of the API."
  value       = google_cloud_run_v2_service.api.uri
}

output "api_service_name" {
  description = "Name of the Cloud Run service of the API."
  value       = google_cloud_run_v2_service.api.name
}

output "worker_jobs" {
  description = "Name of the Cloud Run job of each worker task."
  value       = { for task, job in google_cloud_run_v2_job.worker : task => job.name }
}

output "migrate_job" {
  description = "Name of the Cloud Run job that applies the migrations."
  value       = google_cloud_run_v2_job.migrate.name
}

output "runtime_service_accounts" {
  description = "Ids of the accounts the service and the jobs run as."
  value = toset([
    google_service_account.api.name,
    google_service_account.worker.name,
    google_service_account.migrate.name,
  ])
}
