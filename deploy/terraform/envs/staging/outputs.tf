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
