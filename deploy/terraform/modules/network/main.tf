# The private network of an environment: Cloud SQL gets a private IP in it, and
# Cloud Run will reach the database through it.
resource "google_compute_network" "this" {
  name                    = var.name
  auto_create_subnetworks = false
}

# Address range reserved for Google-managed services (Cloud SQL) in the network.
resource "google_compute_global_address" "private_services" {
  name          = "${var.name}-private-services"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = google_compute_network.this.id

  # Google picks a free range: the subnet must exist first, or both could
  # end up with the same addresses.
  depends_on = [google_compute_subnetwork.cloud_run]
}

resource "google_service_networking_connection" "private_services" {
  network                 = google_compute_network.this.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_services.name]
}

# Subnet of Cloud Run (direct VPC egress): its instances reach Cloud SQL's
# private IP from here.
resource "google_compute_subnetwork" "cloud_run" {
  name          = "${var.name}-cloud-run"
  network       = google_compute_network.this.id
  ip_cidr_range = var.cloud_run_cidr
}
