output "api_url" {
  description = "Public URL of the API."
  value       = google_cloud_run_v2_service.api.uri
}

output "worker_jobs" {
  description = "Name of the Cloud Run job of each worker task."
  value       = { for task, job in google_cloud_run_v2_job.worker : task => job.name }
}
