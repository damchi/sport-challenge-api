output "private_ip_address" {
  description = "Private IP of the instance."
  value       = google_sql_database_instance.this.private_ip_address
}

output "database_name" {
  description = "Name of the application database."
  value       = google_sql_database.this.name
}

output "user_name" {
  description = "Name of the application database user."
  value       = google_sql_user.this.name
}

output "user_password" {
  description = "Password of the application database user (also kept in the Terraform state)."
  value       = random_password.user.result
  sensitive   = true
}
