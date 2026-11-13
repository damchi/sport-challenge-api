variable "name" {
  description = "Prefix of the names of the service account and the schedules."
  type        = string
  default     = "sport-challenge"
}

variable "region" {
  description = "Region of the schedules and of the jobs."
  type        = string
}

variable "jobs" {
  description = "Schedules by task: the name of its Cloud Run job and its cron expression (UTC)."
  type = map(object({
    name     = string
    schedule = string
  }))
}
