terraform {
  required_version = ">= 1.9"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 7.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.7"
    }
  }

  # The bucket is given at init time (-backend-config=bucket=...), see docs/terraform.md.
  backend "gcs" {
    prefix = "staging"
  }
}

provider "google" {
  project = var.project_id
  region  = var.region

  # The budget and its alert channel are billed and quota-checked on this project.
  user_project_override = true
  billing_project       = var.project_id
}

locals {
  apis = [
    "artifactregistry.googleapis.com",
    "billingbudgets.googleapis.com",
    "compute.googleapis.com",
    "monitoring.googleapis.com",
    "secretmanager.googleapis.com",
    "servicenetworking.googleapis.com",
    "sqladmin.googleapis.com",
  ]

  database_secret = "database-url"
}

resource "google_project_service" "apis" {
  for_each = toset(local.apis)
  service  = each.key

  # Turning an API off with `destroy` would break what other resources use.
  disable_on_destroy = false
}

data "google_project" "this" {
  project_id = var.project_id
}

module "network" {
  source     = "../../modules/network"
  depends_on = [google_project_service.apis]
}

module "cloud_sql" {
  source = "../../modules/cloud-sql"

  region                      = var.region
  network_id                  = module.network.network_id
  private_services_connection = module.network.private_services_connection
  tier                        = var.db_tier
  availability_type           = var.db_availability_type
  deletion_protection         = var.db_deletion_protection

  depends_on = [google_project_service.apis]
}

module "secrets" {
  source = "../../modules/secrets"

  region = var.region
  # The value of the Auth0 secret is added by hand (docs/terraform.md).
  secret_ids = [local.database_secret, "auth0-mgmt-client-secret"]

  depends_on = [google_project_service.apis]
}

# The connection URL is built here, from the private IP: it carries the
# password, hence it lives in Secret Manager and in the (private) state only.
resource "google_secret_manager_secret_version" "database_url" {
  secret = module.secrets.ids[local.database_secret]
  secret_data = format("postgres://%s:%s@%s:5432/%s?sslmode=require",
    module.cloud_sql.user_name,
    module.cloud_sql.user_password,
    module.cloud_sql.private_ip_address,
  module.cloud_sql.database_name)
}

module "artifact_registry" {
  source = "../../modules/artifact-registry"

  region = var.region

  depends_on = [google_project_service.apis]
}

module "budget" {
  source = "../../modules/budget"

  display_name       = "sport-challenge staging"
  billing_account_id = var.billing_account_id
  project_number     = data.google_project.this.number
  amount             = var.budget_amount
  currency_code      = var.budget_currency_code
  alert_email        = var.budget_alert_email

  depends_on = [google_project_service.apis]
}
