locals {
  # The API and the worker read the same configuration (config.Load).
  settings = merge(var.env_vars, { ENV = var.environment })
}

# One service account per workload, without any role on the project: the only
# rights are the ones granted below, on the secrets.
resource "google_service_account" "api" {
  account_id   = "${var.name}-api"
  display_name = "Sport Challenge API"
}

resource "google_service_account" "worker" {
  account_id   = "${var.name}-worker"
  display_name = "Sport Challenge worker"
}

resource "google_secret_manager_secret_iam_member" "api" {
  for_each  = var.secrets
  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.api.member
}

resource "google_secret_manager_secret_iam_member" "worker" {
  for_each  = var.secrets
  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.worker.member
}

resource "google_cloud_run_v2_service" "api" {
  name                = "${var.name}-api"
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = var.deletion_protection

  template {
    service_account = google_service_account.api.email

    scaling {
      min_instance_count = 0
      max_instance_count = var.api_max_instances
    }

    # Private ranges only: Cloud SQL goes through the VPC, Auth0 through the internet.
    vpc_access {
      egress = "PRIVATE_RANGES_ONLY"
      network_interfaces {
        network    = var.network_id
        subnetwork = var.subnet_id
      }
    }

    containers {
      image = var.image

      # The image starts the API by default; Cloud Run sets PORT itself.
      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = var.memory
        }
      }

      dynamic "env" {
        for_each = local.settings
        content {
          name  = env.key
          value = env.value
        }
      }

      dynamic "env" {
        for_each = var.secrets
        content {
          name = env.key
          value_source {
            secret_key_ref {
              secret  = env.value
              version = "latest"
            }
          }
        }
      }

      # /healthz does not touch the database: the API starts without it.
      startup_probe {
        http_get {
          path = "/healthz"
        }
      }

      liveness_probe {
        http_get {
          path = "/healthz"
        }
      }
    }
  }

  # The image is deployed by the pipeline: a later apply must not roll it back.
  # gcloud also stamps its own client fields at each deployment.
  lifecycle {
    ignore_changes = [template[0].containers[0].image, client, client_version]
  }

  depends_on = [google_secret_manager_secret_iam_member.api]
}

# The API is public: every route but /healthz and /readyz checks a JWT itself.
resource "google_cloud_run_v2_service_iam_member" "public" {
  name     = google_cloud_run_v2_service.api.name
  location = google_cloud_run_v2_service.api.location
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# One job per worker task, with the task as its argument (same image, other command).
resource "google_cloud_run_v2_job" "worker" {
  for_each            = var.worker_tasks
  name                = "${var.name}-worker-${each.key}"
  location            = var.region
  deletion_protection = var.deletion_protection

  template {
    template {
      service_account = google_service_account.worker.email
      max_retries     = 1

      vpc_access {
        egress = "PRIVATE_RANGES_ONLY"
        network_interfaces {
          network    = var.network_id
          subnetwork = var.subnet_id
        }
      }

      containers {
        image = var.image
        args  = ["worker", each.key]

        resources {
          limits = {
            cpu    = "1"
            memory = var.memory
          }
        }

        dynamic "env" {
          for_each = local.settings
          content {
            name  = env.key
            value = env.value
          }
        }

        dynamic "env" {
          for_each = var.secrets
          content {
            name = env.key
            value_source {
              secret_key_ref {
                secret  = env.value
                version = "latest"
              }
            }
          }
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [template[0].template[0].containers[0].image, client, client_version]
  }

  depends_on = [google_secret_manager_secret_iam_member.worker]
}

# The migrations, before each deployment: a job of its own, so that the API
# never changes the schema when it starts. It runs `migrate up` and only
# needs the database; a failed migration is not retried (it leaves the
# database dirty, and a person decides, see docs/migrations.md).
resource "google_service_account" "migrate" {
  account_id   = "${var.name}-migrate"
  display_name = "Sport Challenge migrations"
}

resource "google_secret_manager_secret_iam_member" "migrate" {
  for_each  = var.migrate_secrets
  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.migrate.member
}

resource "google_cloud_run_v2_job" "migrate" {
  name                = "${var.name}-migrate"
  location            = var.region
  deletion_protection = var.deletion_protection

  template {
    template {
      service_account = google_service_account.migrate.email
      max_retries     = 0

      vpc_access {
        egress = "PRIVATE_RANGES_ONLY"
        network_interfaces {
          network    = var.network_id
          subnetwork = var.subnet_id
        }
      }

      containers {
        image = var.image
        args  = ["migrate", "up"]

        resources {
          limits = {
            cpu    = "1"
            memory = var.memory
          }
        }

        dynamic "env" {
          for_each = var.migrate_secrets
          content {
            name = env.key
            value_source {
              secret_key_ref {
                secret  = env.value
                version = "latest"
              }
            }
          }
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [template[0].template[0].containers[0].image, client, client_version]
  }

  depends_on = [google_secret_manager_secret_iam_member.migrate]
}
