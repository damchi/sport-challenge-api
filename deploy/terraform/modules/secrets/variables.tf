variable "secret_ids" {
  description = "Ids of the secrets to create."
  type        = set(string)
}

variable "region" {
  description = "Region where the secrets are stored."
  type        = string
}
