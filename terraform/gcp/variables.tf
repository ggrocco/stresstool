variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region"
  type        = string
  default     = "us-central1"
}

variable "zone" {
  description = "GCP zone"
  type        = string
  default     = "us-central1-a"
}

variable "machine_type" {
  description = "GCE machine type for controller and nodes"
  type        = string
  default     = "e2-medium"
}

variable "node_count" {
  description = "Number of worker nodes"
  type        = number
  default     = 3
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
