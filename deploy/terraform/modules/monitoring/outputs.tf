output "notification_channel_id" {
  description = "Id of the e-mail notification channel (the budget uses it too)."
  value       = google_monitoring_notification_channel.email.id
}
