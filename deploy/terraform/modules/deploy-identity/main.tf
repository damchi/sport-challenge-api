data "google_project" "this" {}

# The pipeline signs in with the OIDC token of its GitHub job: there is no
# service account key anywhere.
resource "google_iam_workload_identity_pool" "github" {
  workload_identity_pool_id = "${var.name}-github"
  display_name              = "GitHub Actions"
}

resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "github"
  display_name                       = "GitHub"

  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "attribute.repository" = "assertion.repository"
  }

  # No token from another repository, nor from a branch other than main, is
  # accepted: the subject of an Environment job does not carry the branch, so a
  # modified workflow on another branch would otherwise be let in.
  attribute_condition = "assertion.repository == \"${var.github_repository}\" && assertion.ref == \"refs/heads/main\""

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
}

resource "google_service_account" "deploy" {
  account_id   = "${var.name}-deploy"
  display_name = "Sport Challenge deployment"
}

# Only the job that runs in this GitHub Environment (and so, for production,
# after its approval) can become the deployment account.
resource "google_service_account_iam_member" "github" {
  service_account_id = google_service_account.deploy.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principal://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/subject/repo:${var.github_repository}:environment:${var.github_environment}"
}

# Pushing the image.
resource "google_artifact_registry_repository_iam_member" "push" {
  repository = var.repository_id
  location   = var.region
  role       = "roles/artifactregistry.writer"
  member     = google_service_account.deploy.member
}

# Deploying the service and the jobs, and running the migrations job.
resource "google_cloud_run_v2_service_iam_member" "deploy" {
  name     = var.service_name
  location = var.region
  role     = "roles/run.developer"
  member   = google_service_account.deploy.member
}

resource "google_cloud_run_v2_job_iam_member" "deploy" {
  for_each = var.job_names
  name     = each.value
  location = var.region
  role     = "roles/run.developer"
  member   = google_service_account.deploy.member
}

# A revision runs as the service account of the workload: updating it requires
# the right to act as that account.
resource "google_service_account_iam_member" "act_as" {
  for_each           = var.runtime_service_accounts
  service_account_id = each.value
  role               = "roles/iam.serviceAccountUser"
  member             = google_service_account.deploy.member
}
