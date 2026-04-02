terraform {
  required_version = ">= 1.5"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

data "aws_ami" "ubuntu" {
  most_recent = true
  owners      = ["099720109477"] # Canonical

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

# --- Networking ---

resource "aws_vpc" "stresstool" {
  cidr_block           = "10.0.0.0/16"
  enable_dns_hostnames = true

  tags = { Name = "stresstool-vpc" }
}

resource "aws_internet_gateway" "gw" {
  vpc_id = aws_vpc.stresstool.id

  tags = { Name = "stresstool-igw" }
}

resource "aws_subnet" "public" {
  vpc_id                  = aws_vpc.stresstool.id
  cidr_block              = "10.0.1.0/24"
  map_public_ip_on_launch = true

  tags = { Name = "stresstool-public" }
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.stresstool.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.gw.id
  }

  tags = { Name = "stresstool-public-rt" }
}

resource "aws_route_table_association" "public" {
  subnet_id      = aws_subnet.public.id
  route_table_id = aws_route_table.public.id
}

# --- Security Groups ---

resource "aws_security_group" "controller" {
  name_prefix = "stresstool-controller-"
  vpc_id      = aws_vpc.stresstool.id

  ingress {
    description = "SSH"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = [var.allowed_ssh_cidr]
  }

  ingress {
    description = "Controller port"
    from_port   = var.controller_port
    to_port     = var.controller_port
    protocol    = "tcp"
    cidr_blocks = [aws_vpc.stresstool.cidr_block]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "stresstool-controller-sg" }
}

resource "aws_security_group" "node" {
  name_prefix = "stresstool-node-"
  vpc_id      = aws_vpc.stresstool.id

  ingress {
    description = "SSH"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = [var.allowed_ssh_cidr]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "stresstool-node-sg" }
}

# --- Controller Instance ---

resource "aws_instance" "controller" {
  ami                    = data.aws_ami.ubuntu.id
  instance_type          = var.instance_type
  key_name               = var.key_name
  subnet_id              = aws_subnet.public.id
  vpc_security_group_ids = [aws_security_group.controller.id]

  user_data = <<-EOF
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

    # Start the controller as a systemd service
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

  tags = { Name = "stresstool-controller" }
}

# --- Worker Node Instances ---

resource "aws_instance" "node" {
  count                  = var.node_count
  ami                    = data.aws_ami.ubuntu.id
  instance_type          = var.instance_type
  key_name               = var.key_name
  subnet_id              = aws_subnet.public.id
  vpc_security_group_ids = [aws_security_group.node.id]

  user_data = <<-EOF
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
    ExecStart=/usr/local/bin/stresstool node --node-name node-${count.index} --controller ${aws_instance.controller.private_ip}:${var.controller_port}
    Restart=on-failure

    [Install]
    WantedBy=multi-user.target
    SVC

    systemctl daemon-reload
    systemctl enable --now stresstool-node
  EOF

  tags = { Name = "stresstool-node-${count.index}" }

  depends_on = [aws_instance.controller]
}
