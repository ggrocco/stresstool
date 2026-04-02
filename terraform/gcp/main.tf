terraform {
  required_version = ">= 1.5"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
  zone    = var.zone
}

# --- Networking ---

resource "google_compute_network" "stresstool" {
  name                    = "stresstool-network"
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "stresstool" {
  name          = "stresstool-subnet"
  ip_cidr_range = "10.0.1.0/24"
  network       = google_compute_network.stresstool.id
  region        = var.region
}

# --- Firewall Rules ---

resource "google_compute_firewall" "allow_ssh" {
  name    = "stresstool-allow-ssh"
  network = google_compute_network.stresstool.name

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  source_ranges = [var.allowed_ssh_cidr]
  target_tags   = ["stresstool"]
}

resource "google_compute_firewall" "allow_controller" {
  name    = "stresstool-allow-controller"
  network = google_compute_network.stresstool.name

  allow {
    protocol = "tcp"
    ports    = [tostring(var.controller_port)]
  }

  source_tags = ["stresstool-node"]
  target_tags = ["stresstool-controller"]
}

resource "google_compute_firewall" "allow_internal" {
  name    = "stresstool-allow-internal"
  network = google_compute_network.stresstool.name

  allow {
    protocol = "tcp"
  }

  allow {
    protocol = "udp"
  }

  allow {
    protocol = "icmp"
  }

  source_ranges = ["10.0.1.0/24"]
}

# --- Controller Instance ---

resource "google_compute_instance" "controller" {
  name         = "stresstool-controller"
  machine_type = var.machine_type
  zone         = var.zone

  tags = ["stresstool", "stresstool-controller"]

  boot_disk {
    initialize_params {
      image = "ubuntu-os-cloud/ubuntu-2204-lts"
      size  = 20
    }
  }

  network_interface {
    subnetwork = google_compute_subnetwork.stresstool.id
    access_config {} # Assigns an external IP
  }

  metadata_startup_script = <<-EOF
    #!/bin/bash
    set -euo pipefail
    apt-get update && apt-get install -y golang-go git

    cd /opt
    git clone https://github.com/ggrocco/stresstool.git
    cd stresstool
    go build -o /usr/local/bin/stresstool ./cmd/stresstool

    cat > /opt/stresstool/config.yaml <<'CONFIG'
    ${file(var.stresstool_config)}
    CONFIG

    cat > /etc/systemd/system/stresstool-controller.service <<'SVC'
    [Unit]
    Description=Stresstool Controller
    After=network.target

    [Service]
    ExecStart=/usr/local/bin/stresstool controller -f /opt/stresstool/config.yaml --listen :${var.controller_port}
    Restart=on-failure
    WorkingDirectory=/opt/stresstool

    [Install]
    WantedBy=multi-user.target
    SVC

    systemctl daemon-reload
    systemctl enable --now stresstool-controller
  EOF
}

# --- Worker Node Instances ---

resource "google_compute_instance" "node" {
  count        = var.node_count
  name         = "stresstool-node-${count.index}"
  machine_type = var.machine_type
  zone         = var.zone

  tags = ["stresstool", "stresstool-node"]

  boot_disk {
    initialize_params {
      image = "ubuntu-os-cloud/ubuntu-2204-lts"
      size  = 20
    }
  }

  network_interface {
    subnetwork = google_compute_subnetwork.stresstool.id
    access_config {}
  }

  metadata_startup_script = <<-EOF
    #!/bin/bash
    set -euo pipefail
    apt-get update && apt-get install -y golang-go git

    cd /opt
    git clone https://github.com/ggrocco/stresstool.git
    cd stresstool
    go build -o /usr/local/bin/stresstool ./cmd/stresstool

    cat > /etc/systemd/system/stresstool-node.service <<SVC
    [Unit]
    Description=Stresstool Worker Node
    After=network.target

    [Service]
    ExecStart=/usr/local/bin/stresstool node --node-name node-${count.index} --controller ${google_compute_instance.controller.network_interface[0].network_ip}:${var.controller_port}
    Restart=on-failure

    [Install]
    WantedBy=multi-user.target
    SVC

    systemctl daemon-reload
    systemctl enable --now stresstool-node
  EOF

  depends_on = [google_compute_instance.controller]
}
