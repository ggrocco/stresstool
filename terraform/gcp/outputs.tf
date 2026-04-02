output "controller_external_ip" {
  description = "External IP of the controller instance"
  value       = google_compute_instance.controller.network_interface[0].access_config[0].nat_ip
}

output "controller_internal_ip" {
  description = "Internal IP of the controller instance"
  value       = google_compute_instance.controller.network_interface[0].network_ip
}

output "node_external_ips" {
  description = "External IPs of the worker nodes"
  value       = google_compute_instance.node[*].network_interface[0].access_config[0].nat_ip
}

output "node_internal_ips" {
  description = "Internal IPs of the worker nodes"
  value       = google_compute_instance.node[*].network_interface[0].network_ip
}

output "ssh_command_controller" {
  description = "SSH command to connect to the controller"
  value       = "gcloud compute ssh stresstool-controller --zone ${var.zone}"
}
