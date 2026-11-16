output "image_repository" {
  description = "Prefix of the image names."
  value       = module.artifact_registry.repository_url
}

output "database_secret" {
  description = "Secret that holds DATABASE_URL."
  value       = module.secrets.ids["database-url"]
}

output "api_url" {
  description = "Public URL of the API."
  value       = module.cloud_run.api_url
}

output "migrate_job" {
  description = "Cloud Run job that applies the migrations (docs/migrations.md)."
  value       = module.cloud_run.migrate_job
}

output "workload_identity_provider" {
  description = "GCP_WORKLOAD_IDENTITY_PROVIDER of the GitHub Environment (docs/deploiement.md)."
  value       = module.deploy_identity.workload_identity_provider
}

output "deploy_service_account" {
  description = "GCP_DEPLOY_SERVICE_ACCOUNT of the GitHub Environment (docs/deploiement.md)."
  value       = module.deploy_identity.service_account
}
