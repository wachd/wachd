# ============================================================================
# Data Sources
# ============================================================================

data "azurerm_client_config" "current" {}
data "azuread_client_config" "current" {}

# ============================================================================
# Random Passwords
# ============================================================================

resource "random_password" "postgres" {
  length           = 24
  special          = true
  override_special = "!@#$%^&*()-_=+[]{}|;:,.<>?"
  min_upper        = 2
  min_lower        = 2
  min_numeric      = 2
  min_special      = 2
}

resource "random_id" "wachd_encryption_key" {
  byte_length = 32
  # .hex produces 64 lowercase hex characters — matches what Go expects for AES-256-GCM
}

resource "random_string" "acr_suffix" {
  length  = 6
  upper   = false
  special = false
}

# ============================================================================
# Resource Group
# ============================================================================

resource "azurerm_resource_group" "wachd" {
  name     = var.resource_group_name
  location = var.location
  tags     = var.tags
}

# ============================================================================
# AKS Cluster
# ============================================================================

resource "azurerm_kubernetes_cluster" "wachd" {
  name                = "aks-wachd-${var.environment}"
  location            = azurerm_resource_group.wachd.location
  resource_group_name = azurerm_resource_group.wachd.name
  dns_prefix          = "wachd-${var.environment}"
  kubernetes_version  = var.aks_kubernetes_version

  # System node pool — runs Kubernetes system components + Wachd
  default_node_pool {
    name       = "system"
    node_count = var.aks_enable_auto_scaling ? null : var.aks_system_node_count
    vm_size    = var.aks_system_vm_size

    # Autoscaling (optional — set aks_enable_auto_scaling: true)
    enable_auto_scaling = var.aks_enable_auto_scaling
    min_count           = var.aks_enable_auto_scaling ? var.aks_min_node_count : null
    max_count           = var.aks_enable_auto_scaling ? var.aks_max_node_count : null

    # OS disk: ephemeral disks are faster and cheaper
    os_disk_size_gb = 128
    os_disk_type    = "Managed"

    # Availability zones for zone redundancy (requires 3+ nodes)
    zones = var.aks_system_node_count >= 3 ? ["1", "2", "3"] : null

    upgrade_settings {
      max_surge = "33%"
    }
  }

  # Workload Identity (enables pods to authenticate with Azure services — avoids secrets)
  workload_identity_enabled = true
  oidc_issuer_enabled       = true

  # Managed identity for the cluster itself
  identity {
    type = "SystemAssigned"
  }

  # Network policy — enables NetworkPolicy resources in k8s
  network_profile {
    network_plugin    = "azure"
    network_policy    = "azure"
    load_balancer_sku = "standard"
  }

  # Disable local account — use Entra for kubectl access (enterprise security)
  local_account_disabled = false  # set true after configuring Azure RBAC for kubectl

  # Azure Monitor integration
  oms_agent {
    log_analytics_workspace_id = azurerm_log_analytics_workspace.wachd.id
  }

  # Defender for Containers (security scanning)
  microsoft_defender {
    log_analytics_workspace_id = azurerm_log_analytics_workspace.wachd.id
  }

  tags = var.tags
}

# ============================================================================
# Log Analytics Workspace (for AKS monitoring)
# ============================================================================

resource "azurerm_log_analytics_workspace" "wachd" {
  name                = "log-wachd-${var.environment}"
  location            = azurerm_resource_group.wachd.location
  resource_group_name = azurerm_resource_group.wachd.name
  sku                 = "PerGB2018"
  retention_in_days   = 30
  tags                = var.tags
}

# ============================================================================
# PostgreSQL Flexible Server
# ============================================================================

resource "azurerm_postgresql_flexible_server" "wachd" {
  name                = "psql-wachd-${var.environment}"
  location            = azurerm_resource_group.wachd.location
  resource_group_name = azurerm_resource_group.wachd.name

  administrator_login    = var.postgres_admin_login
  administrator_password = random_password.postgres.result

  sku_name   = var.postgres_sku
  version    = var.postgres_version
  storage_mb = var.postgres_storage_mb

  backup_retention_days         = var.postgres_backup_retention_days
  geo_redundant_backup_enabled  = var.postgres_geo_redundant_backup

  # High Availability — omit block to disable (mode="Disabled" is not a valid value)
  # Uncomment and set mode="ZoneRedundant" for production SLA
  # high_availability {
  #   mode = "ZoneRedundant"
  # }

  tags = var.tags

  lifecycle {
    ignore_changes = [zone]
  }
}

resource "azurerm_postgresql_flexible_server_database" "wachd" {
  name      = "wachd"
  server_id = azurerm_postgresql_flexible_server.wachd.id
  charset   = "UTF8"
  collation = "en_US.utf8"
}

# Allow connections from Azure services (AKS cluster)
resource "azurerm_postgresql_flexible_server_firewall_rule" "allow_azure" {
  name             = "AllowAllAzureServices"
  server_id        = azurerm_postgresql_flexible_server.wachd.id
  start_ip_address = "0.0.0.0"
  end_ip_address   = "0.0.0.0"
}

# ============================================================================
# Azure Cache for Redis
# ============================================================================

resource "azurerm_redis_cache" "wachd" {
  name                = "redis-wachd-${var.environment}"
  location            = azurerm_resource_group.wachd.location
  resource_group_name = azurerm_resource_group.wachd.name

  sku_name  = var.redis_sku
  family    = var.redis_sku == "Premium" ? "P" : "C"
  capacity  = var.redis_capacity

  # Security: TLS only, no plaintext port
  non_ssl_port_enabled = false
  minimum_tls_version  = "1.2"

  # Redis configuration
  redis_configuration {
    maxmemory_policy = "allkeys-lru"  # Evict LRU keys when memory full
  }

  tags = var.tags
}

# ============================================================================
# Entra Application Registration (for SSO)
# ============================================================================

resource "azuread_application" "wachd" {
  display_name = var.entra_app_display_name
  owners       = [data.azuread_client_config.current.object_id]

  sign_in_audience = "AzureADMyOrg"  # Single tenant — only your directory

  web {
    redirect_uris = [
      "https://${var.wachd_hostname}/auth/callback",
    ]

    implicit_grant {
      access_token_issuance_enabled = false
      id_token_issuance_enabled     = true
    }
  }

  # Microsoft Graph permissions — needed to read user's group memberships
  required_resource_access {
    # Microsoft Graph
    resource_app_id = "00000003-0000-0000-c000-000000000000"

    # User.Read — delegated (lets users sign in and read their own profile)
    resource_access {
      id   = "e1fe6dd8-ba31-4d61-89e7-88639da4683d"
      type = "Scope"
    }

    # GroupMember.Read.All — application (lets Wachd read user's group memberships on login)
    resource_access {
      id   = "98830695-27a2-44f7-8c18-0c3ebc9698f6"
      type = "Role"
    }
  }

  # Token configuration — include groups claim in ID token
  optional_claims {
    id_token {
      name = "groups"
    }
  }

  tags = ["wachd", var.environment]
}

resource "azuread_service_principal" "wachd" {
  client_id                    = azuread_application.wachd.client_id
  app_role_assignment_required = false
  owners                       = [data.azuread_client_config.current.object_id]
}

resource "azuread_application_password" "wachd" {
  application_id = azuread_application.wachd.id   # object_id of the app registration
  display_name   = "wachd-helm-secret"
  end_date       = var.entra_client_secret_expiry
}

# ============================================================================
# Optional: Azure Container Registry
# ============================================================================

resource "azurerm_container_registry" "wachd" {
  count = var.create_acr ? 1 : 0

  name                = "acrwachd${random_string.acr_suffix.result}"
  resource_group_name = azurerm_resource_group.wachd.name
  location            = azurerm_resource_group.wachd.location
  sku                 = var.acr_sku
  admin_enabled       = false  # Use Managed Identity instead

  tags = var.tags
}

# Grant AKS pull access to ACR (Managed Identity — no secrets needed)
resource "azurerm_role_assignment" "aks_acr_pull" {
  count = var.create_acr ? 1 : 0

  principal_id         = azurerm_kubernetes_cluster.wachd.kubelet_identity[0].object_id
  role_definition_name = "AcrPull"
  scope                = azurerm_container_registry.wachd[0].id
}

# ============================================================================
# Key Vault (for production secret management — optional but recommended)
# ============================================================================

resource "azurerm_key_vault" "wachd" {
  name                = "kv-wachd-${var.environment}"
  location            = azurerm_resource_group.wachd.location
  resource_group_name = azurerm_resource_group.wachd.name
  tenant_id           = data.azurerm_client_config.current.tenant_id

  sku_name                    = "standard"
  soft_delete_retention_days  = 7
  purge_protection_enabled    = false  # Set true for production

  # Access policy for the Terraform deployer
  access_policy {
    tenant_id = data.azurerm_client_config.current.tenant_id
    object_id = data.azurerm_client_config.current.object_id

    secret_permissions = ["Get", "List", "Set", "Delete", "Purge"]
  }

  # Access policy for AKS (to read secrets at deploy time)
  access_policy {
    tenant_id = data.azurerm_client_config.current.tenant_id
    object_id = azurerm_kubernetes_cluster.wachd.identity[0].principal_id

    secret_permissions = ["Get", "List"]
  }

  tags = var.tags
}

# Store all secrets in Key Vault
resource "azurerm_key_vault_secret" "postgres_password" {
  name         = "wachd-postgres-password"
  value        = random_password.postgres.result
  key_vault_id = azurerm_key_vault.wachd.id
  tags         = var.tags
}

resource "azurerm_key_vault_secret" "redis_password" {
  name         = "wachd-redis-password"
  value        = azurerm_redis_cache.wachd.primary_access_key
  key_vault_id = azurerm_key_vault.wachd.id
  tags         = var.tags
}

resource "azurerm_key_vault_secret" "entra_client_secret" {
  name         = "wachd-entra-client-secret"
  value        = azuread_application_password.wachd.value
  key_vault_id = azurerm_key_vault.wachd.id
  tags         = var.tags
}

resource "azurerm_key_vault_secret" "encryption_key" {
  name         = "wachd-encryption-key"
  value        = random_id.wachd_encryption_key.hex
  key_vault_id = azurerm_key_vault.wachd.id
  tags         = var.tags
}
