output "controller_public_ip" {
  description = "Public IP of the controller VM"
  value       = azurerm_public_ip.controller.ip_address
}

output "controller_private_ip" {
  description = "Private IP of the controller VM"
  value       = azurerm_network_interface.controller.private_ip_address
}

output "node_public_ips" {
  description = "Public IPs of the worker node VMs"
  value       = azurerm_public_ip.node[*].ip_address
}

output "node_private_ips" {
  description = "Private IPs of the worker node VMs"
  value       = azurerm_network_interface.node[*].private_ip_address
}

output "ssh_command_controller" {
  description = "SSH command to connect to the controller"
  value       = "ssh ${var.admin_username}@${azurerm_public_ip.controller.ip_address}"
}
