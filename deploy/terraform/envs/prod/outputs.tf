output "image_repository" {
  description = "Prefix of the image names."
  value       = module.artifact_registry.repository_url
}

output "database_secret" {
  description = "Secret that holds DATABASE_URL."
  value       = module.secrets.ids["database-url"]
}
