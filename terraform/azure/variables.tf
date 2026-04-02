variable "location" {
  description = "Azure region to deploy resources"
  type        = string
  default     = "East US"
}

variable "vm_size" {
  description = "Azure VM size for controller and nodes"
  type        = string
  default     = "Standard_B2s"
}

variable "node_count" {
  description = "Number of worker nodes"
  type        = number
  default     = 3
}

variable "admin_username" {
  description = "Admin username for VMs"
  type        = string
  default     = "azureuser"
}

variable "ssh_public_key_path" {
  description = "Path to SSH public key for VM access"
  type        = string
  default     = "~/.ssh/id_rsa.pub"
}

variable "allowed_ssh_cidr" {
  description = "CIDR block allowed to SSH into instances"
  type        = string
  default     = "0.0.0.0/0"
}

variable "stresstool_config" {
  description = "Path to the stresstool YAML configuration file"
  type        = string
  default     = "../../example-config.yaml"
}

variable "controller_port" {
  description = "Port the controller listens on"
  type        = number
  default     = 8090
}
