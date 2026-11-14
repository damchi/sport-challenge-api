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

variable "alert_email" {
  description = "Address that receives the alerts (budget, API errors, worker)."
  type        = string
  sensitive   = true
}

variable "db_tier" {
  description = "Machine type of the Cloud SQL instance."
  type        = string
  default     = "db-f1-micro"
}

variable "db_availability_type" {
  description = "ZONAL, or REGIONAL for high availability."
  type        = string
  default     = "ZONAL"
}

variable "db_deletion_protection" {
  description = "Refuse the deletion of the Cloud SQL instance."
  type        = bool
  default     = false
}

variable "api_5xx_threshold" {
  description = "Number of 5xx responses in 5 minutes above which the API alerts."
  type        = number
  default     = 5
}

variable "run_deletion_protection" {
  description = "Refuse the deletion of the Cloud Run service and jobs."
  type        = bool
  default     = false
}

variable "image" {
  description = "Image of the API and the worker (<image_repository>/api:<tag>), pushed to Artifact Registry before the first apply."
  type        = string
}

variable "log_level" {
  description = "LOG_LEVEL of the API and the worker."
  type        = string
  default     = "info"
}

variable "auth0_domain" {
  description = "Host name of the Auth0 tenant of this environment (without scheme)."
  type        = string
}

variable "auth0_audience" {
  description = "Identifier of the API in Auth0."
  type        = string
}

variable "auth0_mgmt_client_id" {
  description = "Client id of the Auth0 machine-to-machine application that deletes users."
  type        = string
}

variable "cors_allowed_origins" {
  description = "Web origins allowed to call the API from a browser (scheme://host, no wildcard)."
  type        = list(string)
}

variable "trusted_proxies" {
  description = "Addresses or ranges of the proxies in front of the API, from which X-Forwarded-For is read (never wider than /8)."
  type        = list(string)

  validation {
    condition     = length(var.trusted_proxies) > 0
    error_message = "trusted_proxies must not be empty: the rate limit by IP would count every client together."
  }
}

variable "clock_offset" {
  description = "Moves the clock of the API and the worker forward (a Go duration such as 168h) to see a week end in minutes. Empty: the real time. Remove it after the tests."
  type        = string
  default     = ""

  validation {
    # A positive duration, like config.Load requires: "0s" would stop the API and the worker.
    condition     = var.clock_offset == "" || (can(regex("^([0-9]+(\\.[0-9]+)?(ns|us|ms|s|m|h))+$", var.clock_offset)) && can(regex("[1-9]", var.clock_offset)))
    error_message = "clock_offset must be empty or a positive Go duration such as 168h or 90m (no days, not zero)."
  }
}

variable "github_repository" {
  description = "Repository whose workflows deploy to this environment, as owner/name."
  type        = string
  default     = "damchi/sport-challenge-api"
}
