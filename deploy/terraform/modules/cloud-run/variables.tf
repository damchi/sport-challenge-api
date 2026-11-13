variable "name" {
  description = "Prefix of the names of the service accounts, the service and the jobs."
  type        = string
  default     = "sport-challenge"
}

variable "region" {
  description = "Region of the service and the jobs."
  type        = string
}

variable "environment" {
  description = "Value of ENV: staging or production."
  type        = string
}

variable "image" {
  description = "Image of the API and the worker, with its tag or digest. Used at creation only: the pipeline deploys the next ones."
  type        = string
}

variable "network_id" {
  description = "VPC network through which Cloud Run reaches the database."
  type        = string
}

variable "subnet_id" {
  description = "Subnet of Cloud Run in that network."
  type        = string
}

variable "env_vars" {
  description = "Plain environment variables (not secrets) of the API and the worker."
  type        = map(string)
}

variable "secrets" {
  description = "Secrets injected as environment variables: variable name => full id of the Secret Manager secret."
  type        = map(string)
}

variable "migrate_secrets" {
  description = "Secrets of the migration job: variable name => full id of the Secret Manager secret (the database only)."
  type        = map(string)
}

variable "worker_tasks" {
  description = "Worker tasks, one Cloud Run job each."
  type        = set(string)
}

variable "api_max_instances" {
  description = "Upper bound of API instances: each one opens connections to the database."
  type        = number
  default     = 3
}

variable "memory" {
  description = "Memory of an instance."
  type        = string
  default     = "512Mi"
}

variable "deletion_protection" {
  description = "Refuse the deletion of the service and the jobs."
  type        = bool
  default     = true
}
