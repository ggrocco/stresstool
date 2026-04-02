output "controller_public_ip" {
  description = "Public IP of the controller instance"
  value       = aws_instance.controller.public_ip
}

output "controller_private_ip" {
  description = "Private IP of the controller instance"
  value       = aws_instance.controller.private_ip
}

output "node_public_ips" {
  description = "Public IPs of the worker nodes"
  value       = aws_instance.node[*].public_ip
}

output "node_private_ips" {
  description = "Private IPs of the worker nodes"
  value       = aws_instance.node[*].private_ip
}

output "ssh_command_controller" {
  description = "SSH command to connect to the controller"
  value       = "ssh -i <key>.pem ubuntu@${aws_instance.controller.public_ip}"
}
