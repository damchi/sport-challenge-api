variable "alert_email" {
  description = "Address that receives the alerts."
  type        = string
  sensitive   = true
}

variable "api_service_name" {
  description = "Name of the Cloud Run service of the API."
  type        = string
}

variable "close_weeks_job_name" {
  description = "Name of the Cloud Run job of the close-weeks task."
  type        = string
}

variable "api_5xx_threshold" {
  description = "Number of 5xx responses in 5 minutes above which the API alerts."
  type        = number
  default     = 5
}
