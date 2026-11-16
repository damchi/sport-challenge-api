variable "name" {
  description = "Prefix of the names of the service account and the pool."
  type        = string
  default     = "sport-challenge"
}

variable "region" {
  description = "Region of the Cloud Run service and jobs, and of the Artifact Registry repository."
  type        = string
}

variable "github_repository" {
  description = "Repository whose workflows deploy, as owner/name."
  type        = string
}

variable "github_repository_id" {
  description = "Numeric id of that repository (gh api repos/<owner>/<name> -q .id)."
  type        = string
}

variable "github_environment" {
  description = "GitHub Environment of the deploy job (staging or production): only that job can use the service account."
  type        = string
}

variable "repository_id" {
  description = "Artifact Registry repository the pipeline pushes the image to."
  type        = string
}

variable "service_name" {
  description = "Cloud Run service of the API."
  type        = string
}

variable "job_names" {
  description = "Cloud Run jobs the pipeline updates: the worker tasks and the migrations."
  type        = set(string)
}

variable "runtime_service_accounts" {
  description = "Ids (projects/.../serviceAccounts/...) of the accounts the service and the jobs run as: updating them requires acting as these."
  type        = set(string)
}
