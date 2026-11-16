output "repository_url" {
  description = "Prefix of the image names: <repository_url>/api:<tag>."
  value       = "${google_artifact_registry_repository.this.location}-docker.pkg.dev/${google_artifact_registry_repository.this.project}/${google_artifact_registry_repository.this.repository_id}"
}

output "repository_id" {
  description = "Id of the repository, to grant rights on it."
  value       = google_artifact_registry_repository.this.repository_id
}
