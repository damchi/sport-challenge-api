variable "billing_account_id" {
  description = "Id of the billing account (XXXXXX-XXXXXX-XXXXXX)."
  type        = string
}

variable "project_number" {
  description = "Number (not the id) of the project the budget watches."
  type        = string
}

variable "display_name" {
  description = "Name of the budget."
  type        = string
}

variable "amount" {
  description = "Monthly budget, in whole units of the currency."
  type        = number

  validation {
    condition     = var.amount > 0
    error_message = "amount must be greater than 0: a zero budget alerts all the time."
  }
}

variable "currency_code" {
  description = "Currency of the budget; it must be the one of the billing account."
  type        = string
}

variable "alert_email" {
  description = "Address that receives the alerts."
  type        = string
}
