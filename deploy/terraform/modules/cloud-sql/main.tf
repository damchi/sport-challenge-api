resource "google_sql_database_instance" "this" {
  name             = var.name
  region           = var.region
  database_version = var.database_version

  # Protects against `terraform destroy`; the same flag is set on the instance
  # itself below, so that the console and the API refuse the deletion too.
  deletion_protection = var.deletion_protection

  # The private services connection must exist before the private IP.
  depends_on = [var.private_services_connection]

  settings {
    edition           = "ENTERPRISE"
    tier              = var.tier
    availability_type = var.availability_type

    deletion_protection_enabled = var.deletion_protection

    ip_configuration {
      # Private IP only: no address reachable from the internet.
      ipv4_enabled    = false
      private_network = var.network_id
      ssl_mode        = "ENCRYPTED_ONLY"
    }

    backup_configuration {
      enabled                        = true
      location                       = var.region # not the default multi-region
      start_time                     = "07:00"    # UTC, the night in Montreal
      point_in_time_recovery_enabled = true
      transaction_log_retention_days = 7

      backup_retention_settings {
        retained_backups = var.retained_backups
        retention_unit   = "COUNT"
      }
    }
  }
}

resource "google_sql_database" "this" {
  name     = var.database_name
  instance = google_sql_database_instance.this.name
}

# URL-safe on purpose: the password goes into a connection URL.
resource "random_password" "user" {
  length  = 32
  special = false
}

resource "google_sql_user" "this" {
  name     = var.user_name
  instance = google_sql_database_instance.this.name
  password = random_password.user.result
}
