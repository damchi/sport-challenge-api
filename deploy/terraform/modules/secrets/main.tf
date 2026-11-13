# Only the secrets themselves are created here, as empty containers: a value
# written by hand never goes through the Terraform state.
resource "google_secret_manager_secret" "this" {
  for_each  = var.secret_ids
  secret_id = each.key

  replication {
    # Kept in the region of the environment, not replicated worldwide.
    user_managed {
      replicas {
        location = var.region
      }
    }
  }
}
