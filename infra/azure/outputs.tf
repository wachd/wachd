# ============================================================================
# Outputs — used by deploy-azure.sh to wire up Helm values and K8s secrets
# ============================================================================

output "resource_group_name" {
  description = "Name of the created resource group"
  value       = azurerm_resource_group.wachd.name
}

output "aks_cluster_name" {
  description = "Name of the AKS cluster"
  value       = azurerm_kubernetes_cluster.wachd.name
}

output "aks_resource_group" {
  description = "Resource group of the AKS cluster (may differ from main RG for node RG)"
  value       = azurerm_resource_group.wachd.name
}

output "aks_get_credentials_command" {
  description = "Command to configure kubectl to connect to this cluster"
  value       = "az aks get-credentials --resource-group ${azurerm_resource_group.wachd.name} --name ${azurerm_kubernetes_cluster.wachd.name} --overwrite-existing"
}

# ============================================================================
# PostgreSQL
# ============================================================================

output "postgres_host" {
  description = "PostgreSQL Flexible Server hostname (use in Helm values)"
  value       = "${azurerm_postgresql_flexible_server.wachd.name}.postgres.database.azure.com"
}

output "postgres_port" {
  description = "PostgreSQL port"
  value       = 5432
}

output "postgres_database" {
  description = "PostgreSQL database name"
  value       = azurerm_postgresql_flexible_server_database.wachd.name
}

output "postgres_username" {
  description = "PostgreSQL admin username"
  value       = "${var.postgres_admin_login}@${azurerm_postgresql_flexible_server.wachd.name}"
}

output "postgres_password" {
  description = "PostgreSQL admin password (also stored in Key Vault)"
  value       = random_password.postgres.result
  sensitive   = true
}

output "postgres_connection_string_template" {
  description = "PostgreSQL connection string template — substitute password from postgres_password output"
  value       = "postgresql://${var.postgres_admin_login}@${azurerm_postgresql_flexible_server.wachd.name}.postgres.database.azure.com:5432/wachd?sslmode=require"
}

# ============================================================================
# Redis
# ============================================================================

output "redis_host" {
  description = "Redis cache hostname (use in Helm values)"
  value       = azurerm_redis_cache.wachd.hostname
}

output "redis_port_tls" {
  description = "Redis TLS port (Azure Redis always uses 6380 for TLS)"
  value       = azurerm_redis_cache.wachd.ssl_port
}

output "redis_primary_key" {
  description = "Redis primary access key (also stored in Key Vault)"
  value       = azurerm_redis_cache.wachd.primary_access_key
  sensitive   = true
}

# ============================================================================
# Entra / Azure AD
# ============================================================================

output "entra_tenant_id" {
  description = "Azure AD tenant ID (use in Helm values auth.entra.tenantId)"
  value       = data.azurerm_client_config.current.tenant_id
}

output "entra_client_id" {
  description = "Entra application client ID (use in Helm values auth.entra.clientId)"
  value       = azuread_application.wachd.client_id
}

output "entra_client_secret" {
  description = "Entra client secret (also stored in Key Vault — create K8s secret with this)"
  value       = azuread_application_password.wachd.value
  sensitive   = true
}

output "entra_object_id" {
  description = "Entra application object ID"
  value       = azuread_application.wachd.object_id
}

output "entra_admin_consent_url" {
  description = "URL to grant admin consent for GroupMember.Read.All permission"
  value       = "https://login.microsoftonline.com/${data.azurerm_client_config.current.tenant_id}/adminconsent?client_id=${azuread_application.wachd.client_id}"
}

# ============================================================================
# Key Vault
# ============================================================================

output "key_vault_name" {
  description = "Azure Key Vault name"
  value       = azurerm_key_vault.wachd.name
}

output "key_vault_uri" {
  description = "Azure Key Vault URI"
  value       = azurerm_key_vault.wachd.vault_uri
}

# ============================================================================
# Encryption Key
# ============================================================================

output "wachd_encryption_key" {
  description = "AES-256 hex encryption key for Wachd secrets (also stored in Key Vault)"
  value       = random_id.wachd_encryption_key.hex
  sensitive   = true
}

# ============================================================================
# ACR (if created)
# ============================================================================

output "acr_login_server" {
  description = "ACR login server URL (null if create_acr=false)"
  value       = var.create_acr ? azurerm_container_registry.wachd[0].login_server : null
}

output "acr_name" {
  description = "ACR name (null if create_acr=false)"
  value       = var.create_acr ? azurerm_container_registry.wachd[0].name : null
}

# ============================================================================
# Log Analytics
# ============================================================================

output "log_analytics_workspace_id" {
  description = "Log Analytics workspace ID for monitoring"
  value       = azurerm_log_analytics_workspace.wachd.id
}

# ============================================================================
# Prometheus + Grafana
# ============================================================================

output "grafana_endpoint" {
  description = "Azure Managed Grafana URL"
  value       = azurerm_dashboard_grafana.wachd.endpoint
}

output "prometheus_query_endpoint" {
  description = "Azure Monitor Workspace Prometheus query endpoint"
  value       = azurerm_monitor_workspace.wachd.query_endpoint
}

# ============================================================================
# Summary (non-sensitive — safe to print in CI logs)
# ============================================================================

output "deployment_summary" {
  description = "Non-sensitive deployment summary — safe to log in CI"
  value = {
    resource_group    = azurerm_resource_group.wachd.name
    location          = azurerm_resource_group.wachd.location
    aks_cluster       = azurerm_kubernetes_cluster.wachd.name
    postgres_host     = "${azurerm_postgresql_flexible_server.wachd.name}.postgres.database.azure.com"
    redis_host        = azurerm_redis_cache.wachd.hostname
    redis_tls_port    = azurerm_redis_cache.wachd.ssl_port
    entra_tenant_id   = data.azurerm_client_config.current.tenant_id
    entra_client_id   = azuread_application.wachd.client_id
    key_vault         = azurerm_key_vault.wachd.name
    acr_server        = var.create_acr ? azurerm_container_registry.wachd[0].login_server : "not created (using ghcr.io)"
    wachd_url         = "https://${var.wachd_hostname}"
    grafana_url       = azurerm_dashboard_grafana.wachd.endpoint
    prometheus_url    = azurerm_monitor_workspace.wachd.query_endpoint
    admin_consent     = "https://login.microsoftonline.com/${data.azurerm_client_config.current.tenant_id}/adminconsent?client_id=${azuread_application.wachd.client_id}"
  }
}
