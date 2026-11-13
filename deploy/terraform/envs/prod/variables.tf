variable "project_id" {
  description = "Id of the GCP project of this environment (created by hand)."
  type        = string
}

variable "region" {
  description = "Region of every resource."
  type        = string
  default     = "northamerica-northeast1" # Montreal
}

variable "billing_account_id" {
  description = "Id of the billing account (XXXXXX-XXXXXX-XXXXXX)."
  type        = string
}

variable "budget_amount" {
  description = "Monthly budget, in whole units of the currency."
  type        = number
}

variable "budget_currency_code" {
  description = "Currency of the budget; it must be the one of the billing account."
  type        = string
}

variable "budget_alert_email" {
  description = "Address that receives the budget alerts."
  type        = string
}

variable "db_tier" {
  description = "Machine type of the Cloud SQL instance."
  type        = string
  default     = "db-custom-1-3840"
}

variable "db_availability_type" {
  description = "ZONAL, or REGIONAL for high availability."
  type        = string
  default     = "REGIONAL"
}

variable "db_deletion_protection" {
  description = "Refuse the deletion of the Cloud SQL instance."
  type        = bool
  default     = true
}
