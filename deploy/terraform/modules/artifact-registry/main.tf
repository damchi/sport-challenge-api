resource "google_artifact_registry_repository" "this" {
  repository_id = var.repository_id
  location      = var.region
  format        = "DOCKER"
  description   = "Images of the API and the worker"
}
