variable "name" {
  description = "Name of the VPC network."
  type        = string
  default     = "sport-challenge"
}

variable "cloud_run_cidr" {
  description = "Address range of the Cloud Run subnet (a /26 or wider)."
  type        = string
  default     = "10.10.0.0/24"
}
