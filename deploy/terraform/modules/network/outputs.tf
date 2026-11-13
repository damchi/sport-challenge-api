output "network_id" {
  description = "Id of the VPC network."
  value       = google_compute_network.this.id
}

output "private_services_connection" {
  description = "Id of the private services connection; Cloud SQL must wait for it."
  value       = google_service_networking_connection.private_services.id
}

output "cloud_run_subnet_id" {
  description = "Id of the subnet Cloud Run connects through."
  value       = google_compute_subnetwork.cloud_run.id
}
