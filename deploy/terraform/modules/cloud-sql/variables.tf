variable "name" {
  description = "Name of the Cloud SQL instance."
  type        = string
  default     = "sport-challenge"
}

variable "region" {
  description = "Region of the instance."
  type        = string
}

variable "network_id" {
  description = "VPC network that gets the private IP of the instance."
  type        = string
}

variable "private_services_connection" {
  description = "Id of the private services connection of the network (orders the creation)."
  type        = string
}

variable "database_version" {
  description = "PostgreSQL version; the same major version as the local compose."
  type        = string
  default     = "POSTGRES_16"
}

variable "tier" {
  description = "Machine type of the instance."
  type        = string
}

variable "availability_type" {
  description = "ZONAL, or REGIONAL for high availability."
  type        = string
  default     = "ZONAL"

  validation {
    condition     = contains(["ZONAL", "REGIONAL"], var.availability_type)
    error_message = "availability_type must be ZONAL or REGIONAL."
  }
}

variable "deletion_protection" {
  description = "Refuse the deletion of the instance."
  type        = bool
  default     = true
}

variable "retained_backups" {
  description = "Number of automated backups kept."
  type        = number
  default     = 7
}

variable "database_name" {
  description = "Name of the application database."
  type        = string
  default     = "sport_challenge"
}

variable "user_name" {
  description = "Name of the application database user."
  type        = string
  default     = "sport"
}
