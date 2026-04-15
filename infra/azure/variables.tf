# ============================================================================
# Required Variables
# ============================================================================

variable "location" {
  description = "Azure region for all resources"
  type        = string
  default     = "uksouth"
  # Common options: eastus, westeurope, uksouth, australiaeast
}

variable "environment" {
  description = "Environment name — used in all resource names and tags"
  type        = string
  default     = "prod"
  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "environment must be dev, staging, or prod."
  }
}

variable "resource_group_name" {
  description = "Name of the Azure resource group to create"
  type        = string
  default     = "rg-wachd"
}

variable "wachd_hostname" {
  description = "Public hostname for Wachd (used in TLS cert and Entra redirect URI)"
  type        = string
  # Example: "wachd.mycompany.com" or "wachd.20.1.2.3.nip.io"
}

# ============================================================================
# AKS Configuration
# ============================================================================

variable "aks_kubernetes_version" {
  description = "Kubernetes version for the AKS cluster"
  type        = string
  default     = "1.32"
  # Run: az aks get-versions --location <region> --query "values[?isPreview==null].version" -o tsv
}

variable "aks_system_node_count" {
  description = "Number of nodes in the system node pool"
  type        = number
  default     = 3
}

variable "aks_system_vm_size" {
  description = "VM size for system node pool (Standard_D2s_v3 = 2vCPU, 8GB)"
  type        = string
  default     = "Standard_D2s_v3"
  # Minimum for Wachd: Standard_D2s_v3
  # Enterprise recommended: Standard_D4s_v3 (4vCPU, 16GB)
}

variable "aks_enable_auto_scaling" {
  description = "Enable cluster autoscaler on the system node pool"
  type        = bool
  default     = false
}

variable "aks_min_node_count" {
  description = "Minimum node count when autoscaling is enabled"
  type        = number
  default     = 2
}

variable "aks_max_node_count" {
  description = "Maximum node count when autoscaling is enabled"
  type        = number
  default     = 10
}

# ============================================================================
# PostgreSQL Configuration
# ============================================================================

variable "postgres_admin_login" {
  description = "Administrator username for PostgreSQL Flexible Server"
  type        = string
  default     = "wachdadmin"
}

variable "postgres_sku" {
  description = "PostgreSQL Flexible Server SKU"
  type        = string
  default     = "B_Standard_B2ms"
  # Dev/test:    B_Standard_B2ms  (2 vCores, 8 GB) ~$50/month
  # Production:  GP_Standard_D4s  (4 vCores, 16 GB) ~$300/month
}

variable "postgres_storage_mb" {
  description = "Storage size for PostgreSQL in MB"
  type        = number
  default     = 32768  # 32 GB
}

variable "postgres_version" {
  description = "PostgreSQL major version"
  type        = string
  default     = "16"
}

variable "postgres_backup_retention_days" {
  description = "Number of days to retain backups"
  type        = number
  default     = 7
}

variable "postgres_geo_redundant_backup" {
  description = "Enable geo-redundant backups (requires Premium storage tier)"
  type        = bool
  default     = false
}

# ============================================================================
# Redis Configuration
# ============================================================================

variable "redis_sku" {
  description = "Azure Cache for Redis SKU"
  type        = string
  default     = "Standard"
  # Basic    = single node, no SLA (dev only)
  # Standard = 2 nodes with replication (recommended)
  # Premium  = cluster mode, persistence, zone redundancy
}

variable "redis_capacity" {
  description = "Redis cache size (0=250MB, 1=1GB, 2=6GB, 3=13GB, 4=26GB, 5=53GB, 6=120GB)"
  type        = number
  default     = 1  # 1 GB — sufficient for Wachd job queue
}

# ============================================================================
# Entra (Azure AD) Configuration
# ============================================================================

variable "entra_app_display_name" {
  description = "Display name for the Entra application registration"
  type        = string
  default     = "Wachd Alert Intelligence"
}

variable "entra_client_secret_expiry" {
  description = "Expiry date for the Entra client secret (ISO 8601)"
  type        = string
  default     = "2028-01-01T00:00:00Z"
}

variable "entra_group_object_ids" {
  description = "Map of Entra group object IDs to Wachd team name + role. Used to seed group mappings."
  type = map(object({
    team_name = string
    role      = string  # viewer | responder | admin
  }))
  default = {}
  # Example:
  # entra_group_object_ids = {
  #   "00000000-aaaa-bbbb-cccc-000000000001" = { team_name = "Payments", role = "responder" }
  #   "00000000-aaaa-bbbb-cccc-000000000002" = { team_name = "Platform", role = "admin" }
  # }
}

# ============================================================================
# Optional: Azure Container Registry
# ============================================================================

variable "create_acr" {
  description = "Create an Azure Container Registry to host the Wachd image"
  type        = bool
  default     = false
  # Set to false if using ghcr.io or another registry
}

variable "acr_sku" {
  description = "ACR SKU (Basic, Standard, Premium)"
  type        = string
  default     = "Basic"
}

# ============================================================================
# Tagging
# ============================================================================

variable "tags" {
  description = "Tags applied to all resources"
  type        = map(string)
  default = {
    Project     = "wachd"
    ManagedBy   = "terraform"
    Environment = "prod"
  }
}
