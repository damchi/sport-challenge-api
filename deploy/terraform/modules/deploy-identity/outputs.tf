output "workload_identity_provider" {
  description = "Value of the GCP_WORKLOAD_IDENTITY_PROVIDER variable of the GitHub Environment."
  value       = google_iam_workload_identity_pool_provider.github.name
}

output "service_account" {
  description = "Value of the GCP_DEPLOY_SERVICE_ACCOUNT variable of the GitHub Environment."
  value       = google_service_account.deploy.email
}
