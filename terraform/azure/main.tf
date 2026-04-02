terraform {
  required_version = ">= 1.5"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
  }
}

provider "azurerm" {
  features {}
}

# --- Resource Group ---

resource "azurerm_resource_group" "stresstool" {
  name     = "stresstool-rg"
  location = var.location
}

# --- Networking ---

resource "azurerm_virtual_network" "stresstool" {
  name                = "stresstool-vnet"
  address_space       = ["10.0.0.0/16"]
  location            = azurerm_resource_group.stresstool.location
  resource_group_name = azurerm_resource_group.stresstool.name
}

resource "azurerm_subnet" "stresstool" {
  name                 = "stresstool-subnet"
  resource_group_name  = azurerm_resource_group.stresstool.name
  virtual_network_name = azurerm_virtual_network.stresstool.name
  address_prefixes     = ["10.0.1.0/24"]
}

# --- Network Security Groups ---

resource "azurerm_network_security_group" "controller" {
  name                = "stresstool-controller-nsg"
  location            = azurerm_resource_group.stresstool.location
  resource_group_name = azurerm_resource_group.stresstool.name

  security_rule {
    name                       = "SSH"
    priority                   = 1001
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_range     = "22"
    source_address_prefix      = var.allowed_ssh_cidr
    destination_address_prefix = "*"
  }

  security_rule {
    name                       = "Controller"
    priority                   = 1002
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_range     = tostring(var.controller_port)
    source_address_prefix      = "10.0.0.0/16"
    destination_address_prefix = "*"
  }
}

resource "azurerm_network_security_group" "node" {
  name                = "stresstool-node-nsg"
  location            = azurerm_resource_group.stresstool.location
  resource_group_name = azurerm_resource_group.stresstool.name

  security_rule {
    name                       = "SSH"
    priority                   = 1001
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_range     = "22"
    source_address_prefix      = var.allowed_ssh_cidr
    destination_address_prefix = "*"
  }
}

# --- Controller VM ---

resource "azurerm_public_ip" "controller" {
  name                = "stresstool-controller-pip"
  location            = azurerm_resource_group.stresstool.location
  resource_group_name = azurerm_resource_group.stresstool.name
  allocation_method   = "Static"
  sku                 = "Standard"
}

resource "azurerm_network_interface" "controller" {
  name                = "stresstool-controller-nic"
  location            = azurerm_resource_group.stresstool.location
  resource_group_name = azurerm_resource_group.stresstool.name

  ip_configuration {
    name                          = "internal"
    subnet_id                     = azurerm_subnet.stresstool.id
    private_ip_address_allocation = "Dynamic"
    public_ip_address_id          = azurerm_public_ip.controller.id
  }
}

resource "azurerm_network_interface_security_group_association" "controller" {
  network_interface_id      = azurerm_network_interface.controller.id
  network_security_group_id = azurerm_network_security_group.controller.id
}

resource "azurerm_linux_virtual_machine" "controller" {
  name                = "stresstool-controller"
  resource_group_name = azurerm_resource_group.stresstool.name
  location            = azurerm_resource_group.stresstool.location
  size                = var.vm_size
  admin_username      = var.admin_username

  network_interface_ids = [azurerm_network_interface.controller.id]

  admin_ssh_key {
    username   = var.admin_username
    public_key = file(var.ssh_public_key_path)
  }

  os_disk {
    caching              = "ReadWrite"
    storage_account_type = "Standard_LRS"
    disk_size_gb         = 30
  }

  source_image_reference {
    publisher = "Canonical"
    offer     = "0001-com-ubuntu-server-jammy"
    sku       = "22_04-lts"
    version   = "latest"
  }

  custom_data = base64encode(<<-EOF
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
  )
}

# --- Worker Node VMs ---

resource "azurerm_public_ip" "node" {
  count               = var.node_count
  name                = "stresstool-node-${count.index}-pip"
  location            = azurerm_resource_group.stresstool.location
  resource_group_name = azurerm_resource_group.stresstool.name
  allocation_method   = "Static"
  sku                 = "Standard"
}

resource "azurerm_network_interface" "node" {
  count               = var.node_count
  name                = "stresstool-node-${count.index}-nic"
  location            = azurerm_resource_group.stresstool.location
  resource_group_name = azurerm_resource_group.stresstool.name

  ip_configuration {
    name                          = "internal"
    subnet_id                     = azurerm_subnet.stresstool.id
    private_ip_address_allocation = "Dynamic"
    public_ip_address_id          = azurerm_public_ip.node[count.index].id
  }
}

resource "azurerm_network_interface_security_group_association" "node" {
  count                     = var.node_count
  network_interface_id      = azurerm_network_interface.node[count.index].id
  network_security_group_id = azurerm_network_security_group.node.id
}

resource "azurerm_linux_virtual_machine" "node" {
  count               = var.node_count
  name                = "stresstool-node-${count.index}"
  resource_group_name = azurerm_resource_group.stresstool.name
  location            = azurerm_resource_group.stresstool.location
  size                = var.vm_size
  admin_username      = var.admin_username

  network_interface_ids = [azurerm_network_interface.node[count.index].id]

  admin_ssh_key {
    username   = var.admin_username
    public_key = file(var.ssh_public_key_path)
  }

  os_disk {
    caching              = "ReadWrite"
    storage_account_type = "Standard_LRS"
    disk_size_gb         = 30
  }

  source_image_reference {
    publisher = "Canonical"
    offer     = "0001-com-ubuntu-server-jammy"
    sku       = "22_04-lts"
    version   = "latest"
  }

  custom_data = base64encode(<<-EOF
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
    ExecStart=/usr/local/bin/stresstool node --node-name node-${count.index} --controller ${azurerm_network_interface.controller.private_ip_address}:${var.controller_port}
    Restart=on-failure

    [Install]
    WantedBy=multi-user.target
    SVC

    systemctl daemon-reload
    systemctl enable --now stresstool-node
  EOF
  )

  depends_on = [azurerm_linux_virtual_machine.controller]
}
