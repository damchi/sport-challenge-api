output "ids" {
  description = "Full resource id of each secret, by secret id."
  value       = { for id, secret in google_secret_manager_secret.this : id => secret.id }
}
